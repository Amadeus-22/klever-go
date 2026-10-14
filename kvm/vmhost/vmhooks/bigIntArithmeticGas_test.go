package vmhooks_test

import (
	"bytes"
	"fmt"
	"math/big"
	"math/rand"
	"testing"

	"github.com/klever-io/klever-go/kvm/config"
	contextmock "github.com/klever-io/klever-go/kvm/mock/context"
	gasschedules "github.com/klever-io/klever-go/kvm/scenarioexec/gasSchedules"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// arithOversizedLen is the size of the oversized operands: the op on them allocates far more
	// than arithNoOpBelow
	arithOversizedLen = 64 * 1024
	arithNoOpBelow    = arithOversizedLen / 4
	// arithFreshDest is a destination handle no test creates up front
	arithFreshDest = int32(7777)
	// arithMissing is an operand handle no test creates
	arithMissing = int32(9999)
	// arithAliasMissing is a handle used as both the destination and the first operand, never created
	// up front
	arithAliasMissing = int32(8888)
	// arithMaxCopyLen is the largest byte length ConsumeGasForBigIntCopy charges nothing for
	arithMaxCopyLen = 32
)

// bigIntArithmeticGasMap is perByteHookGasMap with a distinct fixed cost per arithmetic hook, so a
// hook charging another's cost fails, and CopyPerByteForTooBig (1_000) distinct from DataCopyPerByte
func bigIntArithmeticGasMap() config.GasScheduleMap {
	gasMap := perByteHookGasMap()
	bigIntCosts := map[string]uint64{
		"BigIntAdd": 101, "BigIntSub": 103, "BigIntMul": 107, "BigIntTDiv": 109, "BigIntTMod": 113,
		"BigIntEDiv": 127, "BigIntEMod": 131, "BigIntSqrt": 137, "BigIntAbs": 139, "BigIntNeg": 149,
		"BigIntNot": 151, "BigIntAnd": 157, "BigIntOr": 163, "BigIntXor": 167, "BigIntShr": 173,
		"BigIntShl": 197, "BigIntPow": 199,
	}
	for name, cost := range bigIntCosts {
		gasMap["BigIntAPICost"][name] = cost
	}
	gasMap["BigFloatAPICost"]["BigFloatFloor"] = 179
	gasMap["BigFloatAPICost"]["BigFloatCeil"] = 181
	gasMap["BigFloatAPICost"]["BigFloatTruncate"] = 191
	gasMap["BigFloatAPICost"]["BigFloatSetBigInt"] = 193
	return gasMap
}

// newBigIntArithmeticHost is a mock host on the real metering and managed types, priced by
// bigIntArithmeticGasMap, where both the BigInt and the BigFloat API fail execution on an error
func newBigIntArithmeticHost(t *testing.T, fixV6 bool) (*contextmock.VMHostMock, *contextmock.RuntimeContextMock) {
	t.Helper()
	host, _ := newMockVMHostWithRealMetering(t, fixV6, bigIntArithmeticGasMap(), flatCloneGasProvided)
	runtime := host.RuntimeContext.(*contextmock.RuntimeContextMock)
	runtime.FailBigIntAPI = true
	runtime.FailBigFloatAPI = true
	return host, runtime
}

// bigIntCopyGas is what ConsumeGasForBigIntCopy charges for value under bigIntArithmeticGasMap
func bigIntCopyGas(value *big.Int) uint64 {
	byteLen := (value.BitLen() + 7) / 8
	if byteLen <= arithMaxCopyLen {
		return 0
	}
	return uint64(byteLen) * bigIntArithmeticGasMap()["BigIntAPICost"]["CopyPerByteForTooBig"]
}

func repeatedBigInt(b byte, n int) *big.Int {
	return new(big.Int).SetBytes(bytes.Repeat([]byte{b}, n))
}

type bigIntArithmeticHook struct {
	name string
	// unary hooks take one operand handle; shift hooks take an operand handle and a bit count
	unary bool
	shift bool
	// chargesDest is set when the hook charges the destination's current value too
	chargesDest bool
	call        func(hooks *vmhooks.VMHooksImpl, dest, op1, op2 int32)
	// apply is the result, or the error the hook faults with after the copy charge
	apply func(a, b *big.Int, bits int32) (*big.Int, error)
}

func binaryOp(op func(z, a, b *big.Int) *big.Int) func(a, b *big.Int, _ int32) (*big.Int, error) {
	return func(a, b *big.Int, _ int32) (*big.Int, error) {
		return op(new(big.Int), a, b), nil
	}
}

func divisionOp(op func(z, a, b *big.Int) *big.Int) func(a, b *big.Int, _ int32) (*big.Int, error) {
	return func(a, b *big.Int, _ int32) (*big.Int, error) {
		if b.Sign() == 0 {
			return nil, vmhost.ErrDivZero
		}
		return op(new(big.Int), a, b), nil
	}
}

func bitwiseOp(op func(z, a, b *big.Int) *big.Int) func(a, b *big.Int, _ int32) (*big.Int, error) {
	return func(a, b *big.Int, _ int32) (*big.Int, error) {
		if a.Sign() < 0 || b.Sign() < 0 {
			return nil, vmhost.ErrBitwiseNegative
		}
		return op(new(big.Int), a, b), nil
	}
}

func bigIntArithmeticHooks() []bigIntArithmeticHook {
	return []bigIntArithmeticHook{
		{name: "BigIntAdd", chargesDest: true, apply: binaryOp((*big.Int).Add),
			call: func(h *vmhooks.VMHooksImpl, d, a, b int32) { h.BigIntAdd(d, a, b) }},
		{name: "BigIntSub", chargesDest: true, apply: binaryOp((*big.Int).Sub),
			call: func(h *vmhooks.VMHooksImpl, d, a, b int32) { h.BigIntSub(d, a, b) }},
		{name: "BigIntMul", chargesDest: true, apply: binaryOp((*big.Int).Mul),
			call: func(h *vmhooks.VMHooksImpl, d, a, b int32) { h.BigIntMul(d, a, b) }},
		{name: "BigIntTDiv", chargesDest: true, apply: divisionOp((*big.Int).Quo),
			call: func(h *vmhooks.VMHooksImpl, d, a, b int32) { h.BigIntTDiv(d, a, b) }},
		{name: "BigIntTMod", chargesDest: true, apply: divisionOp((*big.Int).Rem),
			call: func(h *vmhooks.VMHooksImpl, d, a, b int32) { h.BigIntTMod(d, a, b) }},
		{name: "BigIntEDiv", chargesDest: true, apply: divisionOp((*big.Int).Div),
			call: func(h *vmhooks.VMHooksImpl, d, a, b int32) { h.BigIntEDiv(d, a, b) }},
		{name: "BigIntEMod", chargesDest: true, apply: divisionOp((*big.Int).Mod),
			call: func(h *vmhooks.VMHooksImpl, d, a, b int32) { h.BigIntEMod(d, a, b) }},
		{name: "BigIntSqrt", unary: true, chargesDest: true,
			apply: func(a, _ *big.Int, _ int32) (*big.Int, error) {
				if a.Sign() < 0 {
					return nil, vmhost.ErrBadLowerBounds
				}
				return new(big.Int).Sqrt(a), nil
			},
			call: func(h *vmhooks.VMHooksImpl, d, a, _ int32) { h.BigIntSqrt(d, a) }},
		{name: "BigIntAbs", unary: true, chargesDest: true,
			apply: func(a, _ *big.Int, _ int32) (*big.Int, error) { return new(big.Int).Abs(a), nil },
			call:  func(h *vmhooks.VMHooksImpl, d, a, _ int32) { h.BigIntAbs(d, a) }},
		{name: "BigIntNeg", unary: true, chargesDest: true,
			apply: func(a, _ *big.Int, _ int32) (*big.Int, error) { return new(big.Int).Neg(a), nil },
			call:  func(h *vmhooks.VMHooksImpl, d, a, _ int32) { h.BigIntNeg(d, a) }},
		{name: "BigIntNot", unary: true, chargesDest: true,
			apply: func(a, _ *big.Int, _ int32) (*big.Int, error) {
				if a.Sign() < 0 {
					return nil, vmhost.ErrBitwiseNegative
				}
				return new(big.Int).Not(a), nil
			},
			call: func(h *vmhooks.VMHooksImpl, d, a, _ int32) { h.BigIntNot(d, a) }},
		{name: "BigIntAnd", apply: bitwiseOp((*big.Int).And),
			call: func(h *vmhooks.VMHooksImpl, d, a, b int32) { h.BigIntAnd(d, a, b) }},
		{name: "BigIntOr", apply: bitwiseOp((*big.Int).Or),
			call: func(h *vmhooks.VMHooksImpl, d, a, b int32) { h.BigIntOr(d, a, b) }},
		{name: "BigIntXor", apply: bitwiseOp((*big.Int).Xor),
			call: func(h *vmhooks.VMHooksImpl, d, a, b int32) { h.BigIntXor(d, a, b) }},
		{name: "BigIntShr", unary: true, shift: true,
			apply: func(a, _ *big.Int, bits int32) (*big.Int, error) {
				if a.Sign() < 0 || bits < 0 {
					return nil, vmhost.ErrShiftNegative
				}
				return new(big.Int).Rsh(a, uint(bits)), nil
			},
			call: func(h *vmhooks.VMHooksImpl, d, a, bits int32) { h.BigIntShr(d, a, bits) }},
		{name: "BigIntShl", unary: true, shift: true,
			apply: func(a, _ *big.Int, bits int32) (*big.Int, error) {
				if a.Sign() < 0 || bits < 0 {
					return nil, vmhost.ErrShiftNegative
				}
				return new(big.Int).Lsh(a, uint(bits)), nil
			},
			call: func(h *vmhooks.VMHooksImpl, d, a, bits int32) { h.BigIntShl(d, a, bits) }},
	}
}

// gas is what the hook charges, on either side of the fork, when it gets past its copy charge:
// the fixed cost, then the copy of dest (its value before the op), a and b in that order, and for
// the shift the result it writes
func (hook bigIntArithmeticHook) gas(fixed uint64, dest, a, b *big.Int, bits int32) uint64 {
	gas := fixed
	if hook.chargesDest {
		gas += bigIntCopyGas(dest)
	}
	gas += bigIntCopyGas(a)
	if !hook.unary {
		gas += bigIntCopyGas(b)
	}
	if hook.shift {
		if result, err := hook.apply(a, b, bits); err == nil {
			gas += bigIntCopyGas(result)
		}
	}
	return gas
}

type bigIntArithmeticScenario struct {
	name string
	// dest is the destination's value before the call; nil leaves the destination handle fresh
	dest *big.Int
	a, b *big.Int
	bits int32
	// alias calls the hook with one missing handle as both the destination and the first operand
	alias bool
	// missingOperand calls the hook with the first operand handle missing
	missingOperand bool
	// budget below the fixed cost plus the copy charges: shortfallAt returns it from the success
	// gas, the fixed cost and the first charged copy
	shortfallAt func(success, fixed, firstCopy uint64) uint64
	// shortfallFails is set from FixAuditChangesV6, where the shortfall is refused
	shortfallFails bool
	// noOp asserts the op did not run on the oversized operands; op asserts it did
	noOp bool
	op   bool
}

// Each arithmetic hook used to charge its fixed cost and the copy of its operands (and its
// destination's old value) unbounded, overdrawing when the gas left could not cover them, and do the
// op anyway. From FixAuditChangesV6 both are bounded and charged before the op: an underfunded call
// fails with ErrNotEnoughGas, leaving the destination as it was and the op not done. The charges are
// otherwise the same amount, in the same order, against the same values, so every total on the
// success and error paths is the pre-fork one. Not parallel: it counts allocations
func TestVMHooksImpl_BigIntArithmetic_ChargedBoundedBeforeTheOpFromV6(t *testing.T) {
	gasMap := bigIntArithmeticGasMap()
	small := big.NewInt(0x7F123456)
	smallB := big.NewInt(0x1234)
	oversizedA := repeatedBigInt(0xAB, arithOversizedLen)
	oversizedB := repeatedBigInt(0xCD, arithOversizedLen/2)
	destOver32 := repeatedBigInt(0x11, 40)
	oversizedDest := repeatedBigInt(0x22, arithOversizedLen)

	for _, hook := range bigIntArithmeticHooks() {
		fixed := gasMap["BigIntAPICost"][hook.name]
		require.NotZero(t, fixed)
		for _, fixV6 := range []bool{false, true} {
			scenarios := []bigIntArithmeticScenario{
				{name: "small", a: small, b: smallB, bits: 3},
				{name: "smallNegative", a: new(big.Int).Neg(small), b: smallB, bits: 3},
				{name: "zeroSecond", a: small, b: big.NewInt(0), bits: 0},
				{name: "max32Bytes", a: repeatedBigInt(0xFF, 32), b: repeatedBigInt(0xFF, 32), bits: 1},
				{name: "oversized", a: oversizedA, b: oversizedB, bits: 8},
				{name: "oversizedInvalid", a: new(big.Int).Neg(oversizedA), b: big.NewInt(0), bits: 8},
				{name: "existingDestOver32", dest: destOver32, a: small, b: smallB, bits: 3},
				{name: "existingDestOversized", dest: oversizedDest, a: oversizedA, b: oversizedB, bits: 8},
				// the destination is created before the operands are read, so a missing handle used as
				// both reads as zero: this must not turn into a missing-handle fault
				{name: "aliasMissing", alias: true, b: smallB, bits: 3},
				{name: "missingOperand", missingOperand: true, b: smallB, bits: 3},
			}
			if hook.shift {
				// a shift that leaves a partial top byte pins the result length rounding up, not down; a
				// shift past the length leaves zero right and more than doubles the operand left
				unalignedShortfall := func(success, _, _ uint64) uint64 { return success - 1 }
				scenarios = append(scenarios,
					bigIntArithmeticScenario{name: "negativeBits", a: small, bits: -1},
					bigIntArithmeticScenario{name: "oversizedUnaligned", a: oversizedA, bits: 3},
					bigIntArithmeticScenario{name: "oversizedUnalignedShortfall", a: oversizedA, bits: 3,
						shortfallAt: unalignedShortfall, shortfallFails: fixV6, noOp: fixV6, op: !fixV6},
					bigIntArithmeticScenario{name: "shiftPastLength", a: oversizedA, bits: 8*arithOversizedLen + 5},
				)
			}
			fixedShortfall := func(_, fixed, _ uint64) uint64 { return fixed - 1 }
			lastShortfall := func(success, _, _ uint64) uint64 { return success - 1 }
			firstCopyShortfall := func(_, fixed, firstCopy uint64) uint64 { return fixed + firstCopy - 1 }
			scenarios = append(scenarios,
				bigIntArithmeticScenario{name: "fixedShortfall", a: oversizedA, b: oversizedB, bits: 8,
					shortfallAt: fixedShortfall, shortfallFails: fixV6, noOp: fixV6, op: !fixV6},
				bigIntArithmeticScenario{name: "firstCopyShortfall", a: oversizedA, b: oversizedB, bits: 8,
					shortfallAt: firstCopyShortfall, shortfallFails: fixV6, noOp: fixV6, op: !fixV6},
				bigIntArithmeticScenario{name: "lastCopyShortfall", a: oversizedA, b: oversizedB, bits: 8,
					shortfallAt: lastShortfall, shortfallFails: fixV6, noOp: fixV6, op: !fixV6},
				bigIntArithmeticScenario{name: "destCopyShortfall", dest: oversizedDest, a: small, b: smallB, bits: 3,
					shortfallAt: lastShortfall, shortfallFails: fixV6},
			)

			for _, sc := range scenarios {
				t.Run(fmt.Sprintf("%s/FixAuditChangesV6=%v/%s", hook.name, fixV6, sc.name), func(t *testing.T) {
					host, runtime := newBigIntArithmeticHost(t, fixV6)
					managedType := host.ManagedTypes()

					a, b := sc.a, sc.b
					if sc.alias || sc.missingOperand {
						a = big.NewInt(0)
					}
					if b == nil {
						b = big.NewInt(0)
					}
					destBefore := big.NewInt(0)
					if sc.dest != nil {
						destBefore = new(big.Int).Set(sc.dest)
					}

					destHandle := arithFreshDest
					if sc.dest != nil {
						destHandle = managedType.NewBigInt(new(big.Int).Set(sc.dest))
					}
					op1Handle := arithMissing
					if sc.alias {
						destHandle = arithAliasMissing
						op1Handle = arithAliasMissing
					} else if !sc.missingOperand {
						op1Handle = managedType.NewBigInt(new(big.Int).Set(a))
					}
					op2Handle := sc.bits
					if !hook.shift {
						op2Handle = managedType.NewBigInt(new(big.Int).Set(b))
					}

					success := hook.gas(fixed, destBefore, a, b, sc.bits)
					expected, opErr := hook.apply(a, b, sc.bits)
					failErr := opErr
					if sc.missingOperand {
						failErr = vmhost.ErrNoBigIntUnderThisHandle
					}

					budget := success + 1_000_000
					if sc.shortfallAt != nil {
						firstCopy := bigIntCopyGas(a)
						if hook.chargesDest && sc.dest != nil {
							firstCopy = bigIntCopyGas(destBefore)
						}
						budget = sc.shortfallAt(success, fixed, firstCopy)
						require.Less(t, budget, success)
						if sc.shortfallFails {
							failErr = vmhost.ErrNotEnoughGas
						}
					}

					gasUsed, allocated := runFlatCloneHook(host, budget, func() {
						hook.call(vmhooks.NewVMHooksImpl(host), destHandle, op1Handle, op2Handle)
					})

					dest, err := managedType.GetBigInt(destHandle)
					if err != nil {
						// only a refused fixed cost stops the hook before it creates the destination
						require.Nil(t, sc.dest)
						require.ErrorIs(t, runtime.FailExecutionErr, vmhost.ErrNotEnoughGas)
						dest = big.NewInt(0)
					}
					if failErr != nil {
						require.ErrorIs(t, runtime.FailExecutionErr, failErr)
						// a fault burns the gas left
						assert.Equal(t, budget, gasUsed)
						assert.Equal(t, 0, destBefore.Cmp(dest), "the destination must be left as it was")
					} else {
						require.Nil(t, runtime.FailExecutionErr)
						// pre-fork a shortfall overdraws: the total is still the success one
						assert.Equal(t, success, gasUsed)
						assert.Equal(t, 0, expected.Cmp(dest), "the destination must hold the result")
					}
					if sc.noOp {
						assert.Less(t, allocated, uint64(arithNoOpBelow), "the op must not run")
					}
					if sc.op && (hook.name == "BigIntMul" || hook.name == "BigIntSqrt" || hook.name == "BigIntTDiv") {
						assert.GreaterOrEqual(t, allocated, uint64(arithNoOpBelow), "the op runs")
					}
				})
			}
		}
	}
}

// powResultGas is what BigIntPow charges for its result under bigIntArithmeticGasMap: b × BitLen(a) / 8
// bytes, rounded down, at CopyPerByteForTooBig each, with no free 32 bytes
func powResultGas(a, b *big.Int) uint64 {
	byteLen := new(big.Int).Div(new(big.Int).Mul(b, big.NewInt(int64(a.BitLen()))), big.NewInt(8))
	return byteLen.Uint64() * bigIntArithmeticGasMap()["BigIntAPICost"]["CopyPerByteForTooBig"]
}

// BigIntPow charged its fixed cost and the copy of its base and exponent unbounded, overdrawing when
// the gas left could not cover them; only its result-size charge was bounded. From FixAuditChangesV6
// the fixed cost and the copies are bounded too, in the same order (fixed cost, result size, base,
// exponent), all before Exp: an underfunded call fails with ErrNotEnoughGas, leaving the destination
// as it was and Exp not done. Every total on the success and error paths is the pre-fork one. Not
// parallel: it counts allocations
func TestVMHooksImpl_BigIntPow_ChargedBoundedBeforeTheOpFromV6(t *testing.T) {
	gasMap := bigIntArithmeticGasMap()
	fixed := gasMap["BigIntAPICost"]["BigIntPow"]
	require.NotZero(t, fixed)
	// squaring an 8000-byte base allocates at least its 16000-byte result
	const powNoOpBelow = 8000
	largeBase := repeatedBigInt(0xAB, 8000)
	over32 := repeatedBigInt(0xCD, 40)
	existingDest := repeatedBigInt(0x33, 64)

	type scenario struct {
		name string
		// dest is the destination's value before the call; nil leaves the destination handle fresh
		dest *big.Int
		a, b *big.Int
		// alias calls the hook with one missing handle as both the destination and the base
		alias bool
		// missingOperand calls the hook with the base handle missing
		missingOperand bool
		budget         func(success uint64) uint64
		failErr        error
		// gasUsed, when set, is the total of a fault that overdrew before it: the budget otherwise
		gasUsed uint64
		// destNotCreated asserts a fresh destination handle was not created
		destNotCreated bool
		noOp, op       bool
	}

	for _, fixV6 := range []bool{false, true} {
		scenarios := []scenario{
			{name: "small", a: big.NewInt(2), b: big.NewInt(10)},
			{name: "negativeBase", a: big.NewInt(-3), b: big.NewInt(3)},
			{name: "zeroToZero", a: big.NewInt(0), b: big.NewInt(0)},
			{name: "zeroToN", a: big.NewInt(0), b: big.NewInt(5)},
			{name: "zeroToOver32", a: big.NewInt(0), b: over32},
			{name: "max32BytesBase", a: repeatedBigInt(0xFF, 32), b: big.NewInt(2)},
			{name: "largeBase", a: largeBase, b: big.NewInt(2), op: true},
			{name: "largeBaseCubed", a: largeBase, b: big.NewInt(3), op: true},
			{name: "existingDest", dest: existingDest, a: largeBase, b: big.NewInt(2), op: true},
			{name: "aliasMissing", alias: true, b: big.NewInt(5)},
			{name: "missingOperand", missingOperand: true, b: big.NewInt(5),
				failErr: vmhost.ErrNoBigIntUnderThisHandle},
		}
		for _, dest := range []*big.Int{nil, existingDest} {
			suffix := ""
			if dest != nil {
				suffix = "/existingDest"
			}
			// the result-size charge was bounded on both sides already
			scenarios = append(scenarios, scenario{name: "resultShortfall" + suffix, dest: dest, a: largeBase,
				b: big.NewInt(2), budget: func(uint64) uint64 { return fixed + 16000*1000 - 1 },
				failErr: vmhost.ErrNotEnoughGas, noOp: true})
			if fixV6 {
				scenarios = append(scenarios,
					scenario{name: "fixedShortfall" + suffix, dest: dest, a: largeBase, b: big.NewInt(2),
						budget:  func(uint64) uint64 { return fixed - 1 },
						failErr: vmhost.ErrNotEnoughGas, destNotCreated: dest == nil, noOp: true},
					scenario{name: "baseCopyShortfall" + suffix, dest: dest, a: largeBase, b: big.NewInt(2),
						budget:  func(s uint64) uint64 { return s - 1 },
						failErr: vmhost.ErrNotEnoughGas, noOp: true},
					scenario{name: "exponentCopyShortfall" + suffix, dest: dest, a: big.NewInt(0), b: over32,
						budget:  func(s uint64) uint64 { return s - 1 },
						failErr: vmhost.ErrNotEnoughGas},
				)
				continue
			}
			scenarios = append(scenarios,
				// the fixed cost overdraws, then the bounded result-size charge finds no gas left. No
				// allocation check: the overdraw logs, and the fault already shows Exp was not reached
				scenario{name: "fixedShortfall" + suffix, dest: dest, a: largeBase, b: big.NewInt(2),
					budget:  func(uint64) uint64 { return fixed - 1 },
					failErr: vmhost.ErrNotEnoughGas, gasUsed: fixed},
				scenario{name: "baseCopyShortfall" + suffix, dest: dest, a: largeBase, b: big.NewInt(2),
					budget: func(s uint64) uint64 { return s - 1 }, op: true},
				scenario{name: "exponentCopyShortfall" + suffix, dest: dest, a: big.NewInt(0), b: over32,
					budget: func(s uint64) uint64 { return s - 1 }},
			)
		}

		for _, sc := range scenarios {
			t.Run(fmt.Sprintf("FixAuditChangesV6=%v/%s", fixV6, sc.name), func(t *testing.T) {
				host, runtime := newBigIntArithmeticHost(t, fixV6)
				managedType := host.ManagedTypes()

				a := sc.a
				if sc.alias || sc.missingOperand {
					a = big.NewInt(0)
				}
				destBefore := big.NewInt(0)
				destHandle := arithFreshDest
				if sc.dest != nil {
					destBefore = new(big.Int).Set(sc.dest)
					destHandle = managedType.NewBigInt(new(big.Int).Set(sc.dest))
				}
				op1Handle := arithMissing
				if sc.alias {
					destHandle = arithAliasMissing
					op1Handle = arithAliasMissing
				} else if !sc.missingOperand {
					op1Handle = managedType.NewBigInt(new(big.Int).Set(a))
				}
				op2Handle := managedType.NewBigInt(new(big.Int).Set(sc.b))

				success := fixed + powResultGas(a, sc.b) + bigIntCopyGas(a) + bigIntCopyGas(sc.b)
				budget := success + 1_000_000
				if sc.budget != nil {
					budget = sc.budget(success)
					require.Less(t, budget, success)
				}

				gasUsed, allocated := runFlatCloneHook(host, budget, func() {
					vmhooks.NewVMHooksImpl(host).BigIntPow(destHandle, op1Handle, op2Handle)
				})

				dest, destErr := managedType.GetBigInt(destHandle)
				if sc.destNotCreated {
					require.ErrorIs(t, destErr, vmhost.ErrNoBigIntUnderThisHandle, "the destination must not be created")
					dest = big.NewInt(0)
				} else {
					require.NoError(t, destErr)
				}
				if sc.failErr != nil {
					require.ErrorIs(t, runtime.FailExecutionErr, sc.failErr)
					expectedGas := budget
					if sc.gasUsed != 0 {
						expectedGas = sc.gasUsed
					}
					assert.Equal(t, expectedGas, gasUsed)
					assert.Equal(t, 0, destBefore.Cmp(dest), "the destination must be left as it was")
				} else {
					require.Nil(t, runtime.FailExecutionErr)
					// pre-fork a copy shortfall overdraws: the total is still the success one
					assert.Equal(t, success, gasUsed)
					expected := new(big.Int).Exp(a, sc.b, nil)
					assert.Equal(t, 0, expected.Cmp(dest), "the destination must hold the result")
				}
				if sc.noOp {
					assert.Less(t, allocated, uint64(powNoOpBelow), "Exp must not run")
				}
				if sc.op {
					assert.GreaterOrEqual(t, allocated, uint64(powNoOpBelow), "Exp runs")
				}
			})
		}
	}
}

type bigFloatToBigIntHook struct {
	name   string
	call   func(hooks *vmhooks.VMHooksImpl, dest, op int32)
	adjust func(op *big.Float, truncated *big.Int) *big.Int
}

func bigFloatToBigIntHooks() []bigFloatToBigIntHook {
	return []bigFloatToBigIntHook{
		{name: "BigFloatFloor",
			call: func(h *vmhooks.VMHooksImpl, d, op int32) { h.BigFloatFloor(d, op) },
			adjust: func(op *big.Float, truncated *big.Int) *big.Int {
				if !op.IsInt() && truncated.Sign() < 0 {
					return new(big.Int).Sub(truncated, big.NewInt(1))
				}
				return truncated
			}},
		{name: "BigFloatCeil",
			call: func(h *vmhooks.VMHooksImpl, d, op int32) { h.BigFloatCeil(d, op) },
			adjust: func(op *big.Float, truncated *big.Int) *big.Int {
				if !op.IsInt() && truncated.Sign() > 0 {
					return new(big.Int).Add(truncated, big.NewInt(1))
				}
				return truncated
			}},
		{name: "BigFloatTruncate",
			call: func(h *vmhooks.VMHooksImpl, d, op int32) { h.BigFloatTruncate(d, op) },
			adjust: func(_ *big.Float, truncated *big.Int) *big.Int {
				return truncated
			}},
	}
}

// mantExpFloat is mant × 2**exp at the managed big float precision
func mantExpFloat(mant float64, exp int) *big.Float {
	return new(big.Float).SetMantExp(big.NewFloat(mant), exp)
}

// BigFloatFloor, BigFloatCeil and BigFloatTruncate converted the float into the destination and then
// charged the integer part's copy unbounded. From FixAuditChangesV6 the fixed cost and that copy,
// sized from the float's exponent without converting, are bounded and charged first: an underfunded
// call fails with ErrNotEnoughGas without creating or writing the destination. The totals stay as
// they were (the copy is of the truncated value, before the floor/ceil step). Not parallel: it counts
// allocations
func TestVMHooksImpl_BigFloatToBigInt_ChargedBoundedBeforeConvertingFromV6(t *testing.T) {
	gasMap := bigIntArithmeticGasMap()
	maxExponent := mantExpFloat(0.75, 65025)
	values := []struct {
		name  string
		value *big.Float
	}{
		{name: "zero", value: big.NewFloat(0)},
		{name: "negativeZero", value: new(big.Float).Neg(big.NewFloat(0))},
		{name: "half", value: big.NewFloat(0.5)},
		{name: "negativeHalf", value: big.NewFloat(-0.5)},
		{name: "fraction", value: big.NewFloat(3.75)},
		{name: "negativeFraction", value: big.NewFloat(-3.75)},
		{name: "exactInt", value: big.NewFloat(42)},
		{name: "negativeExactInt", value: big.NewFloat(-42)},
		{name: "largeFraction", value: big.NewFloat(1<<51 + 0.5)},
		{name: "powerOfTwo256", value: mantExpFloat(0.5, 257)},
		{name: "belowPowerOfTwo256", value: mantExpFloat(1-1.0/(1<<53), 256)},
		{name: "abovePowerOfTwo256", value: mantExpFloat(0.5+1.0/(1<<53), 257)},
		{name: "large", value: mantExpFloat(0.75, 60000)},
		{name: "negativeLarge", value: mantExpFloat(-0.75, 60000)},
		{name: "maxExponent", value: maxExponent},
		{name: "negativeMaxExponent", value: new(big.Float).Neg(maxExponent)},
	}

	for _, hook := range bigFloatToBigIntHooks() {
		fixed := gasMap["BigFloatAPICost"][hook.name]
		require.NotZero(t, fixed)
		for _, fixV6 := range []bool{false, true} {
			type scenario struct {
				name    string
				value   *big.Float
				dest    *big.Int
				missing bool
				budget  func(success uint64) uint64
				failErr error
				noOp    bool
			}
			var scenarios []scenario
			for _, v := range values {
				scenarios = append(scenarios, scenario{name: v.name, value: v.value})
			}
			scenarios = append(scenarios,
				scenario{name: "existingDest", value: maxExponent, dest: repeatedBigInt(0x33, 64)},
				scenario{name: "missing", missing: true, failErr: vmhost.ErrNoBigFloatUnderThisHandle},
			)
			for _, shortfall := range []struct {
				name   string
				budget func(success uint64) uint64
			}{
				{name: "fixedShortfall", budget: func(uint64) uint64 { return fixed - 1 }},
				{name: "copyShortfall", budget: func(success uint64) uint64 { return success - 1 }},
			} {
				for _, dest := range []*big.Int{nil, repeatedBigInt(0x33, 64)} {
					sc := scenario{name: shortfall.name, value: maxExponent, dest: dest, budget: shortfall.budget}
					if dest != nil {
						sc.name += "/existingDest"
					}
					if fixV6 {
						sc.failErr = vmhost.ErrNotEnoughGas
						sc.noOp = true
					}
					scenarios = append(scenarios, sc)
				}
			}

			for _, sc := range scenarios {
				t.Run(fmt.Sprintf("%s/FixAuditChangesV6=%v/%s", hook.name, fixV6, sc.name), func(t *testing.T) {
					host, runtime := newBigIntArithmeticHost(t, fixV6)
					managedType := host.ManagedTypes()

					opHandle := arithMissing
					success := fixed
					var expected *big.Int
					if !sc.missing {
						var err error
						opHandle, err = managedType.PutBigFloat(sc.value)
						require.NoError(t, err)
						truncated, _ := sc.value.Int(nil)
						success += bigIntCopyGas(truncated)
						expected = hook.adjust(sc.value, truncated)
					}
					destHandle := arithFreshDest
					if sc.dest != nil {
						destHandle = managedType.NewBigInt(new(big.Int).Set(sc.dest))
					}
					budget := success + 1_000_000
					if sc.budget != nil {
						budget = sc.budget(success)
						require.Less(t, budget, success)
					}

					gasUsed, allocated := runFlatCloneHook(host, budget, func() {
						hook.call(vmhooks.NewVMHooksImpl(host), destHandle, opHandle)
					})

					dest, destErr := managedType.GetBigInt(destHandle)
					if sc.failErr != nil {
						require.ErrorIs(t, runtime.FailExecutionErr, sc.failErr)
						assert.Equal(t, budget, gasUsed)
						if sc.dest != nil {
							require.NoError(t, destErr)
							assert.Equal(t, 0, sc.dest.Cmp(dest), "the destination must be left as it was")
						} else if !sc.missing {
							require.ErrorIs(t, destErr, vmhost.ErrNoBigIntUnderThisHandle,
								"the destination must not be created")
						}
					} else {
						require.Nil(t, runtime.FailExecutionErr)
						assert.Equal(t, success, gasUsed)
						require.NoError(t, destErr)
						assert.Equal(t, 0, expected.Cmp(dest), "the destination must hold the result")
					}
					if sc.noOp {
						assert.Less(t, allocated, uint64(4096), "the float must not be converted")
					}
				})
			}
		}
	}
}

// BigFloatSetBigInt charged its fixed cost and the operand copy unbounded. From FixAuditChangesV6 both
// are bounded: an underfunded call fails with ErrNotEnoughGas without creating or writing the
// destination. The totals stay as they were. Not parallel: the other tests count allocations
func TestVMHooksImpl_BigFloatSetBigInt_ChargedBoundedFromV6(t *testing.T) {
	gasMap := bigIntArithmeticGasMap()
	fixed := gasMap["BigFloatAPICost"]["BigFloatSetBigInt"]
	require.NotZero(t, fixed)
	large := repeatedBigInt(0xEF, 8000)

	type scenario struct {
		name    string
		value   *big.Int
		missing bool
		budget  func(success uint64) uint64
		failErr error
	}
	for _, fixV6 := range []bool{false, true} {
		scenarios := []scenario{
			{name: "zero", value: big.NewInt(0)},
			{name: "small", value: big.NewInt(-12345)},
			{name: "over32", value: repeatedBigInt(0xEF, 40)},
			{name: "large", value: large},
			{name: "negativeLarge", value: new(big.Int).Neg(large)},
			// charged, then refused for its exponent: the fault burns the gas left on both sides
			{name: "exponentTooBig", value: repeatedBigInt(0xEF, 9000), failErr: vmhost.ErrExponentTooBigOrTooSmall},
		}
		if fixV6 {
			// pre-fork a missing handle panicked inside the copy charge
			scenarios = append(scenarios,
				scenario{name: "missing", missing: true, failErr: vmhost.ErrNoBigIntUnderThisHandle},
				scenario{name: "fixedShortfall", value: large, budget: func(uint64) uint64 { return fixed - 1 },
					failErr: vmhost.ErrNotEnoughGas},
				scenario{name: "copyShortfall", value: large, budget: func(s uint64) uint64 { return s - 1 },
					failErr: vmhost.ErrNotEnoughGas},
			)
		} else {
			scenarios = append(scenarios,
				scenario{name: "fixedShortfall", value: large, budget: func(uint64) uint64 { return fixed - 1 }},
				scenario{name: "copyShortfall", value: large, budget: func(s uint64) uint64 { return s - 1 }},
			)
		}

		for _, sc := range scenarios {
			t.Run(fmt.Sprintf("FixAuditChangesV6=%v/%s", fixV6, sc.name), func(t *testing.T) {
				host, runtime := newBigIntArithmeticHost(t, fixV6)
				managedType := host.ManagedTypes()

				opHandle := arithMissing
				success := fixed
				if !sc.missing {
					opHandle = managedType.NewBigInt(new(big.Int).Set(sc.value))
					success += bigIntCopyGas(sc.value)
				}
				budget := success + 1_000_000
				if sc.budget != nil {
					budget = sc.budget(success)
				}

				gasUsed, _ := runFlatCloneHook(host, budget, func() {
					vmhooks.NewVMHooksImpl(host).BigFloatSetBigInt(arithFreshDest, opHandle)
				})

				dest, destErr := managedType.GetBigFloat(arithFreshDest)
				if sc.failErr != nil {
					require.ErrorIs(t, runtime.FailExecutionErr, sc.failErr)
					assert.Equal(t, budget, gasUsed)
					require.ErrorIs(t, destErr, vmhost.ErrNoBigFloatUnderThisHandle, "the destination must not be created")
				} else {
					require.Nil(t, runtime.FailExecutionErr)
					assert.Equal(t, success, gasUsed)
					require.NoError(t, destErr)
					expected := big.NewFloat(0).Set(new(big.Float).SetInt(sc.value))
					assert.Equal(t, 0, expected.Cmp(dest), "the destination must hold the value")
				}
			})
		}
	}
}

// arithmeticMaxGasPerTx is the governance MaxGasPerTX at multiplier 1
const arithmeticMaxGasPerTx = uint64(400_000_000)

// randomBigInt is a seeded pseudo-random value of exactly n bytes: all-ones operands are a special
// case for division
func randomBigInt(n int) *big.Int {
	buf := make([]byte, n)
	rng := rand.New(rand.NewSource(int64(n))) // #nosec G404
	_, _ = rng.Read(buf)
	buf[0] = 0xFF
	return new(big.Int).SetBytes(buf)
}

// BenchmarkBigIntArithmetic reports, for a post-embargo schedule decision only, the time per gas the
// gasScheduleV1 prices give BigIntMul, BigIntTDiv and BigIntSqrt, up to the largest operands one
// transaction's MaxGasPerTX affords under the copy charges (a fresh destination prices 0)
func BenchmarkBigIntArithmetic(b *testing.B) {
	gasMap, err := gasschedules.LoadGasScheduleConfig(gasschedules.GetV1())
	require.NoError(b, err)
	copyPerByte := gasMap["BigIntAPICost"]["CopyPerByteForTooBig"]

	type benchHook struct {
		name string
		// operands returns the operand byte lengths for size, and for 0 the largest affordable
		operands func(size int) (int, int)
		call     func(h *vmhooks.VMHooksImpl, d, a, b int32)
	}
	affordable := func(name string) int {
		return int((arithmeticMaxGasPerTx - gasMap["BigIntAPICost"][name]) / copyPerByte) // #nosec G115
	}
	hooks := []benchHook{
		{name: "BigIntMul",
			operands: func(size int) (int, int) {
				if size == 0 {
					size = affordable("BigIntMul") / 2
				}
				return size, size
			},
			call: func(h *vmhooks.VMHooksImpl, d, a, b int32) { h.BigIntMul(d, a, b) }},
		{name: "BigIntTDiv",
			// a quotient as long as the divisor: equal lengths divide in one step
			operands: func(size int) (int, int) {
				if size == 0 {
					total := affordable("BigIntTDiv")
					return total * 2 / 3, total * 2 / 3 / 2
				}
				return size, size / 2
			},
			call: func(h *vmhooks.VMHooksImpl, d, a, b int32) { h.BigIntTDiv(d, a, b) }},
		{name: "BigIntSqrt",
			operands: func(size int) (int, int) {
				if size == 0 {
					size = affordable("BigIntSqrt")
				}
				return size, 1
			},
			call: func(h *vmhooks.VMHooksImpl, d, a, _ int32) { h.BigIntSqrt(d, a) }},
	}

	for _, hook := range hooks {
		for _, size := range []int{32, 1024, 64 * 1024, 0} {
			lenA, lenB := hook.operands(size)
			label := fmt.Sprintf("%dB", lenA)
			if size == 0 {
				label = fmt.Sprintf("max_%dB", lenA)
			}
			b.Run(fmt.Sprintf("%s/%s", hook.name, label), func(b *testing.B) {
				host, _ := newMockVMHostWithRealMetering(b, true, gasMap, 2*arithmeticMaxGasPerTx)
				managedType := host.ManagedTypes()
				aHandle := managedType.NewBigInt(randomBigInt(lenA))
				bHandle := managedType.NewBigInt(randomBigInt(lenB))
				destHandle := managedType.NewBigInt(big.NewInt(0))
				dest, err := managedType.GetBigInt(destHandle)
				require.NoError(b, err)
				hooksImpl := vmhooks.NewVMHooksImpl(host)
				runtime := host.Runtime()

				runtime.SetPointsUsed(0)
				hook.call(hooksImpl, destHandle, aHandle, bHandle)
				gas := runtime.GetPointsUsed()
				require.LessOrEqual(b, gas, arithmeticMaxGasPerTx)

				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					dest.SetInt64(0)
					runtime.SetPointsUsed(0)
					hook.call(hooksImpl, destHandle, aHandle, bHandle)
				}
				b.StopTimer()
				require.Nil(b, host.RuntimeContext.(*contextmock.RuntimeContextMock).FailExecutionErr)
				nsPerOp := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
				b.ReportMetric(float64(gas), "gas/op")
				b.ReportMetric(nsPerOp/float64(gas), "ns/gas")
			})
		}
	}
}
