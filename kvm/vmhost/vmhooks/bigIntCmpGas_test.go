package vmhooks_test

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/klever-io/klever-go/kvm/config"
	contextmock "github.com/klever-io/klever-go/kvm/mock/context"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bigIntQueryGasMap is bigIntArithmeticGasMap with distinct fixed costs for BigIntCmp, BigIntSign and
// BigIntLog2 (priced by BigIntLog), so a hook charging another's cost fails
func bigIntQueryGasMap() config.GasScheduleMap {
	gasMap := bigIntArithmeticGasMap()
	gasMap["BigIntAPICost"]["BigIntCmp"] = 211
	gasMap["BigIntAPICost"]["BigIntSign"] = 223
	gasMap["BigIntAPICost"]["BigIntLog"] = 227
	return gasMap
}

type bigIntQueryHook struct {
	name    string
	costKey string
	binary  bool
	// errRet is what the hook returns on a fault
	errRet int32
	call   func(hooks *vmhooks.VMHooksImpl, op1, op2 int32) int32
	// apply is the result, or the error the hook faults with after the copy charge
	apply func(a, b *big.Int) (int32, error)
}

func bigIntQueryHooks() []bigIntQueryHook {
	return []bigIntQueryHook{
		{name: "BigIntCmp", costKey: "BigIntCmp", binary: true, errRet: -2,
			call:  func(h *vmhooks.VMHooksImpl, a, b int32) int32 { return h.BigIntCmp(a, b) },
			apply: func(a, b *big.Int) (int32, error) { return int32(a.Cmp(b)), nil }},
		{name: "BigIntSign", costKey: "BigIntSign", errRet: -2,
			call:  func(h *vmhooks.VMHooksImpl, a, _ int32) int32 { return h.BigIntSign(a) },
			apply: func(a, _ *big.Int) (int32, error) { return int32(a.Sign()), nil }},
		{name: "BigIntLog2", costKey: "BigIntLog", errRet: -1,
			call: func(h *vmhooks.VMHooksImpl, a, _ int32) int32 { return h.BigIntLog2(a) },
			apply: func(a, _ *big.Int) (int32, error) {
				if a.Sign() < 0 {
					return 0, vmhost.ErrBadLowerBounds
				}
				return int32(a.BitLen() - 1), nil // #nosec G115
			}},
	}
}

// BigIntCmp, BigIntSign and BigIntLog2 charged their fixed cost and the copy of their operands
// unbounded, overdrawing when the gas left could not cover them, and answered anyway. From
// FixAuditChangesV6 both are bounded and charged before the answer: an underfunded call fails with
// ErrNotEnoughGas and returns the hook's error value, and one short of the fixed cost does not read
// its operands. The charges are otherwise the same amount, in the same order, so every total on the
// success and error paths is the pre-fork one
func TestVMHooksImpl_BigIntQuery_ChargedBoundedFromV6(t *testing.T) {
	gasMap := bigIntQueryGasMap()
	small := big.NewInt(0x7F123456)
	smallB := big.NewInt(0x1234)
	oversizedA := repeatedBigInt(0xAB, arithOversizedLen)
	oversizedB := repeatedBigInt(0xCD, arithOversizedLen/2)

	type scenario struct {
		name string
		a, b *big.Int
		// missingOperand calls the hook with the first operand handle missing
		missingOperand bool
		// budget, when set, returns the gas left from the success gas, the fixed cost and a's copy
		budget func(success, fixed, firstCopy uint64) uint64
		// failErr, when set, is the fault expected instead of the one apply gives
		failErr error
		// gasUsed, when set, is the total of a fault that overdrew before it: the budget otherwise
		gasUsed func(success, fixed uint64) uint64
	}

	fixedShortfall := func(_, fixed, _ uint64) uint64 { return fixed - 1 }
	firstCopyShortfall := func(_, fixed, firstCopy uint64) uint64 { return fixed + firstCopy - 1 }
	lastShortfall := func(success, _, _ uint64) uint64 { return success - 1 }

	for _, hook := range bigIntQueryHooks() {
		fixed := gasMap["BigIntAPICost"][hook.costKey]
		require.NotZero(t, fixed)
		for _, fixV6 := range []bool{false, true} {
			scenarios := []scenario{
				{name: "small", a: small, b: smallB},
				{name: "smallNegative", a: new(big.Int).Neg(small), b: smallB},
				{name: "zero", a: big.NewInt(0), b: big.NewInt(0)},
				{name: "max32Bytes", a: repeatedBigInt(0xFF, 32), b: repeatedBigInt(0xFF, 32)},
				{name: "over32", a: repeatedBigInt(0xFF, 33), b: repeatedBigInt(0xFE, 40)},
				{name: "oversized", a: oversizedA, b: oversizedB},
				{name: "oversizedReversed", a: oversizedB, b: oversizedA},
				{name: "oversizedEqual", a: oversizedA, b: new(big.Int).Set(oversizedA)},
				{name: "oversizedNegative", a: new(big.Int).Neg(oversizedA), b: oversizedB},
				{name: "missingOperand", missingOperand: true, b: smallB, failErr: vmhost.ErrNoBigIntUnderThisHandle},
			}
			if fixV6 {
				scenarios = append(scenarios,
					scenario{name: "fixedShortfall", a: oversizedA, b: oversizedB, budget: fixedShortfall,
						failErr: vmhost.ErrNotEnoughGas},
					// refused before the operand is read: a missing handle is not reported
					scenario{name: "fixedShortfallMissingOperand", missingOperand: true, b: smallB,
						budget: fixedShortfall, failErr: vmhost.ErrNotEnoughGas},
					scenario{name: "firstCopyShortfall", a: oversizedA, b: oversizedB, budget: firstCopyShortfall,
						failErr: vmhost.ErrNotEnoughGas},
					scenario{name: "lastCopyShortfall", a: oversizedA, b: oversizedB, budget: lastShortfall,
						failErr: vmhost.ErrNotEnoughGas},
					// the copy is charged before the negative operand is refused
					scenario{name: "negativeCopyShortfall", a: new(big.Int).Neg(oversizedA), b: oversizedB,
						budget: lastShortfall, failErr: vmhost.ErrNotEnoughGas},
				)
			} else {
				overdrawn := func(success, _ uint64) uint64 { return success }
				scenarios = append(scenarios,
					// the shortfalls overdraw and the hook answers: the total is the success one
					scenario{name: "fixedShortfall", a: oversizedA, b: oversizedB, budget: fixedShortfall},
					// the fixed cost overdraws, then the missing operand faults with no gas left to burn
					scenario{name: "fixedShortfallMissingOperand", missingOperand: true, b: smallB,
						budget: fixedShortfall, failErr: vmhost.ErrNoBigIntUnderThisHandle,
						gasUsed: func(_, fixed uint64) uint64 { return fixed }},
					scenario{name: "firstCopyShortfall", a: oversizedA, b: oversizedB, budget: firstCopyShortfall},
					scenario{name: "lastCopyShortfall", a: oversizedA, b: oversizedB, budget: lastShortfall},
					scenario{name: "negativeCopyShortfall", a: new(big.Int).Neg(oversizedA), b: oversizedB,
						budget: lastShortfall, gasUsed: overdrawn},
				)
			}

			for _, sc := range scenarios {
				t.Run(fmt.Sprintf("%s/FixAuditChangesV6=%v/%s", hook.name, fixV6, sc.name), func(t *testing.T) {
					host, _ := newMockVMHostWithRealMetering(t, fixV6, gasMap, flatCloneGasProvided)
					runtime := host.RuntimeContext.(*contextmock.RuntimeContextMock)
					managedType := host.ManagedTypes()

					a := sc.a
					op1Handle := arithMissing
					if sc.missingOperand {
						a = big.NewInt(0)
					} else {
						op1Handle = managedType.NewBigInt(new(big.Int).Set(a))
					}
					op2Handle := managedType.NewBigInt(new(big.Int).Set(sc.b))

					success := fixed
					if !sc.missingOperand {
						success += bigIntCopyGas(a)
						if hook.binary {
							success += bigIntCopyGas(sc.b)
						}
					}
					expected, failErr := hook.apply(a, sc.b)
					if sc.failErr != nil {
						failErr = sc.failErr
					}

					budget := success + 1_000_000
					if sc.budget != nil {
						budget = sc.budget(success, fixed, bigIntCopyGas(a))
						require.Less(t, budget, success)
					}

					var ret int32
					gasUsed, _ := runFlatCloneHook(host, budget, func() {
						ret = hook.call(vmhooks.NewVMHooksImpl(host), op1Handle, op2Handle)
					})

					if failErr != nil {
						require.ErrorIs(t, runtime.FailExecutionErr, failErr)
						assert.Equal(t, hook.errRet, ret)
						expectedGas := budget
						if sc.gasUsed != nil {
							expectedGas = sc.gasUsed(success, fixed)
						}
						// a fault burns the gas left
						assert.Equal(t, expectedGas, gasUsed)
					} else {
						require.Nil(t, runtime.FailExecutionErr)
						assert.Equal(t, expected, ret)
						// pre-fork a shortfall overdraws: the total is still the success one
						assert.Equal(t, success, gasUsed)
					}
				})
			}
		}
	}
}
