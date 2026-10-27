package hostCoretest

import (
	"testing"

	"github.com/klever-io/klever-go/kvm/testcommon"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/vmcommon"
	"github.com/stretchr/testify/require"
)

// TestNestedExecutionTimeoutLeavesNoLeakedInstances drives a same-code nested execution
// that runs until the VM timeout, the shape that previously raced the instance cleanup
// path against the still-unwinding slow-hook worker goroutine and could destroy the
// native instance while it was still executing (SIGSEGV / use-after-free).
//
// The test asserts the invariants it can actually observe once the timeout path joins
// the worker before unwinding: the call fails with the timeout error, no wasmer
// instance is leaked afterwards (ValidateInstances), and the timeout output is the
// same on every attempt. Run it with -race to cover the timeout goroutine against the
// execution goroutine (interruption request vs. active instance, VM output, errors).
// It does not assert the absence of the crash directly, because that failure mode aborts the whole test binary
// rather than failing a single case; the invariants below hold precisely because the
// worker is now joined before CleanInstance runs. The loop repeats to keep exercising
// the timing window that made the crash probabilistic.
func TestNestedExecutionTimeoutLeavesNoLeakedInstances(t *testing.T) {
	code := testcommon.GetTestSCCode("nested-timeout", "../../")

	const attempts = 15
	for range attempts {
		blockchain := testcommon.BlockchainHookStubForContracts([]*testcommon.InstanceTestSmartContract{
			testcommon.CreateInstanceContract(testcommon.ParentAddress).
				WithCode(code).
				WithBalance(1000),
		})

		host := testcommon.NewTestHostBuilder(t).
			WithBlockchainHook(blockchain).
			Build()

		input := testcommon.CreateTestContractCallInputBuilder().
			WithRecipientAddr(testcommon.ParentAddress).
			WithGasProvided(1_000_000_000).
			WithFunction("nestedForever").
			Build()

		vmOutput, err := host.RunSmartContractCall(input)
		require.ErrorIs(t, err, vmhost.ErrExecutionFailedWithTimeout)
		require.NoError(t, host.Runtime().ValidateInstances())

		// The timeout result is published only after the execution goroutine is
		// joined, so it does not depend on how far that goroutine got.
		require.NotNil(t, vmOutput)
		require.Equal(t, vmcommon.VMExecutionFailed, vmOutput.ReturnCode)
		require.Equal(t, vmhost.ErrExecutionFailedWithTimeout.Error(), vmOutput.ReturnMessage)
		require.ErrorIs(t, host.Runtime().GetAllErrors(), vmhost.ErrExecutionFailedWithTimeout)

		host.Reset()
	}
}

// TestTimeoutInterruptionDoesNotLeakIntoNextExecution runs normal calls on a host
// whose previous execution timed out, without resetting it in between. A stale
// interruption request would stop them with ExecutionFailed: the first call starts
// the code afresh, the second reuses its warm instance.
func TestTimeoutInterruptionDoesNotLeakIntoNextExecution(t *testing.T) {
	code := testcommon.GetTestSCCode("nested-timeout", "../../")
	blockchain := testcommon.BlockchainHookStubForContracts([]*testcommon.InstanceTestSmartContract{
		testcommon.CreateInstanceContract(testcommon.ParentAddress).
			WithCode(code).
			WithBalance(1000),
	})

	host := testcommon.NewTestHostBuilder(t).
		WithBlockchainHook(blockchain).
		Build()
	defer host.Reset()

	timeoutInput := testcommon.CreateTestContractCallInputBuilder().
		WithRecipientAddr(testcommon.ParentAddress).
		WithGasProvided(1_000_000_000).
		WithFunction("nestedForever").
		Build()

	_, err := host.RunSmartContractCall(timeoutInput)
	require.ErrorIs(t, err, vmhost.ErrExecutionFailedWithTimeout)

	for _, attempt := range []string{"fresh", "warm"} {
		input := testcommon.CreateTestContractCallInputBuilder().
			WithRecipientAddr(testcommon.ParentAddress).
			WithGasProvided(1_000_000).
			WithFunction("noop").
			Build()

		vmOutput, err := host.RunSmartContractCall(input)
		require.NoError(t, err, attempt)
		require.Equal(t, vmcommon.Ok, vmOutput.ReturnCode, attempt)
		require.NoError(t, host.Runtime().ValidateInstances(), attempt)
	}
}
