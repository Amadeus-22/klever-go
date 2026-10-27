package vmhooks

import (
	"bytes"
	"fmt"
	"math/big"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

// decimalStringMaxLength must never be below len(value.String()): from FixAuditChangesV6
// BigIntToString refuses, before converting, a call whose gas left cannot cover DataCopyPerByte
// for that many bytes, and then charges the exact length, which must therefore fit
func TestDecimalStringMaxLength_IsAnUpperBound(t *testing.T) {
	t.Parallel()

	values := []*big.Int{big.NewInt(0), big.NewInt(1), big.NewInt(-1), big.NewInt(9), big.NewInt(-9)}
	ten := big.NewInt(10)
	for k := int64(0); k <= 400; k++ {
		power := new(big.Int).Exp(ten, big.NewInt(k), nil)
		for delta := int64(-1); delta <= 1; delta++ {
			value := new(big.Int).Add(power, big.NewInt(delta))
			values = append(values, value, new(big.Int).Neg(value))
		}
	}
	for bits := uint(0); bits <= 1400; bits++ {
		power := new(big.Int).Lsh(big.NewInt(1), bits)
		for delta := int64(-1); delta <= 1; delta++ {
			value := new(big.Int).Add(power, big.NewInt(delta))
			values = append(values, value, new(big.Int).Neg(value))
		}
	}
	random := rand.New(rand.NewSource(2755))
	for i := 0; i < 5000; i++ {
		buf := make([]byte, random.Intn(600))
		random.Read(buf)
		value := new(big.Int).SetBytes(buf)
		if random.Intn(2) == 0 {
			value.Neg(value)
		}
		values = append(values, value)
	}
	// all nines and the largest value of each size, where the digit count is the highest for the bits
	for _, size := range []int{1, 32, 1024, 64 * 1024} {
		values = append(values, new(big.Int).SetBytes(bytes.Repeat([]byte{0xFF}, size)))
		nines := new(big.Int).Sub(new(big.Int).Exp(ten, big.NewInt(int64(size)), nil), big.NewInt(1))
		values = append(values, nines, new(big.Int).Neg(nines))
	}

	// the bound is tight, so that the refusal before the conversion stays close to the exact charge
	// after it. A value of b bits, b > 0, has at least floor((b-1)*log10(2))+1 digits, and the bound
	// counts ceil(b*0.30103) of them: the ceiling of a real and the floor of one less than 1 below it
	// are at most 2 apart, so the bound has at most one digit too many, and a positive value leaves the sign's byte unused
	const maxSlack = 2
	slackSeen := uint64(0)
	for _, value := range values {
		exact := uint64(len(value.String()))
		bound := decimalStringMaxLength(value)
		require.GreaterOrEqual(t, bound, exact, "value with %d bits", value.BitLen())
		require.LessOrEqual(t, bound, exact+maxSlack, "value with %d bits", value.BitLen())
		slackSeen = max(slackSeen, bound-exact)
	}
	require.Equal(t, uint64(maxSlack), slackSeen, "the slack allowed must be reached")
}

var benchmarkBigIntStringSink string

// BenchmarkBigIntString measures what BigIntToString's conversion costs in time against the gas
// the hook charges for it from FixAuditChangesV6 (BigIntFinishSigned, CopyPerByteForTooBig per
// operand byte above 32 bytes and DataCopyPerByte per digit, as in gasScheduleV1.toml), next to the
// linear copy GetArgument charges DataCopyPerByte a byte for. The largest operand MaxGasPerTX
// affords is among the sizes; the larger ones are over it
func BenchmarkBigIntString(b *testing.B) {
	const (
		fixedCost            = 1_000 // BigIntFinishSigned
		copyPerByteForTooBig = 1_000
		dataCopyPerByte      = 50
		getArgumentCost      = 100
		maxGasPerTX          = 400_000_000
	)
	// gasFor is what the hook charges for an operand of size bytes with digits digits and sign
	gasFor := func(size int, digits uint64) uint64 {
		gas := uint64(fixedCost) + dataCopyPerByte*digits
		if size > 32 {
			gas += copyPerByteForTooBig * uint64(size)
		}
		return gas
	}
	// the largest operand whose bound on the digits still fits in maxGasPerTX
	maxAffordable := 0
	for size := 1; ; size++ {
		topBit := new(big.Int).Lsh(big.NewInt(1), uint(size*8-1))
		if gasFor(size, decimalStringMaxLength(topBit)) > maxGasPerTX {
			break
		}
		maxAffordable = size
	}

	random := rand.New(rand.NewSource(2728))
	for _, size := range []int{32, 1 << 10, 64 << 10, 256 << 10, maxAffordable, 400 << 10, 1 << 20} {
		buf := make([]byte, size)
		random.Read(buf)
		buf[0] |= 0x80
		value := new(big.Int).SetBytes(buf)
		digits := len(value.String())
		gas := float64(gasFor(size, uint64(digits)))

		b.Run(fmt.Sprintf("String/%dB", size), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				benchmarkBigIntStringSink = value.String()
			}
			nsPerOp := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
			b.ReportMetric(float64(digits), "digits")
			b.ReportMetric(gas, "gas")
			b.ReportMetric(nsPerOp/gas, "ns/gas")
		})
	}

	argument := make([]byte, 1<<20)
	random.Read(argument)
	gas := float64(getArgumentCost + dataCopyPerByte*len(argument))
	b.Run("GetArgumentCopy/1048576B", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchmarkBigIntStringSink = string(bytes.Clone(argument)[:1])
		}
		nsPerOp := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
		b.ReportMetric(gas, "gas")
		b.ReportMetric(nsPerOp/gas, "ns/gas")
	})
}
