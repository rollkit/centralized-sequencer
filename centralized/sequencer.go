package centralized

import (
	"context"
	"fmt"

	proxyda "github.com/rollkit/go-da/proxy"
	"github.com/rollkit/go-sequencing"
)

var _ sequencing.Sequencer = &Sequencer{}

// Sequencer implements go-sequencing interface using celestia backend
type Sequencer struct {
}

func NewSequencer(daAddress, daAuthToken, daNamespace string) (*Sequencer, error) {
	client, err := proxyda.NewClient(daAddress, daAuthToken)
	if err != nil {
		return nil, fmt.Errorf("error while establishing connection to DA layer: %w", err)
	}
	return &Sequencer{}, nil
}

// SubmitRollupTransaction implements sequencing.Sequencer.
func (c *Sequencer) SubmitRollupTransaction(ctx context.Context, rollupId []byte, tx []byte) error {
	panic("unimplemented")
}

// GetNextBatch implements sequencing.Sequencer.
func (c *Sequencer) GetNextBatch(ctx context.Context, lastBatch sequencing.Batch) (sequencing.Batch, error) {
	panic("unimplemented")
}

// VerifyBatch implements sequencing.Sequencer.
func (c *Sequencer) VerifyBatch(ctx context.Context, batch sequencing.Batch) (bool, error) {
	panic("unimplemented")
}
