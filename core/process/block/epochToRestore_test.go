package block

import (
	"testing"

	"github.com/klever-io/klever-go/data"
	"github.com/klever-io/klever-go/data/block"
	"github.com/stretchr/testify/assert"
)

// TestEpochToRestore pins every branch of the epoch a rejected block returns to: the notifier's
// epoch unless the committed head is in a later one.
func TestEpochToRestore(t *testing.T) {
	t.Parallel()

	headIn := func(epoch uint32) data.HeaderHandler {
		return &block.Block{Header: &block.BlockHeader{Epoch: epoch}}
	}

	tests := []struct {
		name          string
		notifierEpoch uint32
		head          data.HeaderHandler
		expected      uint32
	}{
		{"nil head keeps the notifier epoch", 7, nil, 7},
		{"notifier ahead of the head keeps its epoch", 9, headIn(5), 9},
		{"notifier equal to the head", 5, headIn(5), 5},
		{"lagging notifier after a start takes the head epoch", 0, headIn(5), 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.expected, epochToRestore(tt.notifierEpoch, tt.head))
		})
	}
}
