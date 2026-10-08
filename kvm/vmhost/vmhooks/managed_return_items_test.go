package vmhooks_test

import (
	"encoding/binary"
	"math/big"
	"testing"

	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/data/transaction"
	"github.com/klever-io/klever-go/kapps"
	mock "github.com/klever-io/klever-go/kvm/mock/context"
	worldmock "github.com/klever-io/klever-go/kvm/mock/world"
	test "github.com/klever-io/klever-go/kvm/testcommon"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/klever-io/klever-go/vmcommon"
	"github.com/stretchr/testify/require"
)

// Converting a child call's return items into managed buffers (GHSA-7549-3338-g9hf).
//
// After a managed calling hook returns, setReturnDataIfExists turns the child's new return items
// into a managed vector of fresh handles through WriteManagedVecOfManagedBuffers. Pre-fork only
// the items' bytes are charged, after the handles are made, so an empty item becomes a handle for
// free. Post-fork (FixAuditChangesV6) each item costs MBufferNewFromBytes on top of its bytes,
// charged bounded before the first handle is made.

const mainnetMBufferNewFromBytes = 2000

// returnItemsHook reaches the child through one hook and makes it finish empty return items: the
// execute hooks call its emitEmptyItems endpoint, the create, deploy and upgrade hooks run its init
// or upgrade with the same arguments.
type returnItemsHook struct {
	name string
	// function is what the child is asked for: managedHookEmptyItemsFunction, or
	// managedHookFailAfterItemsFunction to make it fail once the items are finished
	function string
	// preForkGas pins the legacy gas of a call returning returnItemsNumItems empty items, so the
	// pre-fork path cannot drift for block replay
	preForkGas uint64
	// failureReturn is what the hook returns when the conversion fails: -1 for the execute hooks, 1
	// for create and deploy, 0 for the upgrade hooks, which return nothing
	failureReturn int32
	// metadataCopyPostFork is the code-metadata buffer the hook pays for post-fork and cloned for
	// free pre-fork; only the deploy hooks were converted
	metadataCopyPostFork uint64
	call                 func(host vmhost.VMHost, argumentsHandle int32, resultHandle int32) func() int32
}

// returnItemsNumItems is how many empty items the pinned calls return.
const returnItemsNumItems = 1000

func returnItemsHooks() []returnItemsHook {
	return returnItemsHooksCalling(managedHookEmptyItemsFunction)
}

// returnItemsHooksCalling is returnItemsHooks asking the child for function.
func returnItemsHooksCalling(function string) []returnItemsHook {
	const childGas = 1_000_000
	upgradeableMetadata := []byte{vmcommon.MetadataUpgradeable, 0}
	childCode := mock.MockContractCode(test.ChildAddress)

	buffer := func(host vmhost.VMHost, data []byte) int32 {
		return host.ManagedTypes().NewManagedBufferFromBytes(data)
	}
	zero := func(host vmhost.VMHost) int32 {
		return host.ManagedTypes().NewBigInt(big.NewInt(0))
	}
	hooks := func(host vmhost.VMHost) *vmhooks.VMHooksImpl { return vmhooks.NewVMHooksImpl(host) }
	emitEmptyItems := []byte(function)

	hooksToCall := []returnItemsHook{
		{name: "managedExecuteOnDestContext", preForkGas: 2049, failureReturn: -1, call: func(host vmhost.VMHost, args, result int32) func() int32 {
			dst, value, fn := buffer(host, test.ChildAddress), zero(host), buffer(host, emitEmptyItems)
			return func() int32 { return hooks(host).ManagedExecuteOnDestContext(childGas, dst, value, fn, args, result) }
		}},
		{name: "managedExecuteOnSameContext", preForkGas: 2049, failureReturn: -1, call: func(host vmhost.VMHost, args, result int32) func() int32 {
			dst, value, fn := buffer(host, test.ChildAddress), zero(host), buffer(host, emitEmptyItems)
			return func() int32 { return hooks(host).ManagedExecuteOnSameContext(childGas, dst, value, fn, args, result) }
		}},
		{name: "managedExecuteReadOnly", preForkGas: 2049, failureReturn: -1, call: func(host vmhost.VMHost, args, result int32) func() int32 {
			dst, fn := buffer(host, test.ChildAddress), buffer(host, emitEmptyItems)
			return func() int32 { return hooks(host).ManagedExecuteReadOnly(childGas, dst, fn, args, result) }
		}},
		{name: "managedCreateContract", preForkGas: 2096, failureReturn: 1, metadataCopyPostFork: managedDeployMetadataCopy, call: func(host vmhost.VMHost, args, result int32) func() int32 {
			value, code, meta, address := zero(host), buffer(host, childCode), buffer(host, upgradeableMetadata), buffer(host, nil)
			return func() int32 {
				return hooks(host).ManagedCreateContract(childGas, value, code, meta, args, address, result)
			}
		}},
		{name: "managedDeployFromSourceContract", preForkGas: 2096, failureReturn: 1, metadataCopyPostFork: managedDeployMetadataCopy, call: func(host vmhost.VMHost, args, result int32) func() int32 {
			value, source, meta, address := zero(host), buffer(host, test.ChildAddress), buffer(host, upgradeableMetadata), buffer(host, nil)
			return func() int32 {
				return hooks(host).ManagedDeployFromSourceContract(childGas, value, source, meta, args, address, result)
			}
		}},
		{name: "managedUpgradeContract", preForkGas: 2048, call: func(host vmhost.VMHost, args, result int32) func() int32 {
			dst, value, code, meta := buffer(host, test.ChildAddress), zero(host), buffer(host, childCode), buffer(host, upgradeableMetadata)
			return func() int32 {
				hooks(host).ManagedUpgradeContract(dst, childGas, value, code, meta, args, result)
				return 0
			}
		}},
		{name: "managedUpgradeFromSourceContract", preForkGas: 2048, call: func(host vmhost.VMHost, args, result int32) func() int32 {
			dst, value, source, meta := buffer(host, test.ChildAddress), zero(host), buffer(host, test.ChildAddress), buffer(host, upgradeableMetadata)
			return func() int32 {
				hooks(host).ManagedUpgradeFromSourceContract(dst, childGas, value, source, meta, args, result)
				return 0
			}
		}},
	}
	for i := range hooksToCall {
		hooksToCall[i].function = function
	}
	return hooksToCall
}

// returnItemsCall is what one returnItemsHook call left behind in the parent.
type returnItemsCall struct {
	argumentBytes uint64
	returnValue   int32
	// resultLen is the length of the result vector after the call, -1 when it has no buffer
	resultLen int32
	// result is the result vector after the call; it starts as returnItemsResultSentinel
	result []byte
}

// returnItemsResultSentinel seeds the result vector, so a failed conversion that touched it shows.
var returnItemsResultSentinel = []byte("sentinel")

// prepareReturnItemsCall builds the arguments asking the child for numItems empty items, on the
// mainnet MBufferNewFromBytes, and records what the call left in the result vector.
func prepareReturnItemsCall(hook returnItemsHook, numItems uint64, call *returnItemsCall) func(vmhost.VMHost) func() {
	return func(host vmhost.VMHost) func() {
		host.Metering().GasSchedule().ManagedBufferAPICost.MBufferNewFromBytes = mainnetMBufferNewFromBytes

		managedType := host.ManagedTypes()
		arguments := [][]byte{[]byte(hook.function), new(big.Int).SetUint64(numItems).Bytes()}
		handles := make([]int32, len(arguments))
		call.argumentBytes = 0
		for i, argument := range arguments {
			handles[i] = managedType.NewManagedBufferFromBytes(argument)
			call.argumentBytes += uint64(len(argument))
		}
		argumentsHandle := managedType.NewManagedBufferFromBytes(managedVecOf(handles...))
		resultHandle := managedType.NewManagedBufferFromBytes(returnItemsResultSentinel)

		callHook := hook.call(host, argumentsHandle, resultHandle)
		return func() {
			call.returnValue = callHook()
			call.resultLen = managedType.GetLength(resultHandle)
			call.result, _ = managedType.GetBytes(resultHandle)
		}
	}
}

func TestManagedReturnItems_PreForkKeepsTheLegacyGas(t *testing.T) {
	const gasLeft = uint64(10_000_000)

	for _, hook := range returnItemsHooks() {
		var call returnItemsCall
		pre := runManagedHook(t, preForkV6EnableEpochs(), gasLeft, prepareReturnItemsCall(hook, returnItemsNumItems, &call))

		t.Logf("%s: pre-fork %s gasUsed=%d", hook.name, pre.returnCode, pre.gasUsed)

		require.Equal(t, vmcommon.Ok, pre.returnCode, hook.name)
		require.Equal(t, hook.preForkGas, pre.gasUsed, hook.name)
		require.Equal(t, int32(0), call.returnValue, hook.name)
		require.Equal(t, int32(returnItemsNumItems*managedVecHandleLen), call.resultLen, hook.name)
	}
}

func TestManagedReturnItems_PostForkChargesEachItem(t *testing.T) {
	const gasLeft = uint64(10_000_000)

	for _, hook := range returnItemsHooks() {
		var preCall, postCall returnItemsCall
		pre := runManagedHook(t, preForkV6EnableEpochs(), gasLeft, prepareReturnItemsCall(hook, returnItemsNumItems, &preCall))
		post := runManagedHook(t, postForkV6EnableEpochs(), gasLeft, prepareReturnItemsCall(hook, returnItemsNumItems, &postCall))

		t.Logf("%s: pre-fork %s gasUsed=%d, post-fork %s gasUsed=%d", hook.name, pre.returnCode, pre.gasUsed, post.returnCode, post.gasUsed)

		require.Equal(t, vmcommon.Ok, post.returnCode, hook.name)
		require.Equal(t, int32(0), postCall.returnValue, hook.name)
		require.Equal(t, int32(returnItemsNumItems*managedVecHandleLen), postCall.resultLen, hook.name)
		// every hook here billed its decoded arguments twice pre-fork and once post-fork (GHSA-qqr9);
		// on top of that, every empty item now costs MBufferNewFromBytes where it cost nothing, and
		// the deploy hooks pay for the code-metadata buffer they clone
		saving := postCall.argumentBytes * mainnetDataCopyPerByte
		expected := pre.gasUsed - saving + returnItemsNumItems*mainnetMBufferNewFromBytes + hook.metadataCopyPostFork
		require.Equal(t, expected, post.gasUsed, hook.name)
	}
}

func TestManagedReturnItems_PostForkUnderfundedConversionFails(t *testing.T) {
	// 10,000 empty items need 20M gas post-fork, twice what the parent has left
	const numItems = 10_000
	const gasLeft = uint64(10_000_000)

	for _, hook := range returnItemsHooks() {
		var preCall, postCall returnItemsCall
		pre := runManagedHook(t, preForkV6EnableEpochs(), gasLeft, prepareReturnItemsCall(hook, numItems, &preCall))
		post := runManagedHook(t, postForkV6EnableEpochs(), gasLeft, prepareReturnItemsCall(hook, numItems, &postCall))

		t.Logf("%s: pre-fork %s, post-fork %s %q", hook.name, pre.returnCode, post.returnCode, post.returnMessage)

		// pre-fork the handles are made for free, whatever the gas left
		require.Equal(t, vmcommon.Ok, pre.returnCode, hook.name)
		require.Equal(t, int32(numItems*managedVecHandleLen), preCall.resultLen, hook.name)

		// post-fork the conversion fails before the result vector is written
		require.Equal(t, vmcommon.VMOutOfGas, post.returnCode, hook.name)
		require.Contains(t, post.returnMessage, vmhost.ErrNotEnoughGas.Error(), hook.name)
		require.Equal(t, hook.failureReturn, postCall.returnValue, hook.name)
		require.Equal(t, returnItemsResultSentinel, postCall.result, hook.name)
	}
}

func TestManagedReturnItems_FailedChildKeepsItsOwnError(t *testing.T) {
	// the call of the underfunded conversion, with a child that fails once its items are finished:
	// the items go away with the child's output state, so there is no conversion left to fail
	const numItems = 10_000
	const gasLeft = uint64(10_000_000)

	for _, hook := range returnItemsHooksCalling(managedHookFailAfterItemsFunction) {
		var preCall, postCall returnItemsCall
		pre := runManagedHook(t, preForkV6EnableEpochs(), gasLeft, prepareReturnItemsCall(hook, numItems, &preCall))
		post := runManagedHook(t, postForkV6EnableEpochs(), gasLeft, prepareReturnItemsCall(hook, numItems, &postCall))

		t.Logf("%s: pre-fork %s %q, post-fork %s %q", hook.name, pre.returnCode, pre.returnMessage, post.returnCode, post.returnMessage)

		require.Equal(t, vmcommon.VMExecutionFailed, pre.returnCode, hook.name)
		require.Equal(t, vmhost.ErrSignalError.Error(), pre.returnMessage, hook.name)

		// post-fork the call ends exactly as it did, not on the conversion's ErrNotEnoughGas
		require.Equal(t, pre.returnCode, post.returnCode, hook.name)
		require.Equal(t, pre.returnMessage, post.returnMessage, hook.name)
		require.Equal(t, preCall.returnValue, postCall.returnValue, hook.name)
		require.Equal(t, preCall.result, postCall.result, hook.name)
	}
}

// A built-in function's return items go through the same conversion as a contract's: minting
// NFTs through KleverAssetTrigger returns one item per minted NFT.
func TestManagedReturnItems_BuiltinReturnItemsAreCharged(t *testing.T) {
	const gasLeft = uint64(10_000_000)
	// the default MaxNFTMintBatch
	const numMinted = 50
	nftID := []byte("NFT-0101")

	setupWorld := func(host vmhost.VMHost, world *worldmock.MockWorld) {
		host.SetBuiltInFunctionsContainer(world.BuiltinFuncs.Container)
		require.NoError(t, world.SetKDAData(nftID, nil, &kapps.KDAData{
			ID:           nftID,
			AssetType:    kapps.KDAData_NonFungible,
			Name:         []byte("NFT TEST"),
			Ticker:       []byte("NFT"),
			OwnerAddress: test.ParentAddress,
			Royalties:    &kapps.RoyaltiesData{},
			Properties:   &kapps.PropertiesData{CanMint: true},
			Attributes:   &kapps.AttributesData{},
		}))
	}

	type mintCall struct {
		returnItemsCall
		// items are the return items the result vector holds after the call
		items [][]byte
	}
	prepare := func(call *mintCall) func(vmhost.VMHost) func() {
		return func(host vmhost.VMHost) func() {
			host.Metering().GasSchedule().ManagedBufferAPICost.MBufferNewFromBytes = mainnetMBufferNewFromBytes

			managedType := host.ManagedTypes()
			// trigger type, asset, nonce, amount, receiver, value
			arguments := [][]byte{
				{byte(transaction.AssetTriggerContract_Mint)},
				nftID,
				nil,
				{numMinted},
				test.ParentAddress,
				nil,
			}
			handles := make([]int32, len(arguments))
			for i, argument := range arguments {
				handles[i] = managedType.NewManagedBufferFromBytes(argument)
				call.argumentBytes += uint64(len(argument))
			}
			argumentsHandle := managedType.NewManagedBufferFromBytes(managedVecOf(handles...))
			resultHandle := managedType.NewManagedBufferFromBytes(returnItemsResultSentinel)
			dst := managedType.NewManagedBufferFromBytes(test.ParentAddress)
			value := managedType.NewBigInt(big.NewInt(0))
			function := managedType.NewManagedBufferFromBytes([]byte(core.BuiltInFunctionAssetTrigger))

			return func() {
				call.returnValue = vmhooks.NewVMHooksImpl(host).ManagedExecuteOnDestContext(1_000_000, dst, value, function, argumentsHandle, resultHandle)
				call.resultLen = managedType.GetLength(resultHandle)
				call.result, _ = managedType.GetBytes(resultHandle)
				for i := 0; i+managedVecHandleLen <= len(call.result); i += managedVecHandleLen {
					item, _ := managedType.GetBytes(int32(binary.BigEndian.Uint32(call.result[i:]))) // #nosec G115
					call.items = append(call.items, item)
				}
			}
		}
	}

	var preCall, postCall mintCall
	pre := runManagedHookInWorld(t, preForkV6EnableEpochs(), gasLeft, setupWorld, prepare(&preCall))
	post := runManagedHookInWorld(t, postForkV6EnableEpochs(), gasLeft, setupWorld, prepare(&postCall))

	t.Logf("pre-fork %s gasUsed=%d, post-fork %s gasUsed=%d", pre.returnCode, pre.gasUsed, post.returnCode, post.gasUsed)

	// one item per minted NFT, each holding its nonce
	minted := make([][]byte, numMinted)
	for i := range minted {
		minted[i] = []byte{byte(i + 1)}
	}

	require.Equal(t, vmcommon.Ok, pre.returnCode, pre.returnMessage)
	require.Equal(t, int32(0), preCall.returnValue)
	require.Equal(t, minted, preCall.items)

	require.Equal(t, vmcommon.Ok, post.returnCode, post.returnMessage)
	require.Equal(t, int32(0), postCall.returnValue)
	require.Equal(t, minted, postCall.items)

	// the items' bytes are charged on both sides; post-fork each item also costs MBufferNewFromBytes,
	// and the arguments are no longer billed twice (GHSA-qqr9)
	saving := postCall.argumentBytes * mainnetDataCopyPerByte
	require.Equal(t, pre.gasUsed-saving+numMinted*mainnetMBufferNewFromBytes, post.gasUsed)
}
