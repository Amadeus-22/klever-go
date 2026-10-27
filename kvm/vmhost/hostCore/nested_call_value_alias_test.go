package hostCore_test

import (
	"math/big"
	"testing"

	blockchainConfig "github.com/klever-io/klever-go/config"
	mock "github.com/klever-io/klever-go/kvm/mock/context"
	worldmock "github.com/klever-io/klever-go/kvm/mock/world"
	test "github.com/klever-io/klever-go/kvm/testcommon"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/klever-io/klever-go/vmcommon"
	"github.com/stretchr/testify/require"
)

// A nested call with value takes that value straight from a managed big int handle of the caller
// (readDestinationValueArguments), and output.TransferValueOnly records it as the child's output
// transfer, which later becomes the grandparent's back-transfer. Once FixAuditChangesV6 hands the
// caller its own big ints back after the call instead of clones (KLC-2678), nothing the caller does
// with that handle afterwards may change the amount recorded for a transfer that already settled.
func Test_NestedCallValue_CallerHandleMutationLeavesRecordedTransfer(t *testing.T) {
	const sentAmount = int64(1)
	const rewrittenAmount = int64(1_000_000_000)

	run := func(t *testing.T, epochs blockchainConfig.EnableEpochs) {
		var returnCode int32
		var recordedValues []*big.Int

		callerBody := func(host vmhost.VMHost) {
			managedTypes := host.ManagedTypes()
			valueHandle := managedTypes.NewBigIntFromInt64(sentAmount)
			// the pointer readDestinationValueArguments passes on, not a copy
			value, err := managedTypes.GetBigInt(valueHandle)
			require.NoError(t, err)

			returnCode = vmhooks.ExecuteOnDestContextWithTypedArgs(
				host,
				noRollbackFollowUpGas,
				value,
				[]byte(noRollbackAcceptMethod),
				test.ChildAddress,
				nil,
			)

			managedTypes.GetBigIntOrCreate(valueHandle).SetInt64(rewrittenAmount)

			childAccount, _ := host.Output().GetOutputAccount(test.ChildAddress)
			for _, transfer := range childAccount.OutputTransfers {
				recordedValues = append(recordedValues, big.NewInt(0).Set(transfer.KDATransfers.KDAValue))
			}
		}

		testConfig := &test.TestConfig{
			GasProvided:   2000,
			ParentBalance: 1000,
			ChildBalance:  1000,
		}
		_, err := test.BuildMockInstanceCallTest(t).
			WithEnableEpochs(epochs).
			WithContracts(
				test.CreateMockContract(test.ParentAddress).
					WithBalance(testConfig.ParentBalance).
					WithConfig(testConfig).
					WithMethods(func(instance *mock.InstanceMock, _ interface{}) {
						instance.AddMockMethod(noRollbackCallerFunction, func() *mock.InstanceMock {
							callerBody(instance.Host)
							return instance
						})
					}),
				test.CreateMockContract(test.ChildAddress).
					WithBalance(testConfig.ChildBalance).
					WithConfig(testConfig).
					WithMethods(childMethods(t)),
			).
			WithInput(test.CreateTestContractCallInputBuilder().
				WithRecipientAddr(test.ParentAddress).
				WithGasProvided(testConfig.GasProvided).
				WithFunction(noRollbackCallerFunction).
				Build()).
			WithSetup(func(host vmhost.VMHost, mockWorld *worldmock.MockWorld) {
				setupNoRollbackHost(t, host, mockWorld)
			}).
			AndAssertResults(func(_ *worldmock.MockWorld, verify *test.VMOutputVerifier) {
				require.Equal(t, vmcommon.Ok, verify.VmOutput.ReturnCode)
			})
		require.NoError(t, err)

		require.Equal(t, int32(0), returnCode)
		require.Equal(t, []*big.Int{big.NewInt(sentAmount)}, recordedValues,
			"the caller rewrote a settled transfer through its value handle")
	}

	t.Run("before FixAuditChangesV6", func(t *testing.T) {
		run(t, blockchainConfig.EnableEpochs{FixAuditChangesV6: 1_000_000})
	})
	t.Run("from FixAuditChangesV6", func(t *testing.T) {
		run(t, blockchainConfig.EnableEpochs{})
	})
}

// The end-to-end form of the forgery the test above guards against: the child pays its caller
// back through a nested call whose value is one of the child's handles, then rewrites that handle
// and returns. The caller's back-transfer must still be the amount that moved.
func Test_NestedCallValue_ChildHandleMutationCannotForgeBackTransfer(t *testing.T) {
	const paidBack = int64(1)
	const forgedAmount = int64(1_000_000_000)
	const payBackAndRewriteMethod = "payBackAndRewrite"
	const innerCallGas = int64(200)

	run := func(t *testing.T, epochs blockchainConfig.EnableEpochs) {
		var returnCode int32
		var backTransferValue *big.Int

		testConfig := &test.TestConfig{
			GasProvided:   2000,
			ParentBalance: 1000,
			ChildBalance:  1000,
		}
		_, err := test.BuildMockInstanceCallTest(t).
			WithEnableEpochs(epochs).
			WithContracts(
				test.CreateMockContract(test.ParentAddress).
					WithBalance(testConfig.ParentBalance).
					WithConfig(testConfig).
					WithMethods(func(instance *mock.InstanceMock, _ interface{}) {
						instance.AddMockMethod(noRollbackCallerFunction, func() *mock.InstanceMock {
							host := instance.Host
							returnCode = vmhooks.ExecuteOnDestContextWithTypedArgs(
								host,
								noRollbackFollowUpGas,
								big.NewInt(0),
								[]byte(payBackAndRewriteMethod),
								test.ChildAddress,
								nil,
							)
							_, backTransferValue = host.ManagedTypes().GetBackTransfers()
							return instance
						})
						instance.AddMockMethod(noRollbackAcceptMethod, func() *mock.InstanceMock {
							return instance
						})
					}),
				test.CreateMockContract(test.ChildAddress).
					WithBalance(testConfig.ChildBalance).
					WithConfig(testConfig).
					WithMethods(func(instance *mock.InstanceMock, _ interface{}) {
						instance.AddMockMethod(payBackAndRewriteMethod, func() *mock.InstanceMock {
							host := instance.Host
							managedTypes := host.ManagedTypes()
							valueHandle := managedTypes.NewBigIntFromInt64(paidBack)
							value, err := managedTypes.GetBigInt(valueHandle)
							require.NoError(t, err)

							innerReturnCode := vmhooks.ExecuteOnDestContextWithTypedArgs(
								host,
								innerCallGas,
								value,
								[]byte(noRollbackAcceptMethod),
								test.ParentAddress,
								nil,
							)
							require.Equal(t, int32(0), innerReturnCode)

							managedTypes.GetBigIntOrCreate(valueHandle).SetInt64(forgedAmount)
							return instance
						})
					}),
			).
			WithInput(test.CreateTestContractCallInputBuilder().
				WithRecipientAddr(test.ParentAddress).
				WithGasProvided(testConfig.GasProvided).
				WithFunction(noRollbackCallerFunction).
				Build()).
			WithSetup(func(host vmhost.VMHost, mockWorld *worldmock.MockWorld) {
				setupNoRollbackHost(t, host, mockWorld)
			}).
			AndAssertResults(func(_ *worldmock.MockWorld, verify *test.VMOutputVerifier) {
				require.Equal(t, vmcommon.Ok, verify.VmOutput.ReturnCode)
			})
		require.NoError(t, err)

		require.Equal(t, int32(0), returnCode)
		require.Equal(t, big.NewInt(paidBack), backTransferValue,
			"the child forged its caller's back-transfer by rewriting its value handle")
	}

	t.Run("before FixAuditChangesV6", func(t *testing.T) {
		run(t, blockchainConfig.EnableEpochs{FixAuditChangesV6: 1_000_000})
	})
	t.Run("from FixAuditChangesV6", func(t *testing.T) {
		run(t, blockchainConfig.EnableEpochs{})
	})
}
