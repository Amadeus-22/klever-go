package hostCore_test

import (
	"math/big"
	"testing"

	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/core/process/kda/kdautils"
	worldmock "github.com/klever-io/klever-go/kvm/mock/world"
	test "github.com/klever-io/klever-go/kvm/testcommon"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/klever-io/klever-go/vmcommon"
	"github.com/stretchr/testify/require"
)

// TransferKDANFTExecuteWithTypedArgs records its transfer in the caller's output state before the
// follow-up call runs. When the transfer or the follow-up fails, that record has to go with it
// (KLC-2687): the output state is restored as if the call never happened, and the settled transfer
// itself is undone by the transaction reset (KLC-2686). Nothing reads the discarded record today,
// because the failure aborts the call, but a contract must never be able to observe it.

// recordTransferReceipts makes the mock KleverTransfer built-in report its transfers as output
// transfers on the recipient, the way BlockChainHookImpl.ProcessBuiltInFunction rebuilds them
// from the kapps receipts in production. The mock only moves the balances.
func recordTransferReceipts(t *testing.T, world *worldmock.MockWorld) {
	container := world.BuiltinFuncs.Container
	transferFunction, err := container.Get(core.BuiltInFunctionTransfer)
	require.NoError(t, err)

	err = container.Replace(core.BuiltInFunctionTransfer, &test.MockBuiltin{
		ProcessBuiltinFunctionCall: func(input *vmcommon.ContractCallInput) (*vmcommon.VMOutput, error) {
			vmOutput, err := transferFunction.ProcessBuiltinFunction(input)
			if err != nil {
				return vmOutput, err
			}

			if vmOutput.OutputAccounts == nil {
				vmOutput.OutputAccounts = make(map[string]*vmcommon.OutputAccount)
			}
			recipient, ok := vmOutput.OutputAccounts[string(input.RecipientAddr)]
			if !ok {
				recipient = &vmcommon.OutputAccount{Address: input.RecipientAddr}
				vmOutput.OutputAccounts[string(input.RecipientAddr)] = recipient
			}

			for index, transfer := range input.KDATransfers {
				recipient.OutputTransfers = append(recipient.OutputTransfers, vmcommon.OutputTransfer{
					Index:         uint32(index + 1), // #nosec G115 - a handful of test transfers; indexes are 1-based
					SenderAddress: input.CallerAddr,
					RcvAddr:       input.RecipientAddr,
					KDATransfers:  transfer.Clone(),
				})
			}

			return vmOutput, nil
		},
	})
	require.NoError(t, err)
}

// outputStateAfterCall is what the parent sees in its own output state once the hook returned.
type outputStateAfterCall struct {
	childTransfers    []vmcommon.OutputTransfer
	backTransfers     []*vmcommon.KDATransfer
	backTransferValue *big.Int
	returnCode        vmcommon.ReturnCode
	returnMessage     string
}

func captureOutputState(host vmhost.VMHost) outputStateAfterCall {
	childAccount, _ := host.Output().GetOutputAccount(test.ChildAddress)
	backTransfers, backTransferValue := host.ManagedTypes().GetBackTransfers()

	return outputStateAfterCall{
		childTransfers:    append([]vmcommon.OutputTransfer(nil), childAccount.OutputTransfers...),
		backTransfers:     backTransfers,
		backTransferValue: backTransferValue,
		returnCode:        host.Output().ReturnCode(),
		returnMessage:     host.Output().ReturnMessage(),
	}
}

func transferToChildAndCall(host vmhost.VMHost, transfers []*vmcommon.KDATransfer, method string) int32 {
	return vmhooks.TransferKDANFTExecuteWithTypedArgs(
		host,
		test.ChildAddress,
		transfers,
		noRollbackFollowUpGas,
		[]byte(method),
		nil,
	)
}

func requireNoResidualOutput(t *testing.T, state outputStateAfterCall) {
	require.Empty(t, state.childTransfers, "the failed transfer left an output record behind")
	require.Empty(t, state.backTransfers)
	require.Zero(t, state.backTransferValue.Sign())
}

func Test_TransferExecute_FailedFollowUpLeavesNoOutputTransfer(t *testing.T) {
	var state outputStateAfterCall
	_, _, returnCode := runFailingSubCall(t, test.KDATestTokenName, func(host vmhost.VMHost) int32 {
		transfer := &vmcommon.KDATransfer{
			KDAValue:     big.NewInt(noRollbackKDAAmount),
			KDATokenName: test.KDATestTokenName,
		}

		returnCode := transferToChildAndCall(host, []*vmcommon.KDATransfer{transfer}, noRollbackFailingMethod)
		state = captureOutputState(host)
		return returnCode
	})

	require.Equal(t, int32(1), returnCode)
	requireNoResidualOutput(t, state)

	// the fault is raised after the output state is restored, so the restore cannot erase it
	require.Equal(t, vmcommon.VMExecutionFailed, state.returnCode)
	require.Equal(t, vmhost.ErrSignalError.Error(), state.returnMessage)
}

// output.TransferKDA returns before recording anything when a transfer fails, so the residual
// check passes without the output-state wrap as well. What this pins is that the fault survives
// the restore on this failure path too.
func Test_TransferExecute_FailedTransferLeavesNoOutputTransfer(t *testing.T) {
	var state outputStateAfterCall
	callWithUnfundedSecond := multiTransferThenAccept([]byte("UNFUNDED-0000"))
	_, _, returnCode := runFailingSubCall(t, test.KDATestTokenName, func(host vmhost.VMHost) int32 {
		returnCode := callWithUnfundedSecond(host)
		state = captureOutputState(host)
		return returnCode
	})

	require.Equal(t, int32(1), returnCode)
	requireNoResidualOutput(t, state)
	require.Equal(t, vmcommon.VMExecutionFailed, state.returnCode)
	require.NotEmpty(t, state.returnMessage)
}

// On success the transfer record must survive: the wrap discards only its own snapshot.
func Test_TransferExecute_SuccessKeepsOutputTransfer(t *testing.T) {
	var state outputStateAfterCall
	_, _, _, returnCode := runSubCall(t, test.KDATestTokenName, func(host vmhost.VMHost) int32 {
		transfer := &vmcommon.KDATransfer{
			KDAValue:     big.NewInt(noRollbackKDAAmount),
			KDATokenName: test.KDATestTokenName,
		}

		returnCode := transferToChildAndCall(host, []*vmcommon.KDATransfer{transfer}, noRollbackAcceptMethod)
		state = captureOutputState(host)
		return returnCode
	}, true)

	require.Equal(t, int32(0), returnCode)
	require.Len(t, state.childTransfers, 1)
	require.Equal(t, test.KDATestTokenName, state.childTransfers[0].KDATransfers.KDATokenName)
	require.Equal(t, big.NewInt(noRollbackKDAAmount), state.childTransfers[0].KDATransfers.KDAValue)
}

// The wrap pushes an output snapshot that a successful call must discard. A leaked snapshot does
// not change the active state of its own frame, so the tests above cannot see it; it surfaces when
// the enclosing ExecuteOnDestContext frame pops the leaked snapshot instead of its own and the
// parent resumes on the child's censored output. The parent's return data catches that.
//
// The child's payOut is also a back-transfer to the parent, so the parent must see it through
// GetBackTransfers once the child returns. The failure tests above only pin that no back-transfer
// appears, which the host already guarantees by skipping calls that did not return Ok.
func Test_TransferExecute_NestedSuccessKeepsParentOutput(t *testing.T) {
	parentMarker := []byte("parent-before-call")

	var parentReturnData [][]byte
	var backTransfers []*vmcommon.KDATransfer
	var backTransferValue *big.Int
	_, _, _, returnCode := runSubCall(t, kdautils.KLVIdentifier, func(host vmhost.VMHost) int32 {
		host.Output().Finish(parentMarker)

		returnCode := vmhooks.ExecuteOnDestContextWithTypedArgs(
			host,
			noRollbackFollowUpGas,
			big.NewInt(0),
			[]byte(noRollbackPayOutMethod),
			test.ChildAddress,
			nil,
		)
		parentReturnData = append([][]byte(nil), host.Output().ReturnData()...)
		backTransfers, backTransferValue = host.ManagedTypes().GetBackTransfers()
		return returnCode
	}, true)

	require.Equal(t, int32(0), returnCode)
	require.NotEmpty(t, parentReturnData, "the parent lost its output state to the child's frame")
	require.Equal(t, parentMarker, parentReturnData[0])

	require.Empty(t, backTransfers, "a KLV payback is a value-only back-transfer")
	require.Equal(t, big.NewInt(noRollbackKLVAmount), backTransferValue, "the parent must see the child's payback")
}

// The pop on the transfer-failure path has no record to remove, since output.TransferKDA fails
// before writing one, so only its effect on the stack shows. Without it, the enclosing
// ExecuteOnDestContext frame restores the leaked snapshot instead of its own when the child fails,
// and the parent resumes on the child's censored output.
func Test_TransferExecute_NestedFailedTransferKeepsParentOutput(t *testing.T) {
	parentMarker := []byte("parent-before-call")

	var parentReturnData [][]byte
	_, _, returnCode := runFailingSubCall(t, kdautils.KLVIdentifier, func(host vmhost.VMHost) int32 {
		host.Output().Finish(parentMarker)

		returnCode := vmhooks.ExecuteOnDestContextWithTypedArgs(
			host,
			noRollbackFollowUpGas,
			big.NewInt(0),
			[]byte(noRollbackPayUnfunded),
			test.ChildAddress,
			nil,
		)
		parentReturnData = append([][]byte(nil), host.Output().ReturnData()...)
		return returnCode
	})

	require.Equal(t, int32(1), returnCode)
	require.NotEmpty(t, parentReturnData, "the parent lost its output state to the child's frame")
	require.Equal(t, parentMarker, parentReturnData[0])
}
