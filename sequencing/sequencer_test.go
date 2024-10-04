package sequencing

import (
	"context"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	proxy "github.com/rollkit/go-da/proxy/jsonrpc"
	goDATest "github.com/rollkit/go-da/test"
	"github.com/rollkit/go-sequencing"
)

const (
	// MockDAAddressHTTP is mock address for the JSONRPC server
	MockDAAddressHTTP = "http://localhost:7988"
)

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	jsonrpcSrv, _ := startMockDAServJSONRPC(ctx, MockDAAddressHTTP)
	if jsonrpcSrv == nil {
		os.Exit(1)
	}
	exitCode := m.Run()

	// teardown servers
	// nolint:errcheck,gosec
	jsonrpcSrv.Stop(context.Background())

	os.Exit(exitCode)
}

func startMockDAServJSONRPC(ctx context.Context, da_address string) (*proxy.Server, error) {
	addr, _ := url.Parse(da_address)
	srv := proxy.NewServer(addr.Hostname(), addr.Port(), goDATest.NewDummyDA())
	err := srv.Start(ctx)
	if err != nil {
		return nil, err
	}
	return srv, nil
}

func TestNewSequencer(t *testing.T) {
	// Mock DA client
	// mockDAClient := new(da.DAClient)

	// Create a new sequencer with mock DA client
	seq, err := NewSequencer(MockDAAddressHTTP, "authToken", []byte("namespace"), 10*time.Second)
	require.NoError(t, err)

	// Check if the sequencer was created with the correct values
	assert.NotNil(t, seq)
	assert.NotNil(t, seq.tq)
	assert.NotNil(t, seq.bq)
	assert.NotNil(t, seq.dalc)
}

func TestSequencer_SubmitRollupTransaction(t *testing.T) {
	// Initialize a new sequencer
	seq, err := NewSequencer(MockDAAddressHTTP, "authToken", []byte("rollup1"), 10*time.Second)
	require.NoError(t, err)

	// Test with initial rollup ID
	rollupId := []byte("rollup1")
	tx := []byte("transaction1")

	res, err := seq.SubmitRollupTransaction(context.Background(), sequencing.SubmitRollupTransactionRequest{RollupId: rollupId, Tx: tx})
	require.NoError(t, err)
	require.NotNil(t, res)

	// Verify the transaction was added
	assert.Equal(t, 1, len(seq.tq.GetNextBatch(1000).Transactions))

	// Test with a different rollup ID (expecting an error due to mismatch)
	res, err = seq.SubmitRollupTransaction(context.Background(), sequencing.SubmitRollupTransactionRequest{RollupId: []byte("rollup2"), Tx: tx})
	assert.EqualError(t, err, ErrInvalidRollupId.Error())
	assert.Nil(t, res)
}

func TestSequencer_GetNextBatch_NoLastBatch(t *testing.T) {
	// Initialize a new sequencer
	seq := &Sequencer{
		bq:          NewBatchQueue(),
		seenBatches: make(map[string]struct{}),
		rollupId:    []byte("rollup"),
	}

	// Test case where lastBatchHash and seq.lastBatchHash are both nil
	res, err := seq.GetNextBatch(context.Background(), sequencing.GetNextBatchRequest{RollupId: seq.rollupId, LastBatchHash: nil})
	require.NoError(t, err)
	assert.Equal(t, time.Now().Day(), res.Timestamp.Day()) // Ensure the time is approximately the same
	assert.Equal(t, 0, len(res.Batch.Transactions))        // Should return an empty batch
}

func TestSequencer_GetNextBatch_LastBatchMismatch(t *testing.T) {
	// Initialize a new sequencer with a mock batch
	seq := &Sequencer{
		lastBatchHash: []byte("existingHash"),
		bq:            NewBatchQueue(),
		seenBatches:   make(map[string]struct{}),
		rollupId:      []byte("rollup"),
	}

	// Test case where lastBatchHash does not match seq.lastBatchHash
	res, err := seq.GetNextBatch(context.Background(), sequencing.GetNextBatchRequest{RollupId: seq.rollupId, LastBatchHash: []byte("differentHash")})
	assert.EqualError(t, err, "supplied lastBatch does not match with sequencer last batch")
	assert.Nil(t, res)
}

func TestSequencer_GetNextBatch_LastBatchNilMismatch(t *testing.T) {
	// Initialize a new sequencer
	seq := &Sequencer{
		lastBatchHash: []byte("existingHash"),
		bq:            NewBatchQueue(),
		seenBatches:   make(map[string]struct{}),
		rollupId:      []byte("rollup"),
	}

	// Test case where lastBatchHash is nil but seq.lastBatchHash is not
	res, err := seq.GetNextBatch(context.Background(), sequencing.GetNextBatchRequest{RollupId: seq.rollupId, LastBatchHash: nil})
	assert.EqualError(t, err, "lastBatch is not supposed to be nil")
	assert.Nil(t, res)
}

func TestSequencer_GetNextBatch_Success(t *testing.T) {
	// Initialize a new sequencer with a mock batch
	mockBatch := &sequencing.Batch{Transactions: [][]byte{[]byte("tx1"), []byte("tx2")}}

	seq := &Sequencer{
		bq:            NewBatchQueue(),
		seenBatches:   make(map[string]struct{}),
		lastBatchHash: nil,
		rollupId:      []byte("rollup"),
	}

	// Add mock batch to the BatchQueue
	seq.bq.AddBatch(*mockBatch)

	// Test success case with no previous lastBatchHash
	res, err := seq.GetNextBatch(context.Background(), sequencing.GetNextBatchRequest{RollupId: seq.rollupId, LastBatchHash: nil})
	require.NoError(t, err)
	assert.Equal(t, time.Now().Day(), res.Timestamp.Day()) // Ensure the time is approximately the same
	assert.Equal(t, 2, len(res.Batch.Transactions))        // Ensure that the transactions are present

	// Ensure lastBatchHash is updated after the batch
	assert.NotNil(t, seq.lastBatchHash)
	assert.NotEmpty(t, seq.seenBatches) // Ensure the batch hash was added to seenBatches
}

func TestSequencer_VerifyBatch(t *testing.T) {
	// Initialize a new sequencer with a seen batch
	seq := &Sequencer{
		seenBatches: make(map[string]struct{}),
		rollupId:    []byte("rollup"),
	}

	// Simulate adding a batch hash
	batchHash := []byte("validHash")
	seq.seenBatches[hex.EncodeToString(batchHash)] = struct{}{}

	// Test that VerifyBatch returns true for an existing batch
	res, err := seq.VerifyBatch(context.Background(), sequencing.VerifyBatchRequest{RollupId: seq.rollupId, BatchHash: batchHash})
	require.NoError(t, err)
	assert.True(t, res.Status)

	// Test that VerifyBatch returns false for a non-existing batch
	res, err = seq.VerifyBatch(context.Background(), sequencing.VerifyBatchRequest{RollupId: seq.rollupId, BatchHash: []byte("invalidHash")})
	require.NoError(t, err)
	assert.False(t, res.Status)
}
