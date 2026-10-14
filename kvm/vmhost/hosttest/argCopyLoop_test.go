package hostCoretest

import (
	"bytes"
	"testing"
	"time"

	blockchainConfig "github.com/klever-io/klever-go/config"
	"github.com/klever-io/klever-go/core"
	kvmConfig "github.com/klever-io/klever-go/kvm/config"
	gasSchedules "github.com/klever-io/klever-go/kvm/scenarioexec/gasSchedules"
	"github.com/klever-io/klever-go/kvm/testcommon"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/vmcommon"
	"github.com/stretchr/testify/require"
)

// argCopyLoopGas is MaxGasPerTX at multiplier 1
const argCopyLoopGas = uint64(400_000_000)

// argCopyLoopArgumentSize is the ~1 MB argument every iteration of the loops copies or parses
const argCopyLoopArgumentSize = 1 << 20

type argCopyLoopResult struct {
	err        error
	returnCode vmcommon.ReturnCode
	message    string
	elapsed    time.Duration
	gasUsed    uint64
}

func runArgCopyLoop(
	t *testing.T,
	function string,
	fixAuditChangesV6Active bool,
	gasSchedule kvmConfig.GasScheduleMap,
) argCopyLoopResult {
	t.Helper()

	blockchain := testcommon.BlockchainHookStubForContracts([]*testcommon.InstanceTestSmartContract{
		testcommon.CreateInstanceContract(testcommon.ParentAddress).
			WithCode(testcommon.GetTestSCCode("arg-copy-loop", "../../")),
	})

	// every other flag defaults to epoch 0, so only the activation under test moves
	enableEpochs := blockchainConfig.EnableEpochs{}
	if !fixAuditChangesV6Active {
		enableEpochs.FixAuditChangesV6 = 1_000_000
	}

	host := testcommon.NewTestHostBuilder(t).
		WithBlockchainHook(blockchain).
		WithEnableEpochs(enableEpochs).
		WithGasSchedule(gasSchedule).
		Build()
	defer host.Reset()

	input := testcommon.CreateTestContractCallInputBuilder().
		WithRecipientAddr(testcommon.ParentAddress).
		WithGasProvided(argCopyLoopGas).
		WithFunction(function).
		WithArguments(bytes.Repeat([]byte{0xFF}, argCopyLoopArgumentSize)).
		Build()

	start := time.Now()
	vmOutput, err := host.RunSmartContractCall(input)
	elapsed := time.Since(start)

	require.NotNil(t, vmOutput)
	return argCopyLoopResult{
		err:        err,
		returnCode: vmOutput.ReturnCode,
		message:    vmOutput.ReturnMessage,
		elapsed:    elapsed,
		gasUsed:    argCopyLoopGas - vmOutput.GasRemaining,
	}
}

// TestArgCopyLoopStopsAtOutOfGas loops an argument getter over a ~1 MB argument at the maximum
// transaction gas. Before FixAuditChangesV6 each call charges a flat cost while copying or parsing
// the whole argument, so the loop outlasts the gas and only the execution timeout ends it. From the
// fork each call is charged per byte, so the gas runs out well inside the timeout.
func TestArgCopyLoopStopsAtOutOfGas(t *testing.T) {
	gasSchedule, err := gasSchedules.LoadGasScheduleConfig(gasSchedules.GetV1())
	require.NoError(t, err)

	// the post-fork loop is bounded by gas, not by time: a margin of half the timeout keeps it
	// stable under -race while still being far from the timeout path
	maxPostForkElapsed := core.MinSCExecutionTimeout / 2

	for _, function := range []string{"bigIntArgLoop", "getArgumentLoop"} {
		t.Run(function, func(t *testing.T) {
			pre := runArgCopyLoop(t, function, false, gasSchedule)
			t.Logf("%s pre-fork: code=%v err=%v msg=%q elapsed=%v gasUsed=%d",
				function, pre.returnCode, pre.err, pre.message, pre.elapsed, pre.gasUsed)

			post := runArgCopyLoop(t, function, true, gasSchedule)
			t.Logf("%s post-fork: code=%v err=%v msg=%q elapsed=%v gasUsed=%d",
				function, post.returnCode, post.err, post.message, post.elapsed, post.gasUsed)

			// pin: before the fork the flat charge lets the loop run until the execution timeout cuts it
			require.ErrorIs(t, pre.err, vmhost.ErrExecutionFailedWithTimeout)
			require.Equal(t, vmcommon.VMExecutionFailed, pre.returnCode)

			require.NotErrorIs(t, post.err, vmhost.ErrExecutionFailedWithTimeout)
			require.Equal(t, vmcommon.VMOutOfGas, post.returnCode)
			require.Equal(t, argCopyLoopGas, post.gasUsed)
			require.Less(t, post.elapsed, maxPostForkElapsed)
		})
	}
}
