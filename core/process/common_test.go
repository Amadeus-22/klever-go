package process_test

import (
	"errors"
	"testing"

	"github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/core/process"
	"github.com/klever-io/klever-go/data/block"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var commonTestHasher = mock.HasherMock{}

func createBlockWithTxs(epoch uint32, txHashes ...[]byte) *block.Block {
	blk := &block.Block{
		Header: &block.BlockHeader{
			Epoch:   epoch,
			TxCount: uint32(len(txHashes)),
		},
		TxHashes:  txHashes,
		TxResults: make([]uint32, len(txHashes)),
	}
	if len(txHashes) > 0 {
		_ = blk.UpdateTxRootHash(commonTestHasher)
	}

	return blk
}

func TestCheckTxHashesLength(t *testing.T) {
	t.Parallel()

	size := commonTestHasher.Size()

	for _, tc := range []struct {
		name     string
		txHashes [][]byte
		wantErr  error
	}{
		{"no hashes", nil, nil},
		{"all hasher sized", [][]byte{make([]byte, size), make([]byte, size)}, nil},
		{"nil hash", [][]byte{make([]byte, size), nil}, process.ErrInvalidTxHashLength},
		{"shorter hash", [][]byte{make([]byte, size-1)}, process.ErrInvalidTxHashLength},
		{"longer hash", [][]byte{make([]byte, size), make([]byte, size+1)}, process.ErrInvalidTxHashLength},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.wantErr, process.CheckTxHashesLength(tc.txHashes, size))
		})
	}
}

func TestWrapBlockBodyNotMatchingHeader(t *testing.T) {
	t.Parallel()

	err := process.WrapBlockBodyNotMatchingHeader(process.ErrInvalidTXCount)

	assert.ErrorIs(t, err, process.ErrBlockBodyNotMatchingHeader)
	assert.ErrorIs(t, err, process.ErrInvalidTXCount)
	assert.False(t, errors.Is(err, process.ErrBlockExecutionNotMatchingHeader))
	assert.Equal(t, "block body does not match header: invalid block tx count", err.Error())
}

func TestWrapBlockExecutionNotMatchingHeader(t *testing.T) {
	t.Parallel()

	err := process.WrapBlockExecutionNotMatchingHeader(process.ErrInvalidKAppsFees)

	assert.ErrorIs(t, err, process.ErrBlockExecutionNotMatchingHeader)
	assert.ErrorIs(t, err, process.ErrInvalidKAppsFees)
	assert.False(t, errors.Is(err, process.ErrBlockBodyNotMatchingHeader))
	assert.Equal(t, "block execution does not match header: invalid block tx kapps fees", err.Error())
}

func TestCheckBlockBodyMatchesHeader(t *testing.T) {
	t.Parallel()

	const headerEpoch = uint32(7)
	activeAtHeaderEpoch := headerEpoch
	activeAfterHeaderEpoch := headerEpoch + 1

	first := commonTestHasher.Compute("first")
	second := commonTestHasher.Compute("second")

	for _, tc := range []struct {
		name            string
		block           func() *block.Block
		activationEpoch uint32
		wantErr         error
		headerLevel     bool
	}{
		{
			name:            "block with transactions matching its header",
			block:           func() *block.Block { return createBlockWithTxs(headerEpoch, first, second) },
			activationEpoch: activeAtHeaderEpoch,
		},
		{
			name:            "empty block with a nil root",
			block:           func() *block.Block { return createBlockWithTxs(headerEpoch) },
			activationEpoch: activeAtHeaderEpoch,
		},
		{
			name: "empty block with the empty hash as root",
			block: func() *block.Block {
				blk := createBlockWithTxs(headerEpoch)
				blk.Header.TxRootHash = commonTestHasher.EmptyHash()
				return blk
			},
			activationEpoch: activeAtHeaderEpoch,
		},
		{
			name: "empty block with an arbitrary root",
			block: func() *block.Block {
				blk := createBlockWithTxs(headerEpoch)
				blk.Header.TxRootHash = commonTestHasher.Compute("another root")
				return blk
			},
			activationEpoch: activeAtHeaderEpoch,
			wantErr:         process.ErrTxRootHashInvalidForEmptyBlock,
			headerLevel:     true,
		},
		{
			name: "tx count not matching the tx hashes",
			block: func() *block.Block {
				blk := createBlockWithTxs(headerEpoch, first, second)
				blk.Header.TxCount = 3
				return blk
			},
			activationEpoch: activeAtHeaderEpoch,
			wantErr:         process.ErrInvalidTXCount,
		},
		{
			name: "missing tx results once the fork is active",
			block: func() *block.Block {
				blk := createBlockWithTxs(headerEpoch, first, second)
				blk.TxResults = nil
				return blk
			},
			activationEpoch: activeAtHeaderEpoch,
			wantErr:         process.ErrInvalidTXResultsCount,
		},
		{
			name: "tx results on a block without transactions once the fork is active",
			block: func() *block.Block {
				blk := createBlockWithTxs(headerEpoch)
				blk.TxResults = []uint32{0}
				return blk
			},
			activationEpoch: activeAtHeaderEpoch,
			wantErr:         process.ErrInvalidTXResultsCount,
		},
		{
			name: "missing tx results before the fork",
			block: func() *block.Block {
				blk := createBlockWithTxs(headerEpoch, first, second)
				blk.TxResults = nil
				return blk
			},
			activationEpoch: activeAfterHeaderEpoch,
		},
		{
			name: "tx hashes split at the wrong width with a matching root",
			block: func() *block.Block {
				blk := createBlockWithTxs(headerEpoch, first, second)
				joined := append(append([]byte{}, first...), second...)
				blk.TxHashes = [][]byte{joined[:len(first)-1], joined[len(first)-1:]}
				return blk
			},
			activationEpoch: activeAtHeaderEpoch,
			wantErr:         process.ErrInvalidTxHashLength,
		},
		{
			name: "tx root not matching the tx hashes",
			block: func() *block.Block {
				blk := createBlockWithTxs(headerEpoch, first, second)
				blk.TxHashes[0], blk.TxHashes[1] = blk.TxHashes[1], blk.TxHashes[0]
				return blk
			},
			activationEpoch: activeAtHeaderEpoch,
			wantErr:         process.ErrTxRootHashDoesNotMatch,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			activationEpoch := tc.activationEpoch
			forkController := &mock.ForkControllerStub{FixAuditChangesV5Epoch: &activationEpoch}

			err := process.CheckBlockBodyMatchesHeader(tc.block(), commonTestHasher, forkController)
			if tc.wantErr == nil {
				require.NoError(t, err)
				return
			}

			assert.ErrorIs(t, err, tc.wantErr)
			assert.Equal(t, !tc.headerLevel, errors.Is(err, process.ErrBlockBodyNotMatchingHeader))
		})
	}
}

func TestCheckBlockBodyMatchesHeader_NilHeaderShouldErr(t *testing.T) {
	t.Parallel()

	err := process.CheckBlockBodyMatchesHeader(nil, commonTestHasher, &mock.ForkControllerStub{})

	assert.Equal(t, process.ErrNilBlockHeader, err)
}
