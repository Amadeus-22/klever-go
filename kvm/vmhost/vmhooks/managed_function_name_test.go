package vmhooks_test

import (
	"testing"

	test "github.com/klever-io/klever-go/kvm/testcommon"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/stretchr/testify/require"
)

// Reading the function name of the managed calling hooks.
//
// readDestination*FunctionArguments clone the function name buffer, and string() copies it again,
// before anything checks it. Post-fork (FixAuditChangesV6) a name of MaxFunctionNameLength bytes or
// more, which verifyValidFunctionName would reject anyway, fails before anything is read: the
// managed twin of the memory-path check in getArgumentsFromMemory.

// executeOnDestWithFunctionName calls managedExecuteOnDestContext to the child with a function
// name of nameLen bytes and no arguments, and records the hook's return code.
func executeOnDestWithFunctionName(nameLen int, returnCode *int32) func(vmhost.VMHost) func() {
	return func(host vmhost.VMHost) func() {
		managedType := host.ManagedTypes()
		dstHandle := managedType.NewManagedBufferFromBytes(test.ChildAddress)
		valueHandle := managedType.NewBigIntFromInt64(0)
		functionHandle := managedType.NewManagedBufferFromBytes(make([]byte, nameLen))
		argumentsHandle := managedType.NewManagedBufferFromBytes(nil)
		resultHandle := managedType.NewManagedBuffer()
		return func() {
			*returnCode = vmhooks.NewVMHooksImpl(host).ManagedExecuteOnDestContext(
				100_000, dstHandle, valueHandle, functionHandle, argumentsHandle, resultHandle)
		}
	}
}

func TestManagedExecuteOnDest_OversizedFunctionName_PreForkVersusPostFork(t *testing.T) {
	const gasLeft = uint64(10_000_000)
	const nameLen = 4 * 1024 * 1024

	var preRet, postRet int32
	pre := runManagedHook(t, preForkV6EnableEpochs(), gasLeft, executeOnDestWithFunctionName(nameLen, &preRet))
	post := runManagedHook(t, postForkV6EnableEpochs(), gasLeft, executeOnDestWithFunctionName(nameLen, &postRet))

	t.Logf("pre-fork:  ret=%d gasUsed=%d allocated=%d B", preRet, pre.gasUsed, pre.allocated)
	t.Logf("post-fork: ret=%d gasUsed=%d allocated=%d B", postRet, post.gasUsed, post.allocated)

	// the legacy path clones the name and copies it into a string before it is rejected
	require.GreaterOrEqual(t, pre.allocated, uint64(2*nameLen))

	require.Equal(t, int32(-1), postRet)
	// TotalAlloc is process-wide, so this is a loose bound, still far below one copy of the name
	require.Less(t, post.allocated, uint64(nameLen/8))
}
