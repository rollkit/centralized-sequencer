package sequencing

import (
	"context"
	"net/url"
	"os"
	"testing"
	"time"

	proxy "github.com/rollkit/go-da/proxy/jsonrpc"
	goDATest "github.com/rollkit/go-da/test"
	"github.com/rollkit/go-sequencing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	seq, err := NewSequencer(MockDAAddressHTTP, "authToken", []byte("namespace"), 10*time.Second)
	require.NoError(t, err)

	// Test with initial rollup ID
	rollupId := []byte("rollup1")
	tx := []byte("transaction1")

	err = seq.SubmitRollupTransaction(context.Background(), rollupId, tx)
	require.NoError(t, err)

	// Verify the transaction was added
	assert.Equal(t, 1, len(seq.tq.GetNextBatch(1000).Transactions))

	// Test with a different rollup ID (expecting an error due to mismatch)
	err = seq.SubmitRollupTransaction(context.Background(), []byte("rollup2"), tx)
	assert.EqualError(t, err, ErrorRollupIdMismatch.Error())
}

func TestSequencer_GetNextBatch_NoLastBatch(t *testing.T) {
	// Initialize a new sequencer
	seq := &Sequencer{
		bq:          NewBatchQueue(),
		seenBatches: make(map[string]struct{}),
	}

	// Test case where lastBatchHash and seq.lastBatchHash are both nil
	batch, now, err := seq.GetNextBatch(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, time.Now().Day(), now.Day()) // Ensure the time is approximately the same
	assert.Equal(t, 0, len(batch.Transactions))  // Should return an empty batch
}

func TestSequencer_GetNextBatch_LastBatchMismatch(t *testing.T) {
	// Initialize a new sequencer with a mock batch
	seq := &Sequencer{
		lastBatchHash: []byte("existingHash"),
		bq:            NewBatchQueue(),
		seenBatches:   make(map[string]struct{}),
	}

	// Test case where lastBatchHash does not match seq.lastBatchHash
	_, _, err := seq.GetNextBatch(context.Background(), []byte("differentHash"))
	assert.EqualError(t, err, "supplied lastBatch does not match with sequencer last batch")
}

func TestSequencer_GetNextBatch_LastBatchNilMismatch(t *testing.T) {
	// Initialize a new sequencer
	seq := &Sequencer{
		lastBatchHash: []byte("existingHash"),
		bq:            NewBatchQueue(),
		seenBatches:   make(map[string]struct{}),
	}

	// Test case where lastBatchHash is nil but seq.lastBatchHash is not
	_, _, err := seq.GetNextBatch(context.Background(), nil)
	assert.EqualError(t, err, "lastBatch is not supposed to be nil")
}

func TestSequencer_GetNextBatch_Success(t *testing.T) {
	// Initialize a new sequencer with a mock batch
	mockBatch := &sequencing.Batch{Transactions: [][]byte{[]byte("tx1"), []byte("tx2")}}

	seq := &Sequencer{
		bq:            NewBatchQueue(),
		seenBatches:   make(map[string]struct{}),
		lastBatchHash: nil,
	}

	// Add mock batch to the BatchQueue
	seq.bq.AddBatch(*mockBatch)

	// Test success case with no previous lastBatchHash
	batch, now, err := seq.GetNextBatch(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, time.Now().Day(), now.Day()) // Ensure the time is approximately the same
	assert.Equal(t, 2, len(batch.Transactions))  // Ensure that the transactions are present

	// Ensure lastBatchHash is updated after the batch
	assert.NotNil(t, seq.lastBatchHash)
	assert.NotEmpty(t, seq.seenBatches) // Ensure the batch hash was added to seenBatches
}

func TestSequencer_VerifyBatch(t *testing.T) {
	// Initialize a new sequencer with a seen batch
	seq := &Sequencer{
		seenBatches: make(map[string]struct{}),
	}

	// Simulate adding a batch hash
	batchHash := []byte("validHash")
	seq.seenBatches[string(batchHash)] = struct{}{}

	// Test that VerifyBatch returns true for an existing batch
	exists, err := seq.VerifyBatch(context.Background(), batchHash)
	require.NoError(t, err)
	assert.True(t, exists)

	// Test that VerifyBatch returns false for a non-existing batch
	exists, err = seq.VerifyBatch(context.Background(), []byte("invalidHash"))
	require.NoError(t, err)
	assert.False(t, exists)
}
