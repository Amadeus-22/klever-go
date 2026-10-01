package vmhooks_test

import (
	"encoding/binary"
	"math/big"
	"testing"

	mock "github.com/klever-io/klever-go/kvm/mock/context"
	test "github.com/klever-io/klever-go/kvm/testcommon"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/klever-io/klever-go/vmcommon"
	"github.com/stretchr/testify/require"
)

// Decoding a managed vector of managed buffers (GHSA-qqr9-92pp-89p7).
//
// ReadManagedVecOfManagedBuffers backs the topics of managedWriteLog and the arguments of
// managedCreateContract, managedDeleteContract, managedExecuteOnSame/DestContext and
// managedMultiTransferKDANFTExecute. Pre-fork it clones every item and charges afterwards through
// a charge that cannot fail, so a vector repeating one large handle clones it once per occurrence
// whatever the gas left. Post-fork (FixAuditChangesV6) the vector and the items' total are paid,
// bounded, before anything is cloned, and the total is capped at 16 MB.

const managedVecHandleLen = 4

// managedVecOf encodes a managed vector naming the given handles, in order.
func managedVecOf(handles ...int32) []byte {
	vec := make([]byte, len(handles)*managedVecHandleLen)
	for i, handle := range handles {
		binary.BigEndian.PutUint32(vec[i*managedVecHandleLen:], uint32(handle)) // #nosec G115
	}
	return vec
}

// repeatedHandleVec stores one itemLen-byte buffer and returns a vector naming it numItems times.
func repeatedHandleVec(managedType vmhost.ManagedTypesContext, numItems int, itemLen int) int32 {
	itemHandle := managedType.NewManagedBufferFromBytes(make([]byte, itemLen))
	handles := make([]int32, numItems)
	for i := range handles {
		handles[i] = itemHandle
	}
	return managedType.NewManagedBufferFromBytes(managedVecOf(handles...))
}

// writeLogWithTopics calls managedWriteLog with the topics vector buildTopics returns and no data.
func writeLogWithTopics(buildTopics func(managedType vmhost.ManagedTypesContext) int32) func(vmhost.VMHost) func() {
	return func(host vmhost.VMHost) func() {
		managedType := host.ManagedTypes()
		topicsHandle := buildTopics(managedType)
		dataHandle := managedType.NewManagedBufferFromBytes(nil)
		return func() {
			vmhooks.NewVMHooksImpl(host).ManagedWriteLog(topicsHandle, dataHandle)
		}
	}
}

// multiTransferWithArguments calls managedMultiTransferKDANFTExecute with an empty transfers
// vector and the arguments vector buildArguments returns: the arguments are decoded before the
// transfers, so this reaches the decoder through the multi-transfer hook.
func multiTransferWithArguments(buildArguments func(managedType vmhost.ManagedTypesContext) int32) func(vmhost.VMHost) func() {
	return func(host vmhost.VMHost) func() {
		managedType := host.ManagedTypes()
		dstHandle := managedType.NewManagedBufferFromBytes(test.ChildAddress)
		functionHandle := managedType.NewManagedBufferFromBytes([]byte("deposit"))
		transfersHandle := managedType.NewManagedBufferFromBytes(nil)
		argumentsHandle := buildArguments(managedType)
		return func() {
			_ = vmhooks.NewVMHooksImpl(host).ManagedMultiTransferKDANFTExecute(
				dstHandle, transfersHandle, 0, functionHandle, argumentsHandle)
		}
	}
}

func TestManagedVecOfBuffers_RepeatedHandle_PreForkVersusPostFork(t *testing.T) {
	const gasLeft = uint64(10_000_000)
	// 900 x 16 KiB is 14.7 MB, under the 16 MB cap, so it is the gas that has to stop it
	const numItems, itemLen = 900, 16 * 1024
	cloneTraffic := uint64(numItems * itemLen)

	repeated := func(managedType vmhost.ManagedTypesContext) int32 {
		return repeatedHandleVec(managedType, numItems, itemLen)
	}

	hooks := []struct {
		name    string
		prepare func(vmhost.VMHost) func()
	}{
		{name: "managedWriteLog topics", prepare: writeLogWithTopics(repeated)},
		{name: "managedMultiTransferKDANFTExecute arguments", prepare: multiTransferWithArguments(repeated)},
	}

	for _, hook := range hooks {
		pre := runManagedHook(t, preForkV6EnableEpochs(), gasLeft, hook.prepare)
		post := runManagedHook(t, postForkV6EnableEpochs(), gasLeft, hook.prepare)

		t.Logf("%s, %d items x %d B, %d gas left, clone traffic %d B", hook.name, numItems, itemLen, gasLeft, cloneTraffic)
		t.Logf("  pre-fork:  %s gasUsed=%d allocated=%d B", pre.returnCode, pre.gasUsed, pre.allocated)
		t.Logf("  post-fork: %s gasUsed=%d allocated=%d B (%s)", post.returnCode, post.gasUsed, post.allocated, post.returnMessage)

		// the legacy path still clones every occurrence, pinned for block replay
		require.GreaterOrEqual(t, pre.allocated, cloneTraffic, hook.name)
		require.Greater(t, pre.gasUsed, gasLeft, hook.name)

		// post-fork the items' total is paid before the first clone, so nothing is cloned
		require.Equal(t, vmhost.ErrNotEnoughGas.Error(), post.returnMessage, hook.name)
		require.LessOrEqual(t, post.gasUsed, gasLeft, hook.name)
		// TotalAlloc is process-wide, so this is a loose bound with room for unrelated allocations
		require.Less(t, post.allocated, cloneTraffic/8, hook.name)
	}
}

func TestManagedVecOfBuffers_OverCap_PostForkRejectedBeforeCharge(t *testing.T) {
	// two items of 9 MB decode to 18 MB, over the 16 MB cap. The gas left would pay for the
	// charge, so it is the cap and not the gas that rejects the decode
	const gasLeft = uint64(1_000_000_000)

	overCap := writeLogWithTopics(func(managedType vmhost.ManagedTypesContext) int32 {
		return repeatedHandleVec(managedType, 2, 9_000_000)
	})

	post := runManagedHook(t, postForkV6EnableEpochs(), gasLeft, overCap)

	t.Logf("post-fork: %s gasUsed=%d allocated=%d B (%s)", post.returnCode, post.gasUsed, post.allocated, post.returnMessage)

	// a failed execution consumes all the gas left, so the allocation is what shows no item was
	// cloned: only the 8-byte vector was
	require.Equal(t, vmhost.ErrManagedBufferLengthExceedsMaximum.Error(), post.returnMessage)
	require.Less(t, post.allocated, uint64(1_000_000))
}

func TestManagedVecOfBuffers_MissingHandle_SameErrorBothSides(t *testing.T) {
	const gasLeft = uint64(10_000_000)

	missingSecond := writeLogWithTopics(func(managedType vmhost.ManagedTypesContext) int32 {
		present := managedType.NewManagedBufferFromBytes([]byte("topic"))
		return managedType.NewManagedBufferFromBytes(managedVecOf(present, 9_999))
	})

	pre := runManagedHook(t, preForkV6EnableEpochs(), gasLeft, missingSecond)
	post := runManagedHook(t, postForkV6EnableEpochs(), gasLeft, missingSecond)

	t.Logf("pre-fork:  %s gasUsed=%d (%s)", pre.returnCode, pre.gasUsed, pre.returnMessage)
	t.Logf("post-fork: %s gasUsed=%d (%s)", post.returnCode, post.gasUsed, post.returnMessage)

	require.Equal(t, vmhost.ErrNoManagedBufferUnderThisHandle.Error(), pre.returnMessage)
	require.Equal(t, pre.returnMessage, post.returnMessage)
	require.Equal(t, pre.returnCode, post.returnCode)
}

func TestManagedVecOfBuffers_DistinctTopics_PostForkPaysEachByteOnce(t *testing.T) {
	const gasLeft = uint64(10_000_000)

	topics := [][]byte{[]byte("transfer"), test.ParentAddress, test.ChildAddress}
	data := []byte("payload")
	distinctTopics := func(host vmhost.VMHost) func() {
		managedType := host.ManagedTypes()
		handles := make([]int32, len(topics))
		for i, topic := range topics {
			handles[i] = managedType.NewManagedBufferFromBytes(topic)
		}
		topicsHandle := managedType.NewManagedBufferFromBytes(managedVecOf(handles...))
		dataHandle := managedType.NewManagedBufferFromBytes(data)
		return func() {
			vmhooks.NewVMHooksImpl(host).ManagedWriteLog(topicsHandle, dataHandle)
		}
	}

	pre := runManagedHook(t, preForkV6EnableEpochs(), gasLeft, distinctTopics)
	post := runManagedHook(t, postForkV6EnableEpochs(), gasLeft, distinctTopics)

	t.Logf("pre-fork:  %s gasUsed=%d", pre.returnCode, pre.gasUsed)
	t.Logf("post-fork: %s gasUsed=%d", post.returnCode, post.gasUsed)

	require.Equal(t, vmcommon.Ok, pre.returnCode)
	require.Equal(t, pre.returnCode, post.returnCode)

	// pre-fork the topics and the data are charged twice, post-fork once: the saving is exactly
	// one DataCopyPerByte per topic and data byte
	loggedBytes := uint64(len(data))
	for _, topic := range topics {
		loggedBytes += uint64(len(topic))
	}
	require.Equal(t, pre.gasUsed-loggedBytes*mainnetDataCopyPerByte, post.gasUsed)
}

// honestCallArguments is a small argument vector with a distinct buffer per argument, the shape an
// honest contract passes: 8 + 32 + 32 = 72 bytes.
func honestCallArguments(managedType vmhost.ManagedTypesContext) (int32, uint64) {
	arguments := [][]byte{[]byte("transfer"), test.ParentAddress, test.ChildAddress}
	handles := make([]int32, len(arguments))
	argumentBytes := uint64(0)
	for i, argument := range arguments {
		handles[i] = managedType.NewManagedBufferFromBytes(argument)
		argumentBytes += uint64(len(argument))
	}
	return managedType.NewManagedBufferFromBytes(managedVecOf(handles...)), argumentBytes
}

// callingHook reaches the child contract with the honest argument vector through one hook.
type callingHook struct {
	name string
	// preForkGas pins the legacy gas, so the pre-fork path cannot drift for block replay
	preForkGas uint64
	// chargedTwicePreFork is whether the hook billed the decoded arguments a second time pre-fork
	chargedTwicePreFork bool
	// returnItems is how many return items the hook converts into the result vector, each charged
	// MBufferNewFromBytes post-fork
	returnItems uint64
	call        func(host vmhost.VMHost, argumentsHandle int32) func()
}

func callingHooks() []callingHook {
	const childGas = 1000
	upgradeableMetadata := []byte{vmcommon.MetadataUpgradeable, 0}
	childCode := mock.MockContractCode(test.ChildAddress)

	buffer := func(host vmhost.VMHost, data []byte) int32 {
		return host.ManagedTypes().NewManagedBufferFromBytes(data)
	}
	zero := func(host vmhost.VMHost) int32 {
		return host.ManagedTypes().NewBigInt(big.NewInt(0))
	}
	hooks := func(host vmhost.VMHost) *vmhooks.VMHooksImpl { return vmhooks.NewVMHooksImpl(host) }

	return []callingHook{
		{name: "managedExecuteOnDestContext", preForkGas: 7849, chargedTwicePreFork: true, call: func(host vmhost.VMHost, args int32) func() {
			dst, value, fn, result := buffer(host, test.ChildAddress), zero(host), buffer(host, []byte(managedHookChildFunction)), buffer(host, nil)
			return func() { _ = hooks(host).ManagedExecuteOnDestContext(childGas, dst, value, fn, args, result) }
		}},
		{name: "managedExecuteOnSameContext", preForkGas: 7849, chargedTwicePreFork: true, call: func(host vmhost.VMHost, args int32) func() {
			dst, value, fn, result := buffer(host, test.ChildAddress), zero(host), buffer(host, []byte(managedHookChildFunction)), buffer(host, nil)
			return func() { _ = hooks(host).ManagedExecuteOnSameContext(childGas, dst, value, fn, args, result) }
		}},
		{name: "managedExecuteReadOnly", preForkGas: 7849, chargedTwicePreFork: true, call: func(host vmhost.VMHost, args int32) func() {
			dst, fn, result := buffer(host, test.ChildAddress), buffer(host, []byte(managedHookChildFunction)), buffer(host, nil)
			return func() { _ = hooks(host).ManagedExecuteReadOnly(childGas, dst, fn, args, result) }
		}},
		{name: "managedCreateContract", preForkGas: 8096, chargedTwicePreFork: true, returnItems: 1, call: func(host vmhost.VMHost, args int32) func() {
			value, code, meta := zero(host), buffer(host, childCode), buffer(host, upgradeableMetadata)
			address, result := buffer(host, nil), buffer(host, nil)
			return func() { _ = hooks(host).ManagedCreateContract(childGas, value, code, meta, args, address, result) }
		}},
		{name: "managedDeployFromSourceContract", preForkGas: 8096, chargedTwicePreFork: true, returnItems: 1, call: func(host vmhost.VMHost, args int32) func() {
			value, source, meta := zero(host), buffer(host, test.ChildAddress), buffer(host, upgradeableMetadata)
			address, result := buffer(host, nil), buffer(host, nil)
			return func() {
				_ = hooks(host).ManagedDeployFromSourceContract(childGas, value, source, meta, args, address, result)
			}
		}},
		{name: "managedUpgradeContract", preForkGas: 8598, chargedTwicePreFork: true, returnItems: 1, call: func(host vmhost.VMHost, args int32) func() {
			dst, value, code, meta, result := buffer(host, test.ChildAddress), zero(host), buffer(host, childCode), buffer(host, upgradeableMetadata), buffer(host, nil)
			return func() { hooks(host).ManagedUpgradeContract(dst, childGas, value, code, meta, args, result) }
		}},
		{name: "managedUpgradeFromSourceContract", preForkGas: 8598, chargedTwicePreFork: true, returnItems: 1, call: func(host vmhost.VMHost, args int32) func() {
			dst, value, source, meta, result := buffer(host, test.ChildAddress), zero(host), buffer(host, test.ChildAddress), buffer(host, upgradeableMetadata), buffer(host, nil)
			return func() { hooks(host).ManagedUpgradeFromSourceContract(dst, childGas, value, source, meta, args, result) }
		}},
		{name: "managedDeleteContract", preForkGas: 4202, chargedTwicePreFork: false, call: func(host vmhost.VMHost, args int32) func() {
			dst := buffer(host, test.ChildAddress)
			return func() { hooks(host).ManagedDeleteContract(dst, childGas, args) }
		}},
	}
}

func TestManagedVecOfBuffers_CallArguments_PostForkPaysEachByteOnce(t *testing.T) {
	const gasLeft = uint64(10_000_000)

	for _, hook := range callingHooks() {
		var argumentBytes uint64
		prepare := func(host vmhost.VMHost) func() {
			var argumentsHandle int32
			argumentsHandle, argumentBytes = honestCallArguments(host.ManagedTypes())
			return hook.call(host, argumentsHandle)
		}

		pre := runManagedHook(t, preForkV6EnableEpochs(), gasLeft, prepare)
		post := runManagedHook(t, postForkV6EnableEpochs(), gasLeft, prepare)

		t.Logf("%s: pre-fork %s gasUsed=%d, post-fork %s gasUsed=%d", hook.name, pre.returnCode, pre.gasUsed, post.returnCode, post.gasUsed)

		require.Equal(t, vmcommon.Ok, pre.returnCode, hook.name)
		require.Equal(t, vmcommon.Ok, post.returnCode, hook.name)
		require.Equal(t, hook.preForkGas, pre.gasUsed, hook.name)

		// the hooks that billed the decoded arguments twice save exactly one DataCopyPerByte per
		// argument byte post-fork; managedDeleteContract never did, so its gas does not move. Each
		// return item converted into the result vector costs MBufferNewFromBytes post-fork
		saving := uint64(0)
		if hook.chargedTwicePreFork {
			saving = argumentBytes * mainnetDataCopyPerByte
		}
		require.Equal(t, pre.gasUsed-saving+hook.returnItems*testMBufferNewFromBytes, post.gasUsed, hook.name)
	}
}

// writeLogWithData calls managedWriteLog with no topics and the data handle buildData returns.
func writeLogWithData(buildData func(managedType vmhost.ManagedTypesContext) int32) func(vmhost.VMHost) func() {
	return func(host vmhost.VMHost) func() {
		managedType := host.ManagedTypes()
		topicsHandle := managedType.NewManagedBufferFromBytes(nil)
		dataHandle := buildData(managedType)
		return func() {
			vmhooks.NewVMHooksImpl(host).ManagedWriteLog(topicsHandle, dataHandle)
		}
	}
}

func TestManagedWriteLog_Data_PostForkChargedBeforeClone(t *testing.T) {
	// 1 MB of data costs 50M gas to copy, five times the gas left, so post-fork it is never cloned
	const gasLeft = uint64(10_000_000)
	const dataLen = 1_000_000

	largeData := writeLogWithData(func(managedType vmhost.ManagedTypesContext) int32 {
		return managedType.NewManagedBufferFromBytes(make([]byte, dataLen))
	})

	pre := runManagedHook(t, preForkV6EnableEpochs(), gasLeft, largeData)
	post := runManagedHook(t, postForkV6EnableEpochs(), gasLeft, largeData)

	t.Logf("pre-fork:  %s gasUsed=%d allocated=%d B (%s)", pre.returnCode, pre.gasUsed, pre.allocated, pre.returnMessage)
	t.Logf("post-fork: %s gasUsed=%d allocated=%d B (%s)", post.returnCode, post.gasUsed, post.allocated, post.returnMessage)

	// the legacy path clones the data before charging for it
	require.GreaterOrEqual(t, pre.allocated, uint64(dataLen))

	require.Equal(t, vmhost.ErrNotEnoughGas.Error(), post.returnMessage)
	// TotalAlloc is process-wide, so this is a loose bound with room for unrelated allocations
	require.Less(t, post.allocated, uint64(dataLen/8))
}

func TestManagedWriteLog_MissingDataHandle_SameErrorBothSides(t *testing.T) {
	const gasLeft = uint64(10_000_000)

	missingData := writeLogWithData(func(vmhost.ManagedTypesContext) int32 { return 9_999 })

	pre := runManagedHook(t, preForkV6EnableEpochs(), gasLeft, missingData)
	post := runManagedHook(t, postForkV6EnableEpochs(), gasLeft, missingData)

	t.Logf("pre-fork:  %s (%s)", pre.returnCode, pre.returnMessage)
	t.Logf("post-fork: %s (%s)", post.returnCode, post.returnMessage)

	require.Equal(t, vmhost.ErrNoManagedBufferUnderThisHandle.Error(), pre.returnMessage)
	require.Equal(t, pre.returnMessage, post.returnMessage)
	require.Equal(t, pre.returnCode, post.returnCode)
}
