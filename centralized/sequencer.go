package centralized

import (
	"context"

	"github.com/rollkit/go-sequencing"
)

var _ sequencing.Sequencer = &Sequencer{}

// Sequencer implements go-sequencing interface using celestia backend
type Sequencer struct {
}

func NewSequencer() *Sequencer {
	return &Sequencer{}
}

// GetNextBatch implements sequencing.Sequencer.
func (c *Sequencer) GetNextBatch(ctx context.Context, lastBatch [][]byte) ([][]byte, error) {
	panic("unimplemented")
}

// SubmitRollupTransaction implements sequencing.Sequencer.
func (c *Sequencer) SubmitRollupTransaction(ctx context.Context, rollupId []byte, tx []byte) error {
	panic("unimplemented")
}

// VerifyBatch implements sequencing.Sequencer.
func (c *Sequencer) VerifyBatch(ctx context.Context, batch [][]byte) (bool, error) {
	panic("unimplemented")
}
