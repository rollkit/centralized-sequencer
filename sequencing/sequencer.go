package sequencing

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dgraph-io/badger/v3"
	logging "github.com/ipfs/go-log/v2"

	"github.com/rollkit/centralized-sequencer/da"
	goda "github.com/rollkit/go-da"
	proxyda "github.com/rollkit/go-da/proxy"
	"github.com/rollkit/go-sequencing"
)

// ErrInvalidRollupId is returned when the rollup id is invalid
var ErrInvalidRollupId = errors.New("invalid rollup id")

var _ sequencing.Sequencer = &Sequencer{}
var log = logging.Logger("centralized-sequencer")

const maxSubmitAttempts = 30
const defaultMempoolTTL = 25

var initialBackoff = 100 * time.Millisecond

// BatchQueue ...
type BatchQueue struct {
	queue []sequencing.Batch
	mu    sync.Mutex
}

// NewBatchQueue creates a new BatchQueue
func NewBatchQueue() *BatchQueue {
	return &BatchQueue{
		queue: make([]sequencing.Batch, 0),
	}
}

// AddBatch adds a new transaction to the queue
func (bq *BatchQueue) AddBatch(batch sequencing.Batch, db *badger.DB) error {
	bq.mu.Lock()
	bq.queue = append(bq.queue, batch)
	bq.mu.Unlock()

	// Get the hash and bytes of the batch
	h, err := batch.Hash()
	if err != nil {
		return err
	}

	// Marshal the batch
	batchBytes, err := batch.Marshal()
	if err != nil {
		return err
	}

	// Store the batch in BadgerDB
	err = db.Update(func(txn *badger.Txn) error {
		return txn.Set(h, batchBytes)
	})
	return err
}

// Next extracts a batch of transactions from the queue
func (bq *BatchQueue) Next(db *badger.DB) (*sequencing.Batch, error) {
	bq.mu.Lock()
	defer bq.mu.Unlock()
	if len(bq.queue) == 0 {
		return &sequencing.Batch{Transactions: nil}, nil
	}
	batch := bq.queue[0]
	bq.queue = bq.queue[1:]

	h, err := batch.Hash()
	if err != nil {
		return &sequencing.Batch{Transactions: nil}, err
	}

	// Remove the batch from BadgerDB after processing
	err = db.Update(func(txn *badger.Txn) error {
		// Get the batch to ensure it exists in the DB before deleting
		_, err := txn.Get(h)
		if err != nil {
			return err
		}
		// Delete the batch from BadgerDB
		return txn.Delete(h)
	})
	if err != nil {
		return &sequencing.Batch{Transactions: nil}, err
	}

	return &batch, nil
}

// LoadFromDB reloads all batches from BadgerDB into the in-memory queue after a crash or restart.
func (bq *BatchQueue) LoadFromDB(db *badger.DB) error {
	bq.mu.Lock()
	defer bq.mu.Unlock()

	err := db.View(func(txn *badger.Txn) error {
		// Create an iterator to go through all batches stored in BadgerDB
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				var batch sequencing.Batch
				// Unmarshal the batch bytes and add them to the in-memory queue
				err := batch.Unmarshal(val)
				if err != nil {
					return err
				}
				bq.queue = append(bq.queue, batch)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})

	return err
}

// Sequencer implements go-sequencing interface
type Sequencer struct {
	dalc      *da.DAClient
	batchTime time.Duration
	ctx       context.Context
	maxSize   uint64

	rollupId sequencing.RollupId

	tq                 *TransactionQueue
	lastBatchHash      []byte
	lastBatchHashMutex sync.RWMutex

	seenBatches      map[string]struct{}
	seenBatchesMutex sync.Mutex
	bq               *BatchQueue

	db    *badger.DB // BadgerDB instance for persistence
	dbMux sync.Mutex // Mutex for safe concurrent DB access

	metrics *Metrics
}

// NewSequencer ...
func NewSequencer(daAddress, daAuthToken string, daNamespace []byte, rollupId []byte, batchTime time.Duration, metrics *Metrics, dbPath string, extender BatchExtender) (*Sequencer, error) {
	ctx := context.Background()
	dac, err := proxyda.NewClient(daAddress, daAuthToken)
	if err != nil {
		return nil, fmt.Errorf("error while establishing connection to DA layer: %w", err)
	}
	dalc := da.NewDAClient(dac, -1, 0, goda.Namespace(daNamespace)) // nolint:unconvert
	maxBlobSize, err := dalc.DA.MaxBlobSize(ctx)
	if err != nil {
		return nil, err
	}

	// Initialize BadgerDB
	var opts badger.Options
	if dbPath == "" {
		opts = badger.DefaultOptions("").WithInMemory(true)
	} else {
		opts = badger.DefaultOptions(dbPath)
	}
	opts = opts.WithLogger(nil)
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open BadgerDB: %w", err)
	}
	s := &Sequencer{
		dalc:        dalc,
		batchTime:   batchTime,
		ctx:         ctx,
		maxSize:     maxBlobSize,
		rollupId:    rollupId,
		tq:          NewTransactionQueue(extender),
		bq:          NewBatchQueue(),
		seenBatches: make(map[string]struct{}),
		db:          db,
		metrics:     metrics,
	}

	// Load last batch hash from DB to recover from crash
	err = s.LoadLastBatchHashFromDB()
	if err != nil {
		return nil, fmt.Errorf("failed to load last batch hash from DB: %w", err)
	}

	// Load seen batches from DB to recover from crash
	err = s.LoadSeenBatchesFromDB()
	if err != nil {
		return nil, fmt.Errorf("failed to load seen batches from DB: %w", err)
	}

	// Load TransactionQueue and BatchQueue from DB to recover from crash
	err = s.tq.LoadFromDB(s.db) // Load transactions
	if err != nil {
		return nil, fmt.Errorf("failed to load transaction queue from DB: %w", err)
	}
	err = s.bq.LoadFromDB(s.db) // Load batches
	if err != nil {
		return nil, fmt.Errorf("failed to load batch queue from DB: %w", err)
	}

	go s.batchSubmissionLoop(s.ctx)
	return s, nil
}

// Close safely closes the BadgerDB instance if it is open
func (c *Sequencer) Close() error {
	if c.db != nil {
		err := c.db.Close()
		if err != nil {
			return fmt.Errorf("failed to close BadgerDB: %w", err)
		}
	}
	return nil
}

// CompareAndSetMaxSize compares the passed size with the current max size and sets the max size to the smaller of the two
// Initially the max size is set to the max blob size returned by the DA layer
// This can be overwritten by the execution client if it can only handle smaller size
func (c *Sequencer) CompareAndSetMaxSize(size uint64) {
	for {
		current := atomic.LoadUint64(&c.maxSize)
		if size >= current {
			return
		}
		if atomic.CompareAndSwapUint64(&c.maxSize, current, size) {
			return
		}
	}
}

// LoadLastBatchHashFromDB loads the last batch hash from BadgerDB into memory after a crash or restart.
func (c *Sequencer) LoadLastBatchHashFromDB() error {
	// Lock to ensure concurrency safety
	c.dbMux.Lock()
	defer c.dbMux.Unlock()

	var hash []byte
	// Load the last batch hash from BadgerDB if it exists
	err := c.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte("lastBatchHash"))
		if errors.Is(err, badger.ErrKeyNotFound) {
			// If no last batch hash exists, it's the first time or nothing was processed
			c.lastBatchHash = nil
			return nil
		}
		if err != nil {
			return err
		}
		// Set lastBatchHash in memory from BadgerDB
		return item.Value(func(val []byte) error {
			hash = val
			return nil
		})
	})
	// Set the in-memory lastBatchHash after successfully loading it from DB
	c.lastBatchHash = hash
	return err
}

// LoadSeenBatchesFromDB loads the seen batches from BadgerDB into memory after a crash or restart.
func (c *Sequencer) LoadSeenBatchesFromDB() error {
	c.dbMux.Lock()
	defer c.dbMux.Unlock()

	err := c.db.View(func(txn *badger.Txn) error {
		// Create an iterator to go through all entries in BadgerDB
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			key := item.Key()
			// Add the batch hash to the seenBatches map (for fast in-memory lookups)
			c.seenBatches[string(key)] = struct{}{}
		}
		return nil
	})

	return err
}

func (c *Sequencer) setLastBatchHash(hash []byte) error {
	c.dbMux.Lock()
	defer c.dbMux.Unlock()

	return c.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte("lastBatchHash"), hash)
	})
}

func (c *Sequencer) addSeenBatch(hash []byte) error {
	c.dbMux.Lock()
	defer c.dbMux.Unlock()

	return c.db.Update(func(txn *badger.Txn) error {
		return txn.Set(hash, []byte{1}) // Just to mark the batch as seen
	})
}

func (c *Sequencer) batchSubmissionLoop(ctx context.Context) {
	batchTimer := time.NewTimer(0)
	defer batchTimer.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-batchTimer.C:
		}
		start := time.Now()
		err := c.publishBatch()
		if err != nil && ctx.Err() == nil {
			log.Errorf("error while publishing block", "error", err)
		}
		batchTimer.Reset(getRemainingSleep(start, c.batchTime, 0))
	}
}

func (c *Sequencer) publishBatch() error {
	batch := c.tq.GetNextBatch(c.maxSize, c.db)
	if batch.Transactions == nil {
		return nil
	}
	err := c.submitBatchToDA(batch)
	if err != nil {
		// On failure, re-add the batch to the transaction queue for future retry
		revertErr := c.tq.AddBatchBackToQueue(batch, c.db)
		if revertErr != nil {
			return fmt.Errorf("failed to revert batch to queue: %w", revertErr)
		}
		return fmt.Errorf("failed to submit batch to DA: %w", err)
	}
	err = c.bq.AddBatch(batch, c.db)
	if err != nil {
		return err
	}
	return nil
}

func (c *Sequencer) recordMetrics(gasPrice float64, blobSize uint64, statusCode da.StatusCode, numPendingBlocks int, includedBlockHeight uint64) {
	if c.metrics != nil {
		c.metrics.GasPrice.Set(float64(gasPrice))
		c.metrics.LastBlobSize.Set(float64(blobSize))
		c.metrics.TransactionStatus.With("status", fmt.Sprintf("%d", statusCode)).Add(1)
		c.metrics.NumPendingBlocks.Set(float64(numPendingBlocks))
		c.metrics.IncludedBlockHeight.Set(float64(includedBlockHeight))
	}
}

func (c *Sequencer) submitBatchToDA(batch sequencing.Batch) error {
	batchesToSubmit := []*sequencing.Batch{&batch}
	submittedAllBlocks := false
	var backoff time.Duration
	numSubmittedBatches := 0
	attempt := 0

	maxBlobSize := c.maxSize
	initialMaxBlobSize := maxBlobSize
	initialGasPrice := c.dalc.GasPrice
	gasPrice := c.dalc.GasPrice

daSubmitRetryLoop:
	for !submittedAllBlocks && attempt < maxSubmitAttempts {
		select {
		case <-c.ctx.Done():
			break daSubmitRetryLoop
		case <-time.After(backoff):
		}

		res := c.dalc.SubmitBatch(c.ctx, batchesToSubmit, maxBlobSize, gasPrice)
		switch res.Code {
		case da.StatusSuccess:
			txCount := 0
			for _, batch := range batchesToSubmit {
				txCount += len(batch.Transactions)
			}
			log.Info("successfully submitted batches to DA layer", "gasPrice", gasPrice, "daHeight", res.DAHeight, "batchCount", res.SubmittedCount, "txCount", txCount)
			if res.SubmittedCount == uint64(len(batchesToSubmit)) {
				submittedAllBlocks = true
			}
			submittedBatches, notSubmittedBatches := batchesToSubmit[:res.SubmittedCount], batchesToSubmit[res.SubmittedCount:]
			numSubmittedBatches += len(submittedBatches)
			batchesToSubmit = notSubmittedBatches
			// reset submission options when successful
			// scale back gasPrice gradually
			backoff = 0
			maxBlobSize = initialMaxBlobSize
			if c.dalc.GasMultiplier > 0 && gasPrice != -1 {
				gasPrice = gasPrice / c.dalc.GasMultiplier
				if gasPrice < initialGasPrice {
					gasPrice = initialGasPrice
				}
			}
			log.Debug("resetting DA layer submission options", "backoff", backoff, "gasPrice", gasPrice, "maxBlobSize", maxBlobSize)
		case da.StatusNotIncludedInBlock, da.StatusAlreadyInMempool:
			log.Error("DA layer submission failed", "error", res.Message, "attempt", attempt)
			backoff = c.batchTime * time.Duration(defaultMempoolTTL)
			if c.dalc.GasMultiplier > 0 && gasPrice != -1 {
				gasPrice = gasPrice * c.dalc.GasMultiplier
			}
			log.Info("retrying DA layer submission with", "backoff", backoff, "gasPrice", gasPrice, "maxBlobSize", maxBlobSize)

		case da.StatusTooBig:
			maxBlobSize = maxBlobSize / 4
			fallthrough
		default:
			log.Error("DA layer submission failed", "error", res.Message, "attempt", attempt)
			backoff = c.exponentialBackoff(backoff)
		}

		c.recordMetrics(gasPrice, res.BlobSize, res.Code, len(batchesToSubmit), res.DAHeight)
		attempt += 1
	}

	if !submittedAllBlocks {
		return fmt.Errorf(
			"failed to submit all blocks to DA layer, submitted %d blocks (%d left) after %d attempts",
			numSubmittedBatches,
			len(batchesToSubmit),
			attempt,
		)
	}
	return nil
}

func (c *Sequencer) exponentialBackoff(backoff time.Duration) time.Duration {
	backoff *= 2
	if backoff == 0 {
		backoff = initialBackoff
	}
	if backoff > c.batchTime {
		backoff = c.batchTime
	}
	return backoff
}

func getRemainingSleep(start time.Time, blockTime time.Duration, sleep time.Duration) time.Duration {
	elapsed := time.Since(start)
	remaining := blockTime - elapsed
	if remaining < 0 {
		return 0
	}
	return remaining + sleep
}

// SubmitRollupTransaction implements sequencing.Sequencer.
func (c *Sequencer) SubmitRollupTransaction(ctx context.Context, req sequencing.SubmitRollupTransactionRequest) (*sequencing.SubmitRollupTransactionResponse, error) {
	if !c.isValid(req.RollupId) {
		return nil, ErrInvalidRollupId
	}
	err := c.tq.AddTransaction(req.Tx, c.db)
	if err != nil {
		return nil, fmt.Errorf("failed to add transaction: %w", err)
	}
	return &sequencing.SubmitRollupTransactionResponse{}, nil
}

// GetNextBatch implements sequencing.Sequencer.
func (c *Sequencer) GetNextBatch(ctx context.Context, req sequencing.GetNextBatchRequest) (*sequencing.GetNextBatchResponse, error) {
	if !c.isValid(req.RollupId) {
		return nil, ErrInvalidRollupId
	}
	now := time.Now()
	c.lastBatchHashMutex.Lock()
	defer c.lastBatchHashMutex.Unlock()
	lastBatchHash := c.lastBatchHash

	if !reflect.DeepEqual(lastBatchHash, req.LastBatchHash) {
		return nil, fmt.Errorf("batch hash mismatch: lastBatchHash = %x, req.LastBatchHash = %x", lastBatchHash, req.LastBatchHash)
	}

	// Set the max size if it is provided
	if req.MaxBytes > 0 {
		c.CompareAndSetMaxSize(req.MaxBytes)
	}

	batch, err := c.bq.Next(c.db)
	if err != nil {
		return nil, err
	}

	batchRes := &sequencing.GetNextBatchResponse{Batch: batch, Timestamp: now}
	if batch.Transactions == nil {
		return batchRes, nil
	}

	h, err := batch.Hash()
	if err != nil {
		return c.recover(*batch, err)
	}

	c.lastBatchHash = h
	err = c.setLastBatchHash(h)
	if err != nil {
		return c.recover(*batch, err)
	}

	hexHash := hex.EncodeToString(h)
	c.seenBatchesMutex.Lock()
	c.seenBatches[hexHash] = struct{}{}
	c.seenBatchesMutex.Unlock()
	err = c.addSeenBatch(h)
	if err != nil {
		return c.recover(*batch, err)
	}

	return batchRes, nil
}

func (c *Sequencer) recover(batch sequencing.Batch, err error) (*sequencing.GetNextBatchResponse, error) {
	// Revert the batch if Hash() errors out by adding it back to the BatchQueue
	revertErr := c.bq.AddBatch(batch, c.db)
	if revertErr != nil {
		return nil, fmt.Errorf("failed to revert batch: %w", revertErr)
	}
	return nil, fmt.Errorf("failed to generate hash for batch: %w", err)
}

// VerifyBatch implements sequencing.Sequencer.
func (c *Sequencer) VerifyBatch(ctx context.Context, req sequencing.VerifyBatchRequest) (*sequencing.VerifyBatchResponse, error) {
	//TODO: need to add DA verification
	if !c.isValid(req.RollupId) {
		return nil, ErrInvalidRollupId
	}
	c.seenBatchesMutex.Lock()
	defer c.seenBatchesMutex.Unlock()
	key := hex.EncodeToString(req.BatchHash)
	if _, exists := c.seenBatches[key]; exists {
		return &sequencing.VerifyBatchResponse{Status: true}, nil
	}
	return &sequencing.VerifyBatchResponse{Status: false}, nil
}

func (c *Sequencer) isValid(rollupId []byte) bool {
	return bytes.Equal(c.rollupId, rollupId)
}
