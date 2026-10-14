package vmhooks_test

import (
	"fmt"
	"testing"

	"github.com/klever-io/klever-go/kvm/config"
	contextmock "github.com/klever-io/klever-go/kvm/mock/context"
	"github.com/klever-io/klever-go/kvm/vmhost"
	hostmock "github.com/klever-io/klever-go/kvm/vmhost/mock"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// managedUpgradeHook is one of the two managed upgrade hooks, with the buffers it clones out of the
// contract's managed types in the order it clones them.
type managedUpgradeHook struct {
	name string
	// buffers are the handles the hook clones, in order
	buffers []int32
	// charged are the buffers the hook pays DataCopyPerByte for post-fork, in order; the last one is
	// the one a budget one unit short of the whole charge must refuse
	charged []int32
	call    func(hooks *vmhooks.VMHooksImpl, missing int32)
}

// Before FixAuditChangesV6 the managed upgrade hooks cloned the source address, the code metadata and
// the code for free, of whatever size the contract made them. From the fork they pay DataCopyPerByte
// for the code metadata and the code, bounded and before the clone, as the managed deploy hooks pay
// for theirs, and reject a source address longer than AddressLen, as ManagedDeployFromSourceContract
// does. The code is priced as a copy, not compiled: the nested upgrade call charges CompilePerByte
// for it itself, in DeductInitialGasForIndirectDeployment.
func TestVMHooksImpl_ManagedUpgrade_BufferCopyGas(t *testing.T) {
	t.Parallel()

	const (
		destHandle    = int32(1)
		valueHandle   = int32(2)
		codeHandle    = int32(3)
		metaHandle    = int32(4)
		sourceHandle  = int32(5)
		argsHandle    = int32(6)
		resultHandle  = int32(7)
		missingHandle = int32(99)
		// oversizedSourceHandle holds a source address one byte longer than AddressLen; passing it
		// as "missing" makes the from-source hook read it in place of the source
		oversizedSourceHandle = int32(8)
	)
	// distinct costs, so a charge at the wrong rate (CompilePerByte for the code, say) or of the wrong
	// fixed cost shows up in the totals
	gasMap := config.MakeGasMapForTests()
	gasMap["BaseOperationCost"]["DataCopyPerByte"] = 7
	gasMap["BaseOperationCost"]["CompilePerByte"] = 13
	gasMap["BaseOpsAPICost"]["CreateContract"] = 101
	createCost := gasMap["BaseOpsAPICost"]["CreateContract"]
	dataCopyPerByte := gasMap["BaseOperationCost"]["DataCopyPerByte"]
	// enough for upgradeContract to pass its minCallCost check and reach the nested call, so a hook
	// that did not stop at its charge is caught calling ExecuteOnDestContext
	childGas := int64(2 * gasMap["BaseOpsAPICost"]["ExecuteOnDestContext"])

	destination := []byte("destinationContractAddress______")
	source := []byte("sourceContractAddress___________")
	code := []byte("the new contract code, longer than the rest")
	codeMetadata := []byte{0, 0}
	sourceCode := []byte("code served for the source address")
	contents := map[int32][]byte{sourceHandle: source, metaHandle: codeMetadata, codeHandle: code}

	pick := func(handle int32, missing int32) int32 {
		switch {
		case handle == missing:
			return missingHandle
		case handle == sourceHandle && missing == oversizedSourceHandle:
			return oversizedSourceHandle
		}
		return handle
	}
	hooksToTest := []managedUpgradeHook{
		{"ManagedUpgradeContract", []int32{metaHandle, codeHandle}, []int32{metaHandle, codeHandle}, func(hooks *vmhooks.VMHooksImpl, missing int32) {
			hooks.ManagedUpgradeContract(pick(destHandle, missing), childGas, valueHandle,
				pick(codeHandle, missing), pick(metaHandle, missing), argsHandle, resultHandle)
		}},
		{"ManagedUpgradeFromSourceContract", []int32{sourceHandle, metaHandle}, []int32{metaHandle}, func(hooks *vmhooks.VMHooksImpl, missing int32) {
			hooks.ManagedUpgradeFromSourceContract(pick(destHandle, missing), childGas, valueHandle,
				pick(sourceHandle, missing), pick(metaHandle, missing), argsHandle, resultHandle)
		}},
	}

	type outcome struct {
		host     *contextmock.VMHostMock
		gasUsed  uint64
		failErr  error
		getCodes int
	}
	run := func(t *testing.T, hook managedUpgradeHook, fixAuditChangesV6 bool, gasProvided uint64, missing int32) outcome {
		host, metering := newMockVMHostWithRealMetering(t, fixAuditChangesV6, gasMap, gasProvided)
		getCodes := 0
		host.BlockchainContext.(*hostmock.BlockchainContextStub).GetCodeCalled = func(_ []byte) ([]byte, error) {
			getCodes++
			return sourceCode, nil
		}
		// seeding buffers and handles charges nothing; an empty argument vector decodes for free
		managedTypes := host.ManagedTypesContext
		managedTypes.SetBytes(destHandle, destination)
		managedTypes.GetBigIntOrCreate(valueHandle)
		managedTypes.SetBytes(codeHandle, code)
		managedTypes.SetBytes(metaHandle, codeMetadata)
		managedTypes.SetBytes(sourceHandle, source)
		managedTypes.SetBytes(oversizedSourceHandle, append(append([]byte{}, source...), 0))
		managedTypes.SetBytes(argsHandle, nil)
		managedTypes.SetBytes(resultHandle, nil)

		hook.call(vmhooks.NewVMHooksImpl(host), missing)

		return outcome{
			host:     host,
			gasUsed:  gasProvided - metering.GasLeft(),
			failErr:  host.RuntimeContext.(*contextmock.RuntimeContextMock).FailExecutionErr,
			getCodes: getCodes,
		}
	}

	for _, hook := range hooksToTest {
		var copiedBytes uint64
		for _, handle := range hook.charged {
			copiedBytes += uint64(len(contents[handle]))
		}

		for _, fixAuditChangesV6 := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/FixAuditChangesV6=%v/funded", hook.name, fixAuditChangesV6), func(t *testing.T) {
				t.Parallel()

				out := run(t, hook, fixAuditChangesV6, 1000, 0)

				require.Nil(t, out.failErr)
				// pre-fork the buffers were cloned for free, which blocks from before the fork replay on
				expectedGas := createCost
				if fixAuditChangesV6 {
					expectedGas += dataCopyPerByte * copiedBytes
				}
				assert.Equal(t, expectedGas, out.gasUsed)

				require.Len(t, out.host.StoredInputs, 1, "the upgrade must reach the nested call")
				arguments := out.host.StoredInputs[0].Arguments
				require.GreaterOrEqual(t, len(arguments), 2)
				assert.Equal(t, codeMetadata, arguments[1])
				if hook.name == "ManagedUpgradeContract" {
					assert.Equal(t, code, arguments[0])
				} else {
					assert.Equal(t, sourceCode, arguments[0])
					assert.Equal(t, 1, out.getCodes)
				}
			})

			// a missing buffer fails the same way on both sides of the fork, before the upgrade
			for _, missing := range hook.buffers {
				t.Run(fmt.Sprintf("%s/FixAuditChangesV6=%v/missing=%d", hook.name, fixAuditChangesV6, missing), func(t *testing.T) {
					t.Parallel()

					out := run(t, hook, fixAuditChangesV6, 1000, missing)

					require.ErrorIs(t, out.failErr, vmhost.ErrNoManagedBufferUnderThisHandle)
					assert.Empty(t, out.host.StoredInputs, "no upgrade for a missing buffer")
					assert.Zero(t, out.getCodes)
				})
			}

			// a source address longer than AddressLen is rejected from the fork before it is cloned,
			// as ManagedDeployFromSourceContract rejects its own; before it, it was read as given
			if hook.name == "ManagedUpgradeFromSourceContract" {
				t.Run(fmt.Sprintf("%s/FixAuditChangesV6=%v/oversized-source", hook.name, fixAuditChangesV6), func(t *testing.T) {
					t.Parallel()

					out := run(t, hook, fixAuditChangesV6, 1000, oversizedSourceHandle)

					if fixAuditChangesV6 {
						require.ErrorIs(t, out.failErr, vmhost.ErrInvalidArgument)
						assert.Empty(t, out.host.StoredInputs)
						assert.Zero(t, out.getCodes)
						return
					}
					require.Nil(t, out.failErr)
					assert.Equal(t, 1, out.getCodes)
					assert.Equal(t, createCost, out.gasUsed)
				})
			}
		}

		// a budget one unit short of the fixed cost: from the fork it is refused before anything is
		// read, so a missing destination is never reached; before it the charge overdraws and the
		// hook goes on to read the destination, as it always did
		for _, fixAuditChangesV6 := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/FixAuditChangesV6=%v/fixed-cost-shortfall", hook.name, fixAuditChangesV6), func(t *testing.T) {
				t.Parallel()

				out := run(t, hook, fixAuditChangesV6, createCost-1, destHandle)

				if fixAuditChangesV6 {
					require.ErrorIs(t, out.failErr, vmhost.ErrNotEnoughGas)
				} else {
					require.Error(t, out.failErr)
					require.NotErrorIs(t, out.failErr, vmhost.ErrNotEnoughGas)
				}
				assert.Empty(t, out.host.StoredInputs)
				assert.Zero(t, out.getCodes)
			})
		}

		// from the fork a budget that pays the fixed cost and the buffers before one of them, and all
		// but one unit of that one, is refused at that buffer, before it is cloned and before anything
		// is upgraded. Pre-fork there was no charge to refuse, which the funded rows pin
		var paidBytes uint64
		for _, refused := range hook.charged {
			paidBytes += uint64(len(contents[refused]))
			gasProvided := createCost + dataCopyPerByte*paidBytes - 1

			t.Run(fmt.Sprintf("%s/FixAuditChangesV6=true/shortfall=%d", hook.name, refused), func(t *testing.T) {
				t.Parallel()

				out := run(t, hook, true, gasProvided, 0)

				require.ErrorIs(t, out.failErr, vmhost.ErrNotEnoughGas)
				assert.Empty(t, out.host.StoredInputs, "nothing may be upgraded for buffers that were not paid for")
				assert.Zero(t, out.getCodes, "the source code may not be read for buffers that were not paid for")
			})
		}
	}
}
