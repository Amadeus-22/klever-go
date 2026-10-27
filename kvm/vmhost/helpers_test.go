package vmhost

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/klever-io/klever-go/vmcommon"
	"github.com/stretchr/testify/require"
)

func TestGuardedGetBytesSlice(t *testing.T) {
	t.Parallel()

	dataSlice := []byte("data1_data2_data3_data4")

	slice, err := GuardedGetBytesSlice(dataSlice, 100, 100)
	require.Nil(t, slice)
	require.NotNil(t, err)

	slice, err = GuardedGetBytesSlice(dataSlice, 5, -1)
	require.Nil(t, slice)
	require.NotNil(t, err)

	expectedResult := []byte("data1")
	slice, err = GuardedGetBytesSlice(dataSlice, 0, 5)
	require.Nil(t, err)
	require.True(t, bytes.Equal(expectedResult, slice))
}

func TestInverseBytes(t *testing.T) {
	t.Parallel()

	data := []byte("qwerty")
	expectedData := []byte("ytrewq")

	result := InverseBytes(data)
	require.Equal(t, expectedData, result)

	result = InverseBytes(nil)
	require.Equal(t, []byte{}, result)

	result = InverseBytes([]byte("a"))
	require.Equal(t, []byte("a"), result)
}

func TestSliceIsOutOfBounds(t *testing.T) {
	t.Parallel()

	const bufferLength = 8

	inBounds := []struct{ start, length int32 }{{0, 0}, {0, 8}, {2, 6}, {8, 0}}
	outOfBounds := []struct{ start, length int32 }{{-1, 1}, {0, -1}, {0, 9}, {8, 1}}

	for _, fixAuditChangesV5 := range []bool{false, true} {
		for _, c := range inBounds {
			require.False(t, SliceIsOutOfBounds(fixAuditChangesV5, c.start, c.length, bufferLength), "fork=%v %+v", fixAuditChangesV5, c)
		}
		for _, c := range outOfBounds {
			require.True(t, SliceIsOutOfBounds(fixAuditChangesV5, c.start, c.length, bufferLength), "fork=%v %+v", fixAuditChangesV5, c)
		}
	}

	// start+length wraps to a negative int32: pre-fork the legacy arithmetic lets it through
	// (the caller's slice expression then panics), post-fork it is rejected up front
	require.False(t, SliceIsOutOfBounds(false, 2147483647, 1, bufferLength))
	require.True(t, SliceIsOutOfBounds(true, 2147483647, 1, bufferLength))
	require.False(t, SliceIsOutOfBounds(false, 0x40000000, 0x40000000, bufferLength))
	require.True(t, SliceIsOutOfBounds(true, 0x40000000, 0x40000000, bufferLength))
}

func TestWithoutZeroValueTransfers(t *testing.T) {
	t.Parallel()

	transfer := func(token string, value *big.Int) *vmcommon.KDATransfer {
		return &vmcommon.KDATransfer{KDATokenName: []byte(token), KDAValue: value}
	}

	t.Run("nil slice", func(t *testing.T) {
		t.Parallel()

		require.Empty(t, WithoutZeroValueTransfers(nil))
	})

	t.Run("all zero", func(t *testing.T) {
		t.Parallel()

		transfers := []*vmcommon.KDATransfer{transfer("FAKE-1234", big.NewInt(0)), transfer("NFT-1234", nil)}

		require.Empty(t, WithoutZeroValueTransfers(transfers))
		require.Len(t, transfers, 2)
		require.NotNil(t, transfers[0])
		require.NotNil(t, transfers[1])
	})

	t.Run("mixed keeps the non-zero transfers in order and by pointer", func(t *testing.T) {
		t.Parallel()

		first := transfer("KDA-1", big.NewInt(1000))
		zero := transfer("FAKE-1234", big.NewInt(0))
		nilValue := transfer("NFT-1234", nil)
		last := transfer("KDA-2", big.NewInt(7))
		transfers := []*vmcommon.KDATransfer{first, zero, nilValue, last}

		result := WithoutZeroValueTransfers(transfers)

		// the same pointers: the built-in marks these executed in place, and the callee input must see it
		require.Len(t, result, 2)
		require.Same(t, first, result[0])
		require.Same(t, last, result[1])

		// the input keeps its length and order: callBuiltinFunction may hand over input.KDATransfers itself
		require.Len(t, transfers, 4)
		require.Same(t, first, transfers[0])
		require.Same(t, zero, transfers[1])
		require.Same(t, nilValue, transfers[2])
		require.Same(t, last, transfers[3])
	})
}
