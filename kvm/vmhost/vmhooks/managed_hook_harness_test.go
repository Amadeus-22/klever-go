package vmhooks_test

import (
	"runtime"
	"testing"

	blockchainConfig "github.com/klever-io/klever-go/config"
	mock "github.com/klever-io/klever-go/kvm/mock/context"
	"github.com/klever-io/klever-go/kvm/mock/contracts"
	worldmock "github.com/klever-io/klever-go/kvm/mock/world"
	test "github.com/klever-io/klever-go/kvm/testcommon"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/vmcommon"
	"github.com/stretchr/testify/require"
)

// The harness the FixAuditChangesV6 decode tests share: one hook call made from a contract, on
// both sides of the fork, measured on its own.

const mainnetDataCopyPerByte = 50

// managedHookChildFunction is the endpoint of the child contract the calling hooks target.
const managedHookChildFunction = "deposit"

func preForkV6EnableEpochs() blockchainConfig.EnableEpochs {
	return blockchainConfig.EnableEpochs{FixAuditChangesV5: 0, FixAuditChangesV6: 1_000_000}
}

func postForkV6EnableEpochs() blockchainConfig.EnableEpochs {
	return blockchainConfig.EnableEpochs{FixAuditChangesV5: 0, FixAuditChangesV6: 0}
}

// managedHookRun is what one hook call did, plus how the whole execution ended.
type managedHookRun struct {
	gasUsed       uint64
	allocated     uint64
	returnCode    vmcommon.ReturnCode
	returnMessage string
}

// runManagedHook runs one hook call from the parent contract with gasLeftBeforeHook gas left, on
// the mainnet DataCopyPerByte, and measures the call alone. prepare builds the buffers the hook
// reads, directly in the managed types context and so without gas, and returns the call. The
// parent holds test.KDATestTokenName to pay with; the child exposes managedHookChildFunction,
// init and upgrade, is owned by the parent and is upgradeable, so every calling hook has a target.
func runManagedHook(
	t *testing.T,
	epochs blockchainConfig.EnableEpochs,
	gasLeftBeforeHook uint64,
	prepare func(host vmhost.VMHost) func(),
) managedHookRun {
	var run managedHookRun

	_, err := test.BuildMockInstanceCallTest(t).
		WithEnableEpochs(epochs).
		WithContracts(
			test.CreateMockContract(test.ParentAddress).
				WithBalance(1000).
				WithMethods(func(parentInstance *mock.InstanceMock, _ any) {
					parentInstance.AddMockMethod("callHook", func() *mock.InstanceMock {
						host := parentInstance.Host
						metering := host.Metering()
						metering.GasSchedule().BaseOperationCost.DataCopyPerByte = mainnetDataCopyPerByte

						callHook := prepare(host)

						metering.UseGas(metering.GasLeft() - gasLeftBeforeHook)
						pointsBefore := host.Runtime().GetPointsUsed()

						var before, after runtime.MemStats
						runtime.GC()
						runtime.ReadMemStats(&before)

						callHook()

						runtime.ReadMemStats(&after)
						run.allocated = after.TotalAlloc - before.TotalAlloc
						run.gasUsed = host.Runtime().GetPointsUsed() - pointsBefore

						return parentInstance
					})
				}),
			test.CreateMockContract(test.ChildAddress).
				WithBalance(1000).
				WithOwnerAddress(test.ParentAddress).
				WithCodeMetadata([]byte{vmcommon.MetadataUpgradeable, 0}).
				WithMethods(
					contracts.InitFunctionMock,
					contracts.UpgradeFunctionMock,
					func(childInstance *mock.InstanceMock, _ any) {
						childInstance.AddMockMethod(managedHookChildFunction, func() *mock.InstanceMock { return childInstance })
					}),
		).
		WithSetup(func(host vmhost.VMHost, world *worldmock.MockWorld) {
			require.NoError(t, world.InitBuiltinFunctions(host.GetGasScheduleMap(), host.ForkController()))
			caller, err := world.AccountsCacher.LoadUser(test.ParentAddress)
			require.NoError(t, err)
			require.NoError(t, caller.AddToBalance(1000, test.KDATestTokenName, true))
		}).
		WithInput(test.CreateTestContractCallInputBuilder().
			WithRecipientAddr(test.ParentAddress).
			WithGasProvided(gasLeftBeforeHook + 1_000_000).
			WithFunction("callHook").
			Build()).
		AndAssertResults(func(_ *worldmock.MockWorld, verify *test.VMOutputVerifier) {
			run.returnCode = verify.VmOutput.ReturnCode
			run.returnMessage = verify.VmOutput.ReturnMessage
		})
	require.NoError(t, err)

	return run
}
