package hostCoretest

import (
	"testing"
	"time"

	"github.com/klever-io/klever-go/kvm/testcommon"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/vmcommon"
	"github.com/stretchr/testify/require"
)

// maxBigFloatPowTimeoutReturn bounds how long a timed-out BigFloatPow may take to
// return. It sits far above the execution timeout, to stay stable under -race, and
// far below the time the loop needs without its cancellation check (about a minute).
const maxBigFloatPowTimeoutReturn = 5 * time.Second

// TestBigFloatPowStopsAtExecutionTimeout drives BigFloatPow through a real host, so
// the loop observes the hook context that the timeout path cancels. With base 1 the
// result never overflows, so only cancellation can end MaxInt32 multiplications
// before the join would otherwise wait them out.
func TestBigFloatPowStopsAtExecutionTimeout(t *testing.T) {
	blockchain := testcommon.BlockchainHookStubForContracts([]*testcommon.InstanceTestSmartContract{
		testcommon.CreateInstanceContract(testcommon.ParentAddress).
			WithCode(testcommon.GetTestSCCode("bigfloat-pow-timeout", "../../")),
	})

	host := testcommon.NewTestHostBuilder(t).
		WithBlockchainHook(blockchain).
		Build()
	defer host.Reset()

	input := testcommon.CreateTestContractCallInputBuilder().
		WithRecipientAddr(testcommon.ParentAddress).
		WithGasProvided(1_000_000_000).
		WithFunction("powOneMaxExponent").
		Build()

	start := time.Now()
	vmOutput, err := host.RunSmartContractCall(input)
	elapsed := time.Since(start)

	require.ErrorIs(t, err, vmhost.ErrExecutionFailedWithTimeout)
	require.NotNil(t, vmOutput)
	require.Equal(t, vmcommon.VMExecutionFailed, vmOutput.ReturnCode)
	require.Less(t, elapsed, maxBigFloatPowTimeoutReturn)
	require.NoError(t, host.Runtime().ValidateInstances())
}
