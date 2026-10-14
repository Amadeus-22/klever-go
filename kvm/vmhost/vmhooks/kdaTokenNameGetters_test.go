package vmhooks_test

import (
	"bytes"
	"fmt"
	"math/big"
	"testing"

	"github.com/klever-io/klever-go/kvm/config"
	"github.com/klever-io/klever-go/kvm/executor"
	contextmock "github.com/klever-io/klever-go/kvm/mock/context"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/klever-io/klever-go/vmcommon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tokenNameGetterGasMap prices GetCallValue and DataCopyPerByte apart, so a name charged at the
// wrong rate, or not at all, shows in the total
func tokenNameGetterGasMap() config.GasScheduleMap {
	gasMap := config.MakeGasMapForTests()
	gasMap["BaseOpsAPICost"]["GetCallValue"] = 31
	gasMap["BaseOperationCost"]["DataCopyPerByte"] = 50
	return gasMap
}

const (
	tokenNameGetterNamePtr  = executor.MemPtr(0)
	tokenNameGetterValuePtr = executor.MemPtr(2048)
)

var (
	// the calling contract chooses the names of the transfers it sends, so one can be far longer
	// than any real ticker
	tokenNameGetterName     = bytes.Repeat([]byte{'N'}, 1024)
	tokenNameGetterValue    = big.NewInt(123_456_789)
	tokenNameGetterSentinel = bytes.Repeat([]byte{0xEE}, len(tokenNameGetterName))
)

// tokenNameGetterCase is one legacy hook that copies a call value transfer's token name out to
// contract memory
type tokenNameGetterCase struct {
	name string
	call func(hooks *vmhooks.VMHooksImpl) int32
	// writesValue marks a hook that also writes the padded call value at tokenNameGetterValuePtr
	writesValue bool
}

// Before FixAuditChangesV6 the legacy token name getters charge a flat GetCallValue, unbounded,
// whatever the length of the name they copy out. From it the flat cost is bounded and the name is
// paid for, DataCopyPerByte a byte and bounded, before it is copied, so a budget one short of either
// faults with ErrNotEnoughGas and leaves contract memory as it was. Pre-fork every total and every
// effect must stay as it was
func TestVMHooksImpl_TokenNameGetters_ChargeNameFromV6(t *testing.T) {
	t.Parallel()

	const plentyOfGas = uint64(100_000_000)
	gasMap := tokenNameGetterGasMap()
	fixed := gasMap["BaseOpsAPICost"]["GetCallValue"]
	nameGas := gasMap["BaseOperationCost"]["DataCopyPerByte"] * uint64(len(tokenNameGetterName))

	hooks := []tokenNameGetterCase{
		{"GetKDATokenName", func(hooks *vmhooks.VMHooksImpl) int32 {
			return hooks.GetKDATokenName(tokenNameGetterNamePtr)
		}, false},
		{"GetKDATokenNameByIndex", func(hooks *vmhooks.VMHooksImpl) int32 {
			return hooks.GetKDATokenNameByIndex(tokenNameGetterNamePtr, 0)
		}, false},
		{"GetCallValueTokenName", func(hooks *vmhooks.VMHooksImpl) int32 {
			return hooks.GetCallValueTokenName(tokenNameGetterValuePtr, tokenNameGetterNamePtr)
		}, true},
		{"GetCallValueTokenNameByIndex", func(hooks *vmhooks.VMHooksImpl) int32 {
			return hooks.GetCallValueTokenNameByIndex(tokenNameGetterValuePtr, tokenNameGetterNamePtr, 0)
		}, true},
	}

	type scenario struct {
		name   string
		budget uint64
		// failErr is the fault expected, nil for none
		failErr error
		// pointsUsed is checked when failErr is nil; a fault drains the gas left
		pointsUsed uint64
		copied     bool
	}

	for _, hc := range hooks {
		for _, fixV6 := range []bool{false, true} {
			scenarios := []scenario{
				// pre-fork the flat cost overdraws and the name is copied for free, so neither
				// shortfall stops it
				{"funded", plentyOfGas, nil, fixed, true},
				{"fixedShortfall", fixed - 1, nil, fixed, true},
				{"nameShortfall", fixed + nameGas - 1, nil, fixed, true},
			}
			if fixV6 {
				scenarios = []scenario{
					{"funded", plentyOfGas, nil, fixed + nameGas, true},
					{"fixedShortfall", fixed - 1, vmhost.ErrNotEnoughGas, 0, false},
					{"nameShortfall", fixed + nameGas - 1, vmhost.ErrNotEnoughGas, 0, false},
				}
			}

			for _, sc := range scenarios {
				t.Run(fmt.Sprintf("%s/FixAuditChangesV6=%v/%s", hc.name, fixV6, sc.name), func(t *testing.T) {
					host, _ := newMockVMHostWithRealMetering(t, fixV6, gasMap, sc.budget)
					runtime := host.RuntimeContext.(*contextmock.RuntimeContextMock)
					runtime.VMInput = &vmcommon.ContractCallInput{VMInput: vmcommon.VMInput{
						KDATransfers: []*vmcommon.KDATransfer{{
							KDATokenName: tokenNameGetterName,
							KDAValue:     tokenNameGetterValue,
						}},
					}}
					require.NoError(t, host.MemStoreToMock(tokenNameGetterNamePtr, tokenNameGetterSentinel))
					require.NoError(t, host.MemStoreToMock(tokenNameGetterValuePtr, tokenNameGetterSentinel[:vmhost.BalanceLen]))

					result := hc.call(vmhooks.NewVMHooksImpl(host))

					if sc.failErr != nil {
						require.ErrorIs(t, runtime.FailExecutionErr, sc.failErr)
						assert.Equal(t, sc.budget, runtime.GetPointsUsed(), "a fault burns the gas left")
					} else {
						require.Nil(t, runtime.FailExecutionErr)
						assert.Equal(t, sc.pointsUsed, runtime.GetPointsUsed())
					}

					storedName, err := host.MemLoadFromMock(tokenNameGetterNamePtr, len(tokenNameGetterSentinel))
					require.NoError(t, err)
					storedValue, err := host.MemLoadFromMock(tokenNameGetterValuePtr, vmhost.BalanceLen)
					require.NoError(t, err)

					if !sc.copied {
						assert.Equal(t, int32(-1), result)
						assert.Equal(t, tokenNameGetterSentinel, storedName, "name copied unpaid")
						assert.Equal(t, tokenNameGetterSentinel[:vmhost.BalanceLen], storedValue, "call value written unpaid")
						return
					}

					assert.Equal(t, int32(len(tokenNameGetterName)), result) // #nosec G115
					assert.Equal(t, tokenNameGetterName, storedName)
					if hc.writesValue {
						assert.Equal(t, vmhost.PadBytesLeft(tokenNameGetterValue.Bytes(), vmhost.BalanceLen), storedValue)
					} else {
						assert.Equal(t, tokenNameGetterSentinel[:vmhost.BalanceLen], storedValue)
					}
				})
			}
		}
	}
}
