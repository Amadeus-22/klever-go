package vmhooks_test

import (
	"bytes"
	"fmt"
	"math/big"
	"testing"

	twos "github.com/klever-io/klever-go/kvm/math/twos-complement"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BigIntFinishUnsigned and BigIntFinishSigned serialized the value, finished it and then charged
// PersistPerByte for its bytes, unbounded. From FixAuditChangesV6 the byte length is computed
// without serializing and charged, bounded, first: an underfunded call fails with ErrNotEnoughGas
// without serializing or finishing anything. The totals stay as they were. Not parallel: it counts
// allocations
func TestVMHooksImpl_BigIntFinish_ChargedBeforeSerializingFromV6(t *testing.T) {
	gasMap := perByteHookGasMap()
	persistPerByte := gasMap["BaseOperationCost"]["PersistPerByte"]
	oversized := new(big.Int).SetBytes(bytes.Repeat([]byte{0xAB}, flatCloneOversizedLen))

	hooks := []struct {
		name      string
		fixedCost uint64
		encode    func(*big.Int) []byte
		call      func(hooks *vmhooks.VMHooksImpl, handle int32)
	}{
		{
			name: "BigIntFinishUnsigned", fixedCost: gasMap["BigIntAPICost"]["BigIntFinishUnsigned"],
			encode: func(v *big.Int) []byte { return v.Bytes() },
			call:   func(hooks *vmhooks.VMHooksImpl, handle int32) { hooks.BigIntFinishUnsigned(handle) },
		},
		{
			name: "BigIntFinishSigned", fixedCost: gasMap["BigIntAPICost"]["BigIntFinishSigned"],
			encode: twos.ToBytes,
			call:   func(hooks *vmhooks.VMHooksImpl, handle int32) { hooks.BigIntFinishSigned(handle) },
		},
	}

	type scenario struct {
		name      string
		value     *big.Int
		missing   bool
		budget    uint64
		failErr   error
		cloned    bool
		notCloned bool
	}

	for _, hook := range hooks {
		success := func(value *big.Int) uint64 {
			return hook.fixedCost + persistPerByte*uint64(len(hook.encode(value)))
		}
		for _, fixV6 := range []bool{false, true} {
			scenarios := []scenario{
				{name: "zero", value: big.NewInt(0)},
				{name: "small", value: big.NewInt(0x7F12)},
				{name: "signBoundary", value: big.NewInt(0x80)},
				{name: "negative", value: big.NewInt(-0x8000)},
				{name: "oversized", value: oversized, cloned: true},
				{name: "missing", missing: true, budget: hook.fixedCost, failErr: vmhost.ErrNoBigIntUnderThisHandle},
			}
			if fixV6 {
				scenarios = append(scenarios,
					scenario{name: "fixedShortfall", value: oversized, budget: hook.fixedCost - 1,
						failErr: vmhost.ErrNotEnoughGas, notCloned: true},
					scenario{name: "bytesShortfall", value: oversized, budget: success(oversized) - 1,
						failErr: vmhost.ErrNotEnoughGas, notCloned: true},
				)
			} else {
				// pre-fork the charges overdraw and the hook carries on
				scenarios = append(scenarios,
					scenario{name: "fixedShortfall", value: oversized, budget: hook.fixedCost - 1, cloned: true},
					scenario{name: "bytesShortfall", value: oversized, budget: success(oversized) - 1, cloned: true},
				)
			}

			for _, sc := range scenarios {
				t.Run(fmt.Sprintf("%s/FixAuditChangesV6=%v/%s", hook.name, fixV6, sc.name), func(t *testing.T) {
					host, runtime, finished := newPerByteHost(t, fixV6)
					handle := int32(1234)
					if !sc.missing {
						handle = host.ManagedTypes().NewBigInt(sc.value)
					}
					budget := sc.budget
					if budget == 0 {
						budget = success(oversized) + 1_000_000
					}

					gasUsed, allocated := runFlatCloneHook(host, budget, func() {
						hook.call(vmhooks.NewVMHooksImpl(host), handle)
					})

					if sc.failErr != nil {
						require.ErrorIs(t, runtime.FailExecutionErr, sc.failErr)
						assert.Empty(t, *finished, "nothing may be finished")
					} else {
						require.Nil(t, runtime.FailExecutionErr)
						assert.Equal(t, success(sc.value), gasUsed)
						require.Len(t, *finished, 1)
						assert.Equal(t, hook.encode(sc.value), (*finished)[0])
					}
					if sc.cloned {
						assert.GreaterOrEqual(t, allocated, uint64(flatCloneOversizedLen))
					}
					if sc.notCloned {
						assert.Less(t, allocated, uint64(flatCloneNoCloneBelow), "the value must not be serialized")
					}
				})
			}
		}
	}
}

// BigIntToString converted the value, wrote it and then charged DataCopyPerByte for the digits,
// unbounded. From FixAuditChangesV6 the flat cost is bounded, the operand is paid for as every other
// big int hook pays for it (CopyPerByteForTooBig a byte above 32 bytes, bounded; KLC-2728), and the
// call is refused before the conversion when the gas left cannot cover an upper bound on the digits;
// the exact digits are then charged, as before, ahead of the write. So the totals grow by the operand
// term, and a budget between the exact charge and the bound is refused (slackWindow). Not parallel:
// it counts allocations
func TestVMHooksImpl_BigIntToString_RefusedBeforeConvertingFromV6(t *testing.T) {
	gasMap := perByteHookGasMap()
	fixedCost := gasMap["BigIntAPICost"]["BigIntFinishSigned"]
	perByte := gasMap["BaseOperationCost"]["DataCopyPerByte"]
	tooBigPerByte := gasMap["BigIntAPICost"]["CopyPerByteForTooBig"]
	require.NotZero(t, tooBigPerByte, "the operand term must be priced for operandShortfall to mean anything")
	// a 1 MB conversion takes about half a second; 64 KB has ~158K digits, plenty to count
	const oversizedLen = 64 << 10
	oversized := new(big.Int).SetBytes(bytes.Repeat([]byte{0xAB}, oversizedLen))
	oversizedDigits := len(oversized.String())
	// operandCost is what the operand costs from FixAuditChangesV6: nothing up to 32 bytes
	operandCost := func(value *big.Int) uint64 {
		byteLen := uint64(value.BitLen()+7) / 8
		if byteLen <= 32 {
			return 0
		}
		return tooBigPerByte * byteLen
	}
	// digitsBound is the upper bound on the digits, and the sign, the call is refused below
	digitsBound := func(value *big.Int) uint64 {
		return (uint64(value.BitLen())*30103+99999)/100000 + 1
	}
	// without this, operandShortfall would be refused by the digits bound whether or not, and
	// wherever, the operand is charged
	require.Greater(t, operandCost(oversized), perByte*digitsBound(oversized),
		"operandShortfall must be refused by the operand charge, not the digits bound")
	untouched := []byte("untouched")
	max32 := new(big.Int).SetBytes(bytes.Repeat([]byte{0xFF}, 32))
	min33 := new(big.Int).Lsh(big.NewInt(1), 32*8)

	type scenario struct {
		name      string
		value     *big.Int
		missing   bool
		budget    uint64
		failErr   error
		cloned    bool
		notCloned bool
	}

	for _, fixV6 := range []bool{false, true} {
		success := func(value *big.Int) uint64 {
			gas := fixedCost + perByte*uint64(len(value.String()))
			if fixV6 {
				gas += operandCost(value)
			}
			return gas
		}
		slack := perByte * (digitsBound(oversized) - uint64(oversizedDigits))
		scenarios := []scenario{
			{name: "zero", value: big.NewInt(0)},
			{name: "small", value: big.NewInt(1234567890)},
			{name: "negative", value: big.NewInt(-1000)},
			// the largest operand that is free, and the smallest that is not
			{name: "max32Bytes", value: max32},
			{name: "negativeMax32Bytes", value: new(big.Int).Neg(max32)},
			{name: "min33Bytes", value: min33},
			{name: "oversized", value: oversized, cloned: true},
			{name: "negativeOversized", value: new(big.Int).Neg(oversized), cloned: true},
			{name: "missing", missing: true, budget: fixedCost, failErr: vmhost.ErrNoBigIntUnderThisHandle},
		}
		if fixV6 {
			scenarios = append(scenarios,
				scenario{name: "fixedShortfall", value: oversized, budget: fixedCost - 1,
					failErr: vmhost.ErrNotEnoughGas, notCloned: true},
				// enough for the flat cost, not for the operand: refused before the conversion
				scenario{name: "operandShortfall", value: oversized, budget: fixedCost + operandCost(oversized) - 1,
					failErr: vmhost.ErrNotEnoughGas, notCloned: true},
				scenario{name: "digitsShortfall", value: oversized, budget: success(oversized) - 1,
					failErr: vmhost.ErrNotEnoughGas, notCloned: true},
				// enough for the exact digits, not for the bound: refused, which pre-fork it was not.
				// The value is positive, so the bound keeps a byte for a sign it has none of
				scenario{name: "slackWindow", value: oversized, budget: success(oversized) + slack - 1,
					failErr: vmhost.ErrNotEnoughGas, notCloned: true},
				// enough for the bound: converted, and charged the exact digits only
				scenario{name: "atBound", value: oversized, budget: success(oversized) + slack, cloned: true},
			)
		} else {
			// pre-fork the charges overdraw and the hook carries on
			scenarios = append(scenarios,
				scenario{name: "fixedShortfall", value: oversized, budget: fixedCost - 1, cloned: true},
				scenario{name: "digitsShortfall", value: oversized, budget: success(oversized) - 1, cloned: true},
				scenario{name: "slackWindow", value: oversized, budget: success(oversized) + slack - 1, cloned: true},
				scenario{name: "atBound", value: oversized, budget: success(oversized) + slack, cloned: true},
			)
		}

		for _, sc := range scenarios {
			t.Run(fmt.Sprintf("FixAuditChangesV6=%v/%s", fixV6, sc.name), func(t *testing.T) {
				host, runtime, _ := newPerByteHost(t, fixV6)
				managedTypes := host.ManagedTypes()
				handle := int32(1234)
				if !sc.missing {
					handle = managedTypes.NewBigInt(sc.value)
				}
				destination := managedTypes.NewManagedBufferFromBytes(untouched)
				budget := sc.budget
				if budget == 0 {
					budget = success(oversized) + 1_000_000
				}

				gasUsed, allocated := runFlatCloneHook(host, budget, func() {
					vmhooks.NewVMHooksImpl(host).BigIntToString(handle, destination)
				})

				destinationBytes, err := managedTypes.GetBytes(destination)
				require.NoError(t, err)
				if sc.failErr != nil {
					require.ErrorIs(t, runtime.FailExecutionErr, sc.failErr)
					assert.Equal(t, untouched, destinationBytes, "the destination must not be written")
				} else {
					require.Nil(t, runtime.FailExecutionErr)
					assert.Equal(t, success(sc.value), gasUsed)
					assert.Equal(t, sc.value.String(), string(destinationBytes))
				}
				if sc.cloned {
					assert.GreaterOrEqual(t, allocated, uint64(oversizedDigits))
				}
				if sc.notCloned {
					assert.Less(t, allocated, uint64(oversizedDigits/4), "the value must not be converted")
				}
			})
		}
	}
}
