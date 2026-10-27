package hostCoretest

import (
	"encoding/binary"
	"math/big"
	"testing"

	blockchainConfig "github.com/klever-io/klever-go/config"
	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/kapps"
	worldmock "github.com/klever-io/klever-go/kvm/mock/world"
	test "github.com/klever-io/klever-go/kvm/testcommon"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/klever-io/klever-go/vmcommon"
	"github.com/stretchr/testify/require"
)

// Zero-value transfers as call value (KLC-2830).
//
// KleverTransfer settles a zero-value transfer without touching any balance, so it succeeds for any
// asset id and NFT nonce: a nonexistent asset, a nonexistent nonce, or an NFT the sender does not
// hold. The follow-up execution builds its call value from the transfer list, not from what was
// settled, so before FixAuditChangesV6 the callee is told it was paid in an asset nobody moved.
// From the fork the transfer still succeeds but is not reported to the callee.
//
// Each case runs through the three ways a contract reaches a callee with KDA attached: naming
// KleverTransfer as the function (callBuiltinFunction), TransferKDANFTExecuteWithTypedArgs, and
// managedMultiTransferKDANFTExecute, which funnels into the latter.

func preForkV6EnableEpochs() blockchainConfig.EnableEpochs {
	return blockchainConfig.EnableEpochs{FixAuditChangesV6: 1_000_000}
}

func postForkV6EnableEpochs() blockchainConfig.EnableEpochs {
	return blockchainConfig.EnableEpochs{FixAuditChangesV6: 0}
}

var (
	zeroValueFakeAsset = []byte("FAKE-1234")
	zeroValueNFTAsset  = []byte("NFT-1234")
)

const (
	// zeroValueHeldNonce is the one NFT of zeroValueNFTAsset in the world, held by retargetedAddress.
	zeroValueHeldNonce = uint64(5)
	// zeroValueMissingNonce is a nonce of zeroValueNFTAsset that nobody holds.
	zeroValueMissingNonce = uint64(99)
)

type zeroValueTransferSpec struct {
	token []byte
	nonce uint64
	value int64
}

// newTransfers builds a fresh list per run: the built-in marks the transfers it settles executed.
func newTransfers(specs []zeroValueTransferSpec) []*vmcommon.KDATransfer {
	transfers := make([]*vmcommon.KDATransfer, 0, len(specs))
	for _, spec := range specs {
		tokenType := core.Fungible
		if spec.nonce > 0 {
			tokenType = core.NonFungible
		}
		transfers = append(transfers, &vmcommon.KDATransfer{
			KDATokenName:  spec.token,
			KDATokenNonce: spec.nonce,
			KDATokenType:  uint32(tokenType),
			KDAValue:      big.NewInt(spec.value),
		})
	}
	return transfers
}

// viaBuiltinFunction declares the transfers in the KleverTransfer arguments, followed by "deposit".
// The nonce is always 8 bytes like the neighbouring tests': the payable check skips a call whose
// args[2] (the first nonce) is non-empty, and the mock contracts are not payable.
func viaBuiltinFunction(specs []zeroValueTransferSpec) func(vmhost.VMHost) {
	args := [][]byte{big.NewInt(int64(len(specs))).Bytes()}
	for _, spec := range specs {
		nonce := make([]byte, 8)
		binary.BigEndian.PutUint64(nonce, spec.nonce)
		args = append(args, spec.token, nonce, big.NewInt(spec.value).Bytes())
	}
	args = append(args, []byte("deposit"))

	return callBuiltinDirectly(test.ChildAddress, args)
}

// viaTypedArgs settles the transfers to the child and calls "deposit" on it.
func viaTypedArgs(specs []zeroValueTransferSpec) func(vmhost.VMHost) {
	return func(host vmhost.VMHost) {
		ret := vmhooks.TransferKDANFTExecuteWithTypedArgs(
			host,
			test.ChildAddress,
			newTransfers(specs),
			retargetFollowUpGas,
			[]byte("deposit"),
			nil,
		)
		if ret != 0 {
			host.Runtime().FailExecution(vmhost.ErrExecutionFailed)
		}
	}
}

// viaManagedMultiTransfer encodes the transfers as the managed vector of 16-byte records
// (token handle, nonce, value handle) and calls "deposit" on the child.
func viaManagedMultiTransfer(specs []zeroValueTransferSpec) func(vmhost.VMHost) {
	return func(host vmhost.VMHost) {
		managedType := host.ManagedTypes()

		vec := make([]byte, 0, len(specs)*16)
		for _, transfer := range newTransfers(specs) {
			record := make([]byte, 16)
			binary.BigEndian.PutUint32(record[0:4], uint32(managedType.NewManagedBufferFromBytes(transfer.KDATokenName))) // #nosec G115
			binary.BigEndian.PutUint64(record[4:12], transfer.KDATokenNonce)
			binary.BigEndian.PutUint32(record[12:16], uint32(managedType.NewBigInt(transfer.KDAValue))) // #nosec G115
			vec = append(vec, record...)
		}

		ret := vmhooks.NewVMHooksImpl(host).ManagedMultiTransferKDANFTExecute(
			managedType.NewManagedBufferFromBytes(test.ChildAddress),
			managedType.NewManagedBufferFromBytes(vec),
			retargetFollowUpGas,
			managedType.NewManagedBufferFromBytes([]byte("deposit")),
			managedType.NewManagedBufferFromBytes(nil),
		)
		if ret != 0 {
			host.Runtime().FailExecution(vmhost.ErrExecutionFailed)
		}
	}
}

// seedHeldNFT gives retargetedAddress the only NFT of the collection, so the collection exists and
// the sender holds none of it.
func seedHeldNFT(t *testing.T) func(*worldmock.MockWorld) {
	return func(world *worldmock.MockWorld) {
		holder, err := world.AccountsCacher.LoadUser(retargetedAddress)
		require.NoError(t, err)
		require.NoError(t, holder.SetUserKDA(zeroValueNFTAsset, nftNonceKey(zeroValueHeldNonce), &kapps.UserKDA{Balance: 1}))
	}
}

func nftNonceKey(nonce uint64) []byte {
	return []byte(new(big.Int).SetUint64(nonce).String())
}

func nftBalanceOf(t *testing.T, world *worldmock.MockWorld, address []byte, nonce uint64) int64 {
	account, err := world.AccountsCacher.LoadUser(address)
	require.NoError(t, err)

	kda, err := account.GetUserKDA(zeroValueNFTAsset, nftNonceKey(nonce), true)
	if err != nil || kda == nil {
		return 0
	}

	return kda.Balance
}

func Test_ZeroValueTransfer_NotReportedAsCallValue(t *testing.T) {
	paths := []struct {
		name   string
		caller func([]zeroValueTransferSpec) func(vmhost.VMHost)
	}{
		{name: "builtinFunction", caller: viaBuiltinFunction},
		{name: "typedArgs", caller: viaTypedArgs},
		{name: "managedMultiTransfer", caller: viaManagedMultiTransfer},
	}

	cases := []struct {
		name      string
		transfers []zeroValueTransferSpec
		// numPostFork is how many transfers the callee observes from the fork; before it, every one
		numPostFork int
		// value is the KDATestTokenName call value and settled amount, the same on both sides
		value int64
		// zeroValue is the zero-value transfer a pre-fork callee is wrongly told about
		zeroValue observedTransfer
	}{
		{
			name:      "nonexistent asset",
			transfers: []zeroValueTransferSpec{{token: zeroValueFakeAsset}},
			zeroValue: observedTransfer{token: zeroValueFakeAsset},
		},
		{
			name:      "nonexistent nonce of an existing collection",
			transfers: []zeroValueTransferSpec{{token: zeroValueNFTAsset, nonce: zeroValueMissingNonce}},
			zeroValue: observedTransfer{token: zeroValueNFTAsset, nonce: zeroValueMissingNonce},
		},
		{
			name:      "NFT the sender does not hold",
			transfers: []zeroValueTransferSpec{{token: zeroValueNFTAsset, nonce: zeroValueHeldNonce}},
			zeroValue: observedTransfer{token: zeroValueNFTAsset, nonce: zeroValueHeldNonce},
		},
		{
			name: "funded transfer next to a zero-value one",
			transfers: []zeroValueTransferSpec{
				{token: test.KDATestTokenName, value: retargetTransferAmount},
				{token: zeroValueFakeAsset},
			},
			numPostFork: 1,
			value:       retargetTransferAmount,
			zeroValue:   observedTransfer{token: zeroValueFakeAsset},
		},
	}

	for _, path := range paths {
		for _, tc := range cases {
			assertSettlement := func(t *testing.T, world *worldmock.MockWorld) {
				require.Equal(t, tc.value, kdaBalanceOf(t, world, test.ChildAddress), "the funded transfer is settled exactly once")
				require.Equal(t, retargetInitialBalance-tc.value, kdaBalanceOf(t, world, test.ParentAddress))
				require.Equal(t, int64(1), nftBalanceOf(t, world, retargetedAddress, zeroValueHeldNonce), "the NFT never moves")
				require.Zero(t, nftBalanceOf(t, world, test.ChildAddress, zeroValueHeldNonce))
			}

			t.Run(path.name+"/"+tc.name+"/preFork", func(t *testing.T) {
				observed, world := runPostTransferCall(t, path.caller(tc.transfers), preForkV6EnableEpochs(), seedHeldNFT(t))

				require.Equal(t, test.ChildAddress, observed.calledBy)
				require.Equal(t, len(tc.transfers), observed.numTransfers, "legacy behaviour: a zero-value transfer is reported as call value")
				require.Contains(t, observed.transfers, tc.zeroValue, "legacy behaviour: the zero-value transfer itself is reported")
				require.Equal(t, tc.value, observed.value)
				assertSettlement(t, world)
			})

			t.Run(path.name+"/"+tc.name+"/postFork", func(t *testing.T) {
				observed, world := runPostTransferCall(t, path.caller(tc.transfers), postForkV6EnableEpochs(), seedHeldNFT(t))

				require.Equal(t, test.ChildAddress, observed.calledBy, "the follow-up call still runs")
				require.Equal(t, tc.numPostFork, observed.numTransfers, "a zero-value transfer is not call value")
				require.NotContains(t, observed.transfers, tc.zeroValue, "the zero-value transfer is not reported")
				require.Equal(t, tc.value, observed.value)
				assertSettlement(t, world)
			})
		}
	}
}

// Test_ZeroValueTransfer_FollowUpKleverTransfer reaches both gates in one call: the typed hook filters
// the zero-value transfer out of the call input, and the follow-up, KleverTransfer itself, settles one
// more declared transfer on top of the one the hook already settled and executed. The executed
// transfer must be carried into the follow-up exactly once, never settled a second time.
func Test_ZeroValueTransfer_FollowUpKleverTransfer(t *testing.T) {
	const declaredAmount = int64(500)

	typed := []zeroValueTransferSpec{
		{token: test.KDATestTokenName, value: retargetTransferAmount},
		{token: zeroValueFakeAsset},
	}

	// [payee, numTransfers, token, nonce, value, function]
	followUp := func(payee []byte) [][]byte {
		return [][]byte{
			payee,
			{0x01},
			test.KDATestTokenName,
			make([]byte, 8),
			big.NewInt(declaredAmount).Bytes(),
			[]byte("deposit"),
		}
	}
	caller := func(payee []byte) func(vmhost.VMHost) {
		return func(host vmhost.VMHost) {
			ret := vmhooks.TransferKDANFTExecuteWithTypedArgs(
				host,
				test.ChildAddress,
				newTransfers(typed),
				retargetFollowUpGas,
				[]byte(core.BuiltInFunctionTransfer),
				followUp(payee),
			)
			if ret != 0 {
				host.Runtime().FailExecution(vmhost.ErrExecutionFailed)
			}
		}
	}

	t.Run("sameRecipient", func(t *testing.T) {
		for _, tc := range []struct {
			name         string
			epochs       blockchainConfig.EnableEpochs
			numTransfers int
		}{
			{name: "preFork", epochs: preForkV6EnableEpochs(), numTransfers: 3},
			{name: "postFork", epochs: postForkV6EnableEpochs(), numTransfers: 2},
		} {
			t.Run(tc.name, func(t *testing.T) {
				observed, world := runPostTransferCall(t, caller(test.ChildAddress), tc.epochs)

				require.Equal(t, test.ChildAddress, observed.calledBy)
				require.Equal(t, tc.numTransfers, observed.numTransfers)
				require.Equal(t, retargetTransferAmount+declaredAmount, observed.value)

				require.Equal(t, retargetTransferAmount+declaredAmount, kdaBalanceOf(t, world, test.ChildAddress),
					"every funded transfer is settled exactly once")
				require.Equal(t, retargetInitialBalance-retargetTransferAmount-declaredAmount, kdaBalanceOf(t, world, test.ParentAddress))
				require.Zero(t, kdaBalanceOf(t, world, retargetedAddress))
			})
		}
	})

	// With V5 active (the zero value of the epoch), a retargeted recipient no longer sees the
	// transfers settled to the pre-parse recipient, funded or not, under either V6 setting.
	t.Run("retargeted", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			epochs blockchainConfig.EnableEpochs
		}{
			{name: "preFork", epochs: preForkV6EnableEpochs()},
			{name: "postFork", epochs: postForkV6EnableEpochs()},
		} {
			t.Run(tc.name, func(t *testing.T) {
				observed, world := runPostTransferCall(t, caller(retargetedAddress), tc.epochs)

				require.Equal(t, retargetedAddress, observed.calledBy)
				require.Equal(t, 1, observed.numTransfers, "only the transfer this run settled here is reported")
				require.Equal(t, declaredAmount, observed.value)

				require.Equal(t, retargetTransferAmount, kdaBalanceOf(t, world, test.ChildAddress))
				require.Equal(t, declaredAmount, kdaBalanceOf(t, world, retargetedAddress))
				require.Equal(t, retargetInitialBalance-retargetTransferAmount-declaredAmount, kdaBalanceOf(t, world, test.ParentAddress))
			})
		}
	})
}
