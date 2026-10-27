package vmhooks_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ManagedSignalError cloned the message, then charged DataCopyPerByte for it unbounded and
// PersistPerByte bounded. From FixAuditChangesV6 the flat cost and both per-byte charges are bounded
// and come before the clone: an underfunded call fails execution with ErrNotEnoughGas, as it
// already did pre-fork once the persist charge found the gas overdrawn, but without cloning the
// message. The totals stay as they were. Not parallel: it counts allocations
func TestVMHooksImpl_ManagedSignalError_ChargedBeforeTheCloneFromV6(t *testing.T) {
	gasMap := perByteHookGasMap()
	fixedCost := gasMap["BaseOpsAPICost"]["SignalError"]
	copyPerByte := gasMap["BaseOperationCost"]["DataCopyPerByte"]
	persistPerByte := gasMap["BaseOperationCost"]["PersistPerByte"]
	oversized := bytes.Repeat([]byte{'e'}, flatCloneOversizedLen)
	success := func(message []byte) uint64 {
		return fixedCost + (copyPerByte+persistPerByte)*uint64(len(message))
	}

	type scenario struct {
		name      string
		message   []byte
		missing   bool
		budget    uint64
		failErr   error
		cloned    bool
		notCloned bool
		// preset is the signalled message before the call, to tell an empty message signalled from none
		preset string
	}

	for _, fixV6 := range []bool{false, true} {
		scenarios := []scenario{
			{name: "empty", message: []byte{}},
			{name: "small", message: []byte("user error")},
			{name: "oversized", message: oversized, cloned: true},
			{name: "missing", missing: true, budget: fixedCost, failErr: vmhost.ErrNoManagedBufferUnderThisHandle},
		}
		if fixV6 {
			scenarios = append(scenarios,
				// the flat cost is bounded, so an empty message fails execution too
				scenario{name: "emptyFixedShortfall", message: []byte{}, budget: fixedCost - 1,
					failErr: vmhost.ErrNotEnoughGas, preset: "preset"},
				scenario{name: "fixedShortfall", message: oversized, budget: fixedCost - 1,
					failErr: vmhost.ErrNotEnoughGas, notCloned: true},
				scenario{name: "copyShortfall", message: oversized, budget: fixedCost + copyPerByte*uint64(len(oversized)) - 1,
					failErr: vmhost.ErrNotEnoughGas, notCloned: true},
				scenario{name: "persistShortfall", message: oversized, budget: success(oversized) - 1,
					failErr: vmhost.ErrNotEnoughGas, notCloned: true},
			)
		} else {
			// pre-fork the flat cost and the copy overdraw, and the bounded persist charge then
			// finds no gas left, after the clone
			scenarios = append(scenarios,
				// an empty message persists nothing, so the overdrawn flat cost is all it takes and the
				// empty message is signalled
				scenario{name: "emptyFixedShortfall", message: []byte{}, budget: fixedCost - 1, preset: "preset"},
				scenario{name: "fixedShortfall", message: oversized, budget: fixedCost - 1,
					failErr: vmhost.ErrNotEnoughGas, cloned: true},
				scenario{name: "copyShortfall", message: oversized, budget: fixedCost + copyPerByte*uint64(len(oversized)) - 1,
					failErr: vmhost.ErrNotEnoughGas, cloned: true},
				scenario{name: "persistShortfall", message: oversized, budget: success(oversized) - 1,
					failErr: vmhost.ErrNotEnoughGas, cloned: true},
			)
		}

		for _, sc := range scenarios {
			t.Run(fmt.Sprintf("FixAuditChangesV6=%v/%s", fixV6, sc.name), func(t *testing.T) {
				host, runtime, _ := newPerByteHost(t, fixV6)
				handle := int32(1234)
				if !sc.missing {
					handle = host.ManagedTypes().NewManagedBufferFromBytes(sc.message)
				}
				budget := sc.budget
				if budget == 0 {
					budget = success(oversized) + 1_000_000
				}
				runtime.SignalErrorMessage = sc.preset

				gasUsed, allocated := runFlatCloneHook(host, budget, func() {
					vmhooks.NewVMHooksImpl(host).ManagedSignalError(handle)
				})

				if sc.failErr != nil {
					require.ErrorIs(t, runtime.FailExecutionErr, sc.failErr)
					assert.Equal(t, sc.preset, runtime.SignalErrorMessage, "the contract's message is not signalled")
				} else {
					require.Nil(t, runtime.FailExecutionErr)
					assert.Equal(t, success(sc.message), gasUsed)
					assert.Equal(t, string(sc.message), runtime.SignalErrorMessage)
				}
				if sc.cloned {
					assert.GreaterOrEqual(t, allocated, uint64(flatCloneOversizedLen))
				}
				if sc.notCloned {
					assert.Less(t, allocated, uint64(flatCloneNoCloneBelow), "the message must not be cloned")
				}
			})
		}
	}
}
