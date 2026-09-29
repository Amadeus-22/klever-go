package vmhooks_test

import (
	"testing"

	test "github.com/klever-io/klever-go/kvm/testcommon"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/klever-io/klever-go/vmcommon"
	"github.com/stretchr/testify/require"
)

// Reading the destination of the managed calling hooks.
//
// readDestinationArguments and managedDeleteContract clone the destination buffer before anything
// checks it. Every destination they read is an address, which the memory path always loads as
// exactly AddressLen bytes. Post-fork (FixAuditChangesV6) a destination longer than an address
// fails before it is cloned.

func TestManagedCallingHooks_OversizedDestination_PreForkVersusPostFork(t *testing.T) {
	const gasLeft = uint64(10_000_000)
	const destLen = 4 * 1024 * 1024

	hooks := []struct {
		name string
		call func(host vmhost.VMHost, dstHandle int32) func()
	}{
		{name: "managedExecuteOnDestContext", call: func(host vmhost.VMHost, dstHandle int32) func() {
			managedType := host.ManagedTypes()
			value := managedType.NewBigIntFromInt64(0)
			function := managedType.NewManagedBufferFromBytes([]byte(managedHookChildFunction))
			arguments, result := managedType.NewManagedBufferFromBytes(nil), managedType.NewManagedBuffer()
			return func() {
				_ = vmhooks.NewVMHooksImpl(host).ManagedExecuteOnDestContext(100_000, dstHandle, value, function, arguments, result)
			}
		}},
		{name: "managedDeleteContract", call: func(host vmhost.VMHost, dstHandle int32) func() {
			arguments := host.ManagedTypes().NewManagedBufferFromBytes(nil)
			return func() { vmhooks.NewVMHooksImpl(host).ManagedDeleteContract(dstHandle, 100_000, arguments) }
		}},
	}

	for _, hook := range hooks {
		prepare := func(host vmhost.VMHost) func() {
			// an address followed by padding, so only the length is wrong
			destination := append(append([]byte(nil), test.ChildAddress...), make([]byte, destLen-len(test.ChildAddress))...)
			return hook.call(host, host.ManagedTypes().NewManagedBufferFromBytes(destination))
		}

		pre := runManagedHook(t, preForkV6EnableEpochs(), gasLeft, prepare)
		post := runManagedHook(t, postForkV6EnableEpochs(), gasLeft, prepare)

		t.Logf("%s: pre-fork allocated=%d B, post-fork allocated=%d B", hook.name, pre.allocated, post.allocated)

		// the legacy path clones the destination before anything rejects it
		require.GreaterOrEqual(t, pre.allocated, uint64(destLen), hook.name)
		// TotalAlloc is process-wide, so this is a loose bound, still far below one copy of it
		require.Less(t, post.allocated, uint64(destLen/8), hook.name)

		// post-fork the oversized destination fails the execution
		require.Equal(t, vmcommon.VMExecutionFailed, post.returnCode, hook.name)
		require.Equal(t, vmhost.ErrInvalidArgument.Error(), post.returnMessage, hook.name)
	}
}

// managedDeleteContract is where the check changes consensus: pre-fork a destination longer than an
// address still ends the call Ok, post-fork it fails the execution. An address itself is Ok on both
// sides, so the boundary sits between AddressLen and AddressLen+1.
func TestManagedDeleteContract_DestinationLengthBoundary_PreForkVersusPostFork(t *testing.T) {
	const gasLeft = uint64(10_000_000)

	deleteWithDestinationLen := func(destLen int) func(vmhost.VMHost) func() {
		return func(host vmhost.VMHost) func() {
			managedType := host.ManagedTypes()
			// the child address followed by padding, so only the length is wrong
			destination := append(append([]byte(nil), test.ChildAddress...), make([]byte, destLen-len(test.ChildAddress))...)
			dstHandle := managedType.NewManagedBufferFromBytes(destination)
			arguments := managedType.NewManagedBufferFromBytes(nil)
			return func() { vmhooks.NewVMHooksImpl(host).ManagedDeleteContract(dstHandle, 100_000, arguments) }
		}
	}

	for _, destLen := range []int{vmhost.AddressLen, vmhost.AddressLen + 1} {
		pre := runManagedHook(t, preForkV6EnableEpochs(), gasLeft, deleteWithDestinationLen(destLen))
		require.Equal(t, vmcommon.Ok, pre.returnCode, "pre-fork, %d bytes: %s", destLen, pre.returnMessage)
	}

	atAddressLen := runManagedHook(t, postForkV6EnableEpochs(), gasLeft, deleteWithDestinationLen(vmhost.AddressLen))
	require.Equal(t, vmcommon.Ok, atAddressLen.returnCode, atAddressLen.returnMessage)

	overAddressLen := runManagedHook(t, postForkV6EnableEpochs(), gasLeft, deleteWithDestinationLen(vmhost.AddressLen+1))
	require.Equal(t, vmcommon.VMExecutionFailed, overAddressLen.returnCode)
	require.Equal(t, vmhost.ErrInvalidArgument.Error(), overAddressLen.returnMessage)
}
