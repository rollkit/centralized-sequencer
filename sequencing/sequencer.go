package sequencing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	logging "github.com/ipfs/go-log/v2"

	"github.com/rollkit/centralized-sequencer/da"
	goda "github.com/rollkit/go-da"
	proxyda "github.com/rollkit/go-da/proxy"
	"github.com/rollkit/go-sequencing"
)

var _ sequencing.Sequencer = &Sequencer{}
var log = logging.Logger("centralized-sequencer")

const maxSubmitAttempts = 30
const defaultMempoolTTL = 25

var initialBackoff = 100 * time.Millisecond

// ErrorRollupIdMismatch is returned when the rollup id does not match
var ErrorRollupIdMismatch = errors.New("rollup id mismatch")

// BatchQueue ...
type BatchQueue struct {
	queue []sequencing.Batch
	mu    sync.Mutex
}

// NewBatchQueue creates a new TransactionQueue
func NewBatchQueue() *BatchQueue {
	return &BatchQueue{
		queue: make([]sequencing.Batch, 0),
	}
}

// AddBatch adds a new transaction to the queue
func (bq *BatchQueue) AddBatch(batch sequencing.Batch) {
	bq.mu.Lock()
	defer bq.mu.Unlock()
	bq.queue = append(bq.queue, batch)
}

// Next ...
func (bq *BatchQueue) Next() *sequencing.Batch {
	if len(bq.queue) == 0 {
		return &sequencing.Batch{Transactions: nil}
	}
	batch := bq.queue[0]
	bq.queue = bq.queue[1:]
	return &batch
}

// TransactionQueue is a queue of transactions
type TransactionQueue struct {
	queue []sequencing.Tx
	mu    sync.Mutex
}

// NewTransactionQueue creates a new TransactionQueue
func NewTransactionQueue() *TransactionQueue {
	return &TransactionQueue{
		queue: make([]sequencing.Tx, 0),
	}
}

// AddTransaction adds a new transaction to the queue
func (tq *TransactionQueue) AddTransaction(tx sequencing.Tx) {
	tq.mu.Lock()
	defer tq.mu.Unlock()
	tq.queue = append(tq.queue, tx)
}

// GetNextBatch extracts a batch of transactions from the queue
func (tq *TransactionQueue) GetNextBatch(max uint64) sequencing.Batch {
	tq.mu.Lock()
	defer tq.mu.Unlock()

	var batch [][]byte
	batchSize := len(tq.queue)
	for {
		batch = tq.queue[:batchSize]
		blobSize := totalBytes(batch)
		if uint64(blobSize) < max {
			break
		}
		batchSize = batchSize - 1
	}

	tq.queue = tq.queue[batchSize:]
	return sequencing.Batch{Transactions: batch}
}

func totalBytes(data [][]byte) int {
	total := 0
	for _, sub := range data {
		total += len(sub)
	}
	return total
}

// Sequencer implements go-sequencing interface using celestia backend
type Sequencer struct {
	dalc          *da.DAClient
	batchTime     time.Duration
	ctx           context.Context
	maxDABlobSize uint64

	rollupId sequencing.RollupId

	tq            *TransactionQueue
	lastBatchHash []byte

	seenBatches map[string]struct{}
	bq          *BatchQueue
}

// NewSequencer ...
func NewSequencer(daAddress, daAuthToken, daNamespace string, batchTime time.Duration) (*Sequencer, error) {
	ctx := context.Background()
	dac, err := proxyda.NewClient(daAddress, daAuthToken)
	if err != nil {
		return nil, fmt.Errorf("error while establishing connection to DA layer: %w", err)
	}
	dalc := da.NewDAClient(dac, -1, 0, goda.Namespace(daNamespace))
	maxBlobSize, err := dalc.DA.MaxBlobSize(ctx)
	if err != nil {
		return nil, err
	}
	s := &Sequencer{
		dalc:          dalc,
		batchTime:     batchTime,
		ctx:           ctx,
		maxDABlobSize: maxBlobSize,
		tq:            NewTransactionQueue(),
		bq:            NewBatchQueue(),
		seenBatches:   make(map[string]struct{}),
	}
	go s.batchSubmissionLoop(s.ctx)
	return s, nil
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
	batch := c.tq.GetNextBatch(c.maxDABlobSize)
	err := c.submitBatchToDA(batch)
	if err != nil {
		return err
	}
	c.bq.AddBatch(batch)
	return nil
}

func (c *Sequencer) submitBatchToDA(batch sequencing.Batch) error {
	batchesToSubmit := []*sequencing.Batch{&batch}
	submittedAllBlocks := false
	var backoff time.Duration
	numSubmittedBatches := 0
	attempt := 0

	maxBlobSize := c.maxDABlobSize
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

func hashSHA256(data []byte) []byte {
	hash := sha256.Sum256(data)
	return hash[:]
}

// SubmitRollupTransaction implements sequencing.Sequencer.
func (c *Sequencer) SubmitRollupTransaction(ctx context.Context, rollupId []byte, tx []byte) error {
	if c.rollupId == nil {
		c.rollupId = rollupId
	} else {
		if !bytes.Equal(c.rollupId, rollupId) {
			return ErrorRollupIdMismatch
		}
	}
	c.tq.AddTransaction(tx)
	return nil
}

// GetNextBatch implements sequencing.Sequencer.
func (c *Sequencer) GetNextBatch(ctx context.Context, lastBatch *sequencing.Batch) (*sequencing.Batch, error) {
	if c.lastBatchHash == nil {
		if lastBatch.Transactions != nil {
			return nil, errors.New("lastBatch is supposed to be nil")
		}
	} else if lastBatch.Transactions == nil {
		return nil, errors.New("lastBatch is not supposed to be nil")
	} else {
		lastBatchBytes, err := lastBatch.Marshal()
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(c.lastBatchHash, hashSHA256(lastBatchBytes)) {
			return nil, errors.New("supplied lastBatch does not match with sequencer last batch")
		}
	}

	batch := c.bq.Next()
	if batch.Transactions == nil {
		return batch, nil
	}

	batchBytes, err := batch.Marshal()
	if err != nil {
		return nil, err
	}

	c.lastBatchHash = hashSHA256(batchBytes)
	c.seenBatches[string(c.lastBatchHash)] = struct{}{}
	return batch, nil
}

// VerifyBatch implements sequencing.Sequencer.
func (c *Sequencer) VerifyBatch(ctx context.Context, batch *sequencing.Batch) (bool, error) {
	//TODO: need to add DA verification
	batchBytes, err := batch.Marshal()
	if err != nil {
		return false, err
	}
	hash := hashSHA256(batchBytes)
	_, ok := c.seenBatches[string(hash)]
	return ok, nil
}
