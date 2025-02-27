package sequencing

import (
	"sync"

	"github.com/dgraph-io/badger/v4"
	"github.com/rollkit/go-sequencing"
)

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

// AddBatch adds a new batch to the queue
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
		key := append(keyPrefixBatch, h...)
		return txn.Set(key, batchBytes)
	})
	return err
}

// AddBatchToTheTop adds a new batch to the queue, at index 0
func (bq *BatchQueue) AddBatchToTheTop(batch sequencing.Batch, db *badger.DB) error {
	bq.mu.Lock()
	bq.queue = append([]sequencing.Batch{batch}, bq.queue...)
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
		key := append(keyPrefixBatch, h...)
		return txn.Set(key, batchBytes)
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
		key := append(keyPrefixBatch, h...)
		_, err := txn.Get(key)
		if err != nil {
			return err
		}
		// Delete the batch from BadgerDB
		return txn.Delete(key)
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
		opts := badger.DefaultIteratorOptions
		opts.Prefix = keyPrefixBatch
		it := txn.NewIterator(opts)
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
