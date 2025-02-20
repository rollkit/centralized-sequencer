package sequencing

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/dgraph-io/badger/v4"

	"github.com/rollkit/go-sequencing"
)

// BatchExtender is an interface for extending a batch of transactions
type BatchExtender interface {
	Head(max uint64) ([]byte, error)
	Tail(max uint64) ([]byte, error)
}

// TransactionQueue is a queue of transactions
type TransactionQueue struct {
	queue    []sequencing.Tx
	mu       sync.Mutex
	extender BatchExtender
}

// NewTransactionQueue creates a new TransactionQueue
func NewTransactionQueue(extender BatchExtender) *TransactionQueue {
	return &TransactionQueue{
		queue:    make([]sequencing.Tx, 0),
		extender: extender,
	}
}

// GetTransactionHash to get hash from transaction bytes using SHA-256
func GetTransactionHash(txBytes []byte) string {
	hashBytes := sha256.Sum256(txBytes)
	return hex.EncodeToString(hashBytes[:])
}

// AddTransaction adds a new transaction to the queue
func (tq *TransactionQueue) AddTransaction(tx sequencing.Tx, db *badger.DB) error {
	tq.mu.Lock()
	tq.queue = append(tq.queue, tx)
	tq.mu.Unlock()

	// Store transaction in BadgerDB
	err := db.Update(func(txn *badger.Txn) error {
		key := append(keyPrefixTx, []byte(GetTransactionHash(tx))...)
		return txn.Set(key, tx)
	})
	return err
}

// GetNextBatch extracts a batch of transactions from the queue
func (tq *TransactionQueue) GetNextBatch(max uint64, db *badger.DB) sequencing.Batch {
	tq.mu.Lock()
	defer tq.mu.Unlock()

	// Add head of batch if extender is provided, also ask for the tail of the batch
	var head, tail []byte
	if tq.extender != nil {
		var err error
		head, err = tq.extender.Head(max)
		if err != nil {
			return sequencing.Batch{Transactions: nil}
		}
		tail, err = tq.extender.Tail(max)
		if err != nil {
			return sequencing.Batch{Transactions: nil}
		}
	}

	batch := tq.queue
	headTailSize := len(head) + len(tail)

	for uint64(totalBytes(batch)+headTailSize) > max {
		batch = batch[:len(batch)-1]
	}

	// batchLen before adding head and tail, to remove the correct number of transactions from the queue
	batchLen := len(batch)

	// Add head and tail of the batch
	if head != nil {
		batch = append([][]byte{head}, batch...)
	}

	if tail != nil {
		batch = append(batch, tail)
	}

	if len(batch) == 0 {
		return sequencing.Batch{Transactions: nil}
	}

	// Retrieve transactions from BadgerDB and remove processed ones
	for i, tx := range batch {
		txHash := GetTransactionHash(tx)
		err := db.Update(func(txn *badger.Txn) error {
			// Get and then delete the transaction from BadgerDB
			key := append(keyPrefixTx, []byte(txHash)...)
			_, err := txn.Get(key)
			if err != nil {
				// If the transaction not found is the head or tail, skip it as they are not in the queue
				if errors.Is(err, badger.ErrKeyNotFound) && (i == 0 || i == len(batch)-1) {
					return nil
				}
				return err
			}
			return txn.Delete(key) // Remove processed transaction
		})
		if err != nil {
			return sequencing.Batch{Transactions: nil} // Return empty batch if any transaction retrieval fails
		}
	}
	tq.queue = tq.queue[batchLen:]
	return sequencing.Batch{Transactions: batch}
}

// LoadFromDB reloads all transactions from BadgerDB into the in-memory queue after a crash.
func (tq *TransactionQueue) LoadFromDB(db *badger.DB) error {
	tq.mu.Lock()
	defer tq.mu.Unlock()

	// Start a read-only transaction
	err := db.View(func(txn *badger.Txn) error {
		// Create an iterator to go through all transactions stored in BadgerDB
		opts := badger.DefaultIteratorOptions
		opts.Prefix = keyPrefixTx
		it := txn.NewIterator(opts)
		defer it.Close() // Ensure that the iterator is properly closed

		// Iterate through all items in the database
		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				// Load each transaction from DB and add to the in-memory queue
				tq.queue = append(tq.queue, val)
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

// AddBatchBackToQueue re-adds the batch to the transaction queue (and BadgerDB) after a failure.
func (tq *TransactionQueue) AddBatchBackToQueue(batch sequencing.Batch, db *badger.DB) error {
	tq.mu.Lock()
	defer tq.mu.Unlock()

	// Add the batch back to the in-memory transaction queue
	tq.queue = append(tq.queue, batch.Transactions...)

	// Optionally, persist the batch back to BadgerDB
	for _, tx := range batch.Transactions {
		err := db.Update(func(txn *badger.Txn) error {
			key := append(keyPrefixTx, []byte(GetTransactionHash(tx))...)
			return txn.Set(key, tx) // Store transaction back in DB
		})
		if err != nil {
			return fmt.Errorf("failed to revert transaction to DB: %w", err)
		}
	}

	return nil
}

func totalBytes(data [][]byte) int {
	total := 0
	for _, sub := range data {
		total += len(sub)
	}
	return total
}
