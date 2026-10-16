package parsers

import (
	"bytes"
	"math"
	"math/big"
	"testing"

	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/tools/marshal"
	"github.com/stretchr/testify/require"
)

var testRcvAddr = bytes.Repeat([]byte("r"), 32)

func newTestKDATransferParser(t *testing.T) *kdaTransferParser {
	parser, err := NewKDATransferParser(&marshal.ProtoMarshalizer{})
	require.Nil(t, err)

	return parser
}

func uint64Bytes(value uint64) []byte {
	return big.NewInt(0).SetUint64(value).Bytes()
}

func twoPow64Plus(k uint64) []byte {
	value := big.NewInt(0).Lsh(big.NewInt(1), 64)
	return value.Add(value, big.NewInt(0).SetUint64(k)).Bytes()
}

func zeroPadded(value []byte, length int) []byte {
	return append(make([]byte, length-len(value)), value...)
}

func TestKDATransferParser_ParseKDATransfers(t *testing.T) {
	t.Parallel()

	parser := newTestKDATransferParser(t)

	parsed, err := parser.ParseKDATransfers(testRcvAddr, [][]byte{
		uint64Bytes(2),
		[]byte("KDA-1234"), {}, uint64Bytes(10),
		[]byte("NFT-1234"), uint64Bytes(5), uint64Bytes(1),
		[]byte("deposit"), []byte("arg1"), []byte("arg2"),
	})
	require.Nil(t, err)
	require.Equal(t, testRcvAddr, parsed.RcvAddr)
	require.Equal(t, "deposit", parsed.CallFunction)
	require.Equal(t, [][]byte{[]byte("arg1"), []byte("arg2")}, parsed.CallArgs)
	require.Len(t, parsed.KDATransfers, 2)

	require.Equal(t, []byte("KDA-1234"), parsed.KDATransfers[0].KDATokenName)
	require.Equal(t, uint64(0), parsed.KDATransfers[0].KDATokenNonce)
	require.Equal(t, uint32(core.Fungible), parsed.KDATransfers[0].KDATokenType)
	require.Equal(t, uint64(10), parsed.KDATransfers[0].KDAValue.Uint64())

	require.Equal(t, []byte("NFT-1234"), parsed.KDATransfers[1].KDATokenName)
	require.Equal(t, uint64(5), parsed.KDATransfers[1].KDATokenNonce)
	require.Equal(t, uint32(core.NonFungible), parsed.KDATransfers[1].KDATokenType)
	require.Equal(t, uint64(1), parsed.KDATransfers[1].KDAValue.Uint64())

	parsed, err = parser.ParseKDATransfers(testRcvAddr, [][]byte{
		uint64Bytes(1),
		[]byte("KDA-1234"), {}, uint64Bytes(10),
	})
	require.Nil(t, err)
	require.Empty(t, parsed.CallFunction)
	require.Empty(t, parsed.CallArgs)
	require.Len(t, parsed.KDATransfers, 1)
}

func TestKDATransferParser_AddressAsFirstArgument(t *testing.T) {
	t.Parallel()

	parser := newTestKDATransferParser(t)
	destination := bytes.Repeat([]byte("d"), len(testRcvAddr))

	parsed, err := parser.ParseKDATransfers(testRcvAddr, [][]byte{
		destination,
		uint64Bytes(1),
		[]byte("NFT-1234"), uint64Bytes(5), uint64Bytes(1),
		[]byte("deposit"),
	})
	require.Nil(t, err)
	require.Equal(t, destination, parsed.RcvAddr)
	require.Equal(t, "deposit", parsed.CallFunction)
	require.Empty(t, parsed.CallArgs)
	require.Len(t, parsed.KDATransfers, 1)
	require.Equal(t, uint64(5), parsed.KDATransfers[0].KDATokenNonce)

	parsed, err = parser.ParseKDATransfers(testRcvAddr, [][]byte{
		zeroPadded(uint64Bytes(1), len(testRcvAddr)),
		[]byte("KDA-1234"), {}, uint64Bytes(10),
	})
	require.Nil(t, err)
	require.Equal(t, testRcvAddr, parsed.RcvAddr)
	require.Len(t, parsed.KDATransfers, 1)
}

func TestKDATransferParser_CountWiderThan64BitsIsRejected(t *testing.T) {
	t.Parallel()

	parser := newTestKDATransferParser(t)

	for _, count := range [][]byte{twoPow64Plus(0), twoPow64Plus(1), twoPow64Plus(0x5555555555555556)} {
		parsed, err := parser.ParseKDATransfers(testRcvAddr, [][]byte{
			count,
			[]byte("NFT-1234"), uint64Bytes(5), uint64Bytes(1),
		})
		require.Equal(t, ErrNotEnoughArguments, err)
		require.Nil(t, parsed)
	}

	parsed, err := parser.ParseKDATransfers(testRcvAddr, [][]byte{
		bytes.Repeat([]byte("d"), len(testRcvAddr)),
		twoPow64Plus(1),
		[]byte("NFT-1234"), uint64Bytes(5), uint64Bytes(1),
	})
	require.Equal(t, ErrNotEnoughArguments, err)
	require.Nil(t, parsed)
}

func TestKDATransferParser_CountBeyondArgumentsIsRejected(t *testing.T) {
	t.Parallel()

	parser := newTestKDATransferParser(t)

	for _, count := range []uint64{2, 0x5555555555555556, 0xAAAAAAAAAAAAAAAB, math.MaxUint64} {
		parsed, err := parser.ParseKDATransfers(testRcvAddr, [][]byte{
			uint64Bytes(count),
			[]byte("NFT-1234"), uint64Bytes(5), uint64Bytes(1),
		})
		require.Equal(t, ErrNotEnoughArguments, err)
		require.Nil(t, parsed)
	}

	destination := bytes.Repeat([]byte("d"), len(testRcvAddr))

	parsed, err := parser.ParseKDATransfers(testRcvAddr, [][]byte{
		destination,
		uint64Bytes(2),
		[]byte("NFT-1234"), uint64Bytes(5), uint64Bytes(1),
	})
	require.Equal(t, ErrNotEnoughArguments, err)
	require.Nil(t, parsed)

	parsed, err = parser.ParseKDATransfers(testRcvAddr, [][]byte{
		destination,
		uint64Bytes(1),
		[]byte("NFT-1234"), uint64Bytes(5),
	})
	require.Equal(t, ErrNotEnoughArguments, err)
	require.Nil(t, parsed)
}

func TestKDATransferParser_NonceWiderThan64BitsIsRejected(t *testing.T) {
	t.Parallel()

	parser := newTestKDATransferParser(t)

	for _, nonce := range [][]byte{twoPow64Plus(0), twoPow64Plus(5)} {
		parsed, err := parser.ParseKDATransfers(testRcvAddr, [][]byte{
			uint64Bytes(1),
			[]byte("NFT-1234"), nonce, uint64Bytes(1),
		})
		require.Equal(t, ErrNonceOutOfRange, err)
		require.Nil(t, parsed)
	}

	parsed, err := parser.ParseKDATransfers(testRcvAddr, [][]byte{
		uint64Bytes(1),
		[]byte("NFT-1234"), twoPow64Plus(0), bytes.Repeat([]byte{1}, 33),
	})
	require.Equal(t, ErrNonceOutOfRange, err)
	require.Nil(t, parsed)
}

func TestKDATransferParser_MaxUint64NonceIsAccepted(t *testing.T) {
	t.Parallel()

	parser := newTestKDATransferParser(t)

	parsed, err := parser.ParseKDATransfers(testRcvAddr, [][]byte{
		uint64Bytes(1),
		[]byte("NFT-1234"), uint64Bytes(math.MaxUint64), uint64Bytes(1),
	})
	require.Nil(t, err)
	require.Len(t, parsed.KDATransfers, 1)
	require.Equal(t, uint64(math.MaxUint64), parsed.KDATransfers[0].KDATokenNonce)
	require.Equal(t, uint32(core.NonFungible), parsed.KDATransfers[0].KDATokenType)
}

func TestKDATransferParser_ZeroPaddedArgumentsDecodeAsBefore(t *testing.T) {
	t.Parallel()

	parser := newTestKDATransferParser(t)

	parsed, err := parser.ParseKDATransfers(testRcvAddr, [][]byte{
		zeroPadded(uint64Bytes(1), 9),
		[]byte("NFT-1234"), zeroPadded(uint64Bytes(5), 9), uint64Bytes(1),
	})
	require.Nil(t, err)
	require.Len(t, parsed.KDATransfers, 1)
	require.Equal(t, uint64(5), parsed.KDATransfers[0].KDATokenNonce)
	require.Equal(t, uint32(core.NonFungible), parsed.KDATransfers[0].KDATokenType)
}
