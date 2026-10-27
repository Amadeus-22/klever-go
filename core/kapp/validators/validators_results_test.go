package validators

import (
	"strings"
	"testing"

	"github.com/klever-io/klever-go/common"
	"github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/core/kapp"
	"github.com/klever-io/klever-go/core/process"
	"github.com/klever-io/klever-go/data/state"
	"github.com/klever-io/klever-go/data/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin the result code, error and error receipt that Register and
// UpdateValidator produce for each refused input. They run during block processing,
// so a change to any of them changes the block's results.

const invalidUTF8 = "\xff"

// requireLastErrorReceipt asserts that the last receipt is an error receipt of the current
// contract for field, carrying err's message.
func requireLastErrorReceipt(t *testing.T, v *validatorsKApp, field string, err error) {
	t.Helper()

	ctx := v.KAppController.GetCurrentKAppContext()
	receipts := ctx.Receipts().Get()
	require.NotEmpty(t, receipts)

	last := receipts[len(receipts)-1]
	require.Len(t, last.Data, 3)
	assert.Equal(t, []byte{byte(kapp.ReceiptTypeError), byte(ctx.ContractID())}, last.Data[0])
	assert.Equal(t, field, string(last.Data[1]))
	assert.Equal(t, err.Error(), string(last.Data[2]))
}

func tooManyURIs() map[string]string {
	uris := make(map[string]string, core.MaxURIMapSize+1)
	for i := 0; i <= core.MaxURIMapSize; i++ {
		uris[strings.Repeat("k", i+1)] = "uri"
	}

	return uris
}

func TestRegister_RefusalResults(t *testing.T) {
	t.Parallel()

	owner := makeAddress("owner")

	metadataCases := []struct {
		name   string
		mutate func(cfg *transaction.ValidatorConfig)
		field  string
	}{
		{"logo not utf8", func(cfg *transaction.ValidatorConfig) { cfg.Logo = invalidUTF8 }, common.ErrFieldInvalidLogo},
		{"logo too long", func(cfg *transaction.ValidatorConfig) { cfg.Logo = strings.Repeat("l", core.MaxLogoURISize+1) }, common.ErrFieldInvalidLogo},
		{"too many URIs", func(cfg *transaction.ValidatorConfig) { cfg.URIs = tooManyURIs() }, common.ErrFieldURICountExceeded},
		{"URI key too long", func(cfg *transaction.ValidatorConfig) {
			cfg.URIs = map[string]string{strings.Repeat("k", core.MaxURIKeySize+1): "uri"}
		}, common.ErrFieldInvalidURI},
		{"URI value too long", func(cfg *transaction.ValidatorConfig) {
			cfg.URIs = map[string]string{"site": strings.Repeat("u", core.MaxURIValueSize+1)}
		}, common.ErrFieldInvalidURI},
		{"URI value not utf8", func(cfg *transaction.ValidatorConfig) { cfg.URIs = map[string]string{"site": invalidUTF8} }, common.ErrFieldInvalidURI},
		{"name not utf8", func(cfg *transaction.ValidatorConfig) { cfg.Name = invalidUTF8 }, common.ErrFieldInvalidName},
		{"name too long", func(cfg *transaction.ValidatorConfig) { cfg.Name = strings.Repeat("n", core.MaxNameSize+1) }, common.ErrFieldInvalidName},
	}

	for _, tt := range metadataCases {
		t.Run(tt.name, func(t *testing.T) {
			v := newProofTestKApp(t, true)
			key := newTestBLSKey(t)
			tc := newRegisterContractWithProof(owner, key.pub, key.proof(t, owner))
			tt.mutate(tc.Config)

			resultCode, err := v.Register(tc.GetOwnerAddress(), tc)

			assert.Equal(t, transaction.Transaction_ParameterInvalid, resultCode)
			assert.Equal(t, common.ErrInvalidValue, err)
			requireLastErrorReceipt(t, v, tt.field, common.ErrInvalidValue)
			requireNotRegistered(t, v, owner, key.pub)
		})
	}

	t.Run("key already bound to another validator", func(t *testing.T) {
		v := newProofTestKApp(t, true)
		other := makeAddress("other")
		key := newTestBLSKey(t)
		resultCode, err := v.Register(other, newRegisterContractWithProof(other, key.pub, key.proof(t, other)))
		require.Equal(t, transaction.Transaction_Ok, resultCode)
		require.NoError(t, err)

		resultCode, err = v.Register(owner, newRegisterContractWithProof(owner, key.pub, key.proof(t, owner)))

		assert.Equal(t, transaction.Transaction_AccountError, resultCode)
		assert.Equal(t, common.ErrAccountValidatorSet, err)
		requireLastErrorReceipt(t, v, common.ErrFieldBLSKeyAlreadyUsed, common.ErrAccountValidatorSet)
		requireBound(t, v, other, key.pub)
	})

	t.Run("key revoked by a rotation keeps its owner and is refused as already used", func(t *testing.T) {
		v := newProofTestKApp(t, true)
		other := makeAddress("other")
		revokedKey := newTestBLSKey(t)
		nextKey := newTestBLSKey(t)
		resultCode, err := v.Register(other, newRegisterContractWithProof(other, revokedKey.pub, revokedKey.proof(t, other)))
		require.Equal(t, transaction.Transaction_Ok, resultCode)
		require.NoError(t, err)
		resultCode, err = v.UpdateValidator(other, newRotateContract(nextKey.pub, nextKey.proof(t, other)))
		require.Equal(t, transaction.Transaction_Ok, resultCode)
		require.NoError(t, err)

		resultCode, err = v.Register(owner, newRegisterContractWithProof(owner, revokedKey.pub, revokedKey.proof(t, owner)))

		assert.Equal(t, transaction.Transaction_AccountError, resultCode)
		assert.Equal(t, common.ErrAccountValidatorSet, err)
		requireLastErrorReceipt(t, v, common.ErrFieldBLSKeyAlreadyUsed, common.ErrAccountValidatorSet)
	})

	t.Run("revoked key without an owner", func(t *testing.T) {
		v := newProofTestKApp(t, true)
		key := newTestBLSKey(t)
		peerAcc, err := v.loadPeerAccount(key.pub)
		require.NoError(t, err)
		peerAcc.SetRevoked()

		resultCode, err := v.Register(owner, newRegisterContractWithProof(owner, key.pub, key.proof(t, owner)))

		assert.Equal(t, transaction.Transaction_AccountError, resultCode)
		assert.Equal(t, common.ErrAccountValidatorSet, err)
		requireLastErrorReceipt(t, v, common.ErrFieldBLSKeyRevoked, common.ErrAccountValidatorSet)
	})
}

func TestUpdateValidator_RefusalResults(t *testing.T) {
	t.Parallel()

	owner := makeAddress("owner")
	other := makeAddress("other")

	// setup registers owner with a fresh key and returns it
	setup := func(t *testing.T) (*validatorsKApp, testBLSKey) {
		v := newProofTestKApp(t, true)
		key := newTestBLSKey(t)
		resultCode, err := v.Register(owner, newRegisterContractWithProof(owner, key.pub, key.proof(t, owner)))
		require.Equal(t, transaction.Transaction_Ok, resultCode)
		require.NoError(t, err)

		return v, key
	}

	t.Run("reward address of the wrong length", func(t *testing.T) {
		v, key := setup(t)
		tc := newRotateContract(nil, nil)
		tc.Config.RewardAddress = []byte("short")

		resultCode, err := v.UpdateValidator(owner, tc)

		assert.Equal(t, transaction.Transaction_AccountError, resultCode)
		assert.Equal(t, process.ErrInvalidRcvAddr, err)
		requireLastErrorReceipt(t, v, common.ErrFieldInvalidRewardAddress, process.ErrInvalidRcvAddr)
		requireBound(t, v, owner, key.pub)
	})

	t.Run("invalid metadata", func(t *testing.T) {
		v, _ := setup(t)
		tc := newRotateContract(nil, nil)
		tc.Config.Name = strings.Repeat("n", core.MaxNameSize+1)

		resultCode, err := v.UpdateValidator(owner, tc)

		assert.Equal(t, transaction.Transaction_ParameterInvalid, resultCode)
		assert.Equal(t, common.ErrInvalidValue, err)
		requireLastErrorReceipt(t, v, common.ErrFieldInvalidName, common.ErrInvalidValue)
	})

	// rotateToRevokedWithBadLogo rotates owner's validator to a second key, then sends a rotation back
	// to the revoked first key with an invalid logo, and returns the second key
	rotateToRevokedWithBadLogo := func(t *testing.T, v6 bool) (*validatorsKApp, testBLSKey, transaction.Transaction_TXResultCode, error) {
		v, firstKey := setup(t)
		secondKey := newTestBLSKey(t)
		resultCode, err := v.UpdateValidator(owner, newRotateContract(secondKey.pub, secondKey.proof(t, owner)))
		require.Equal(t, transaction.Transaction_Ok, resultCode)
		require.NoError(t, err)
		setFixAuditChangesV6(v, v6)

		tc := newRotateContract(firstKey.pub, firstKey.proof(t, owner))
		tc.Config.Logo = invalidUTF8

		resultCode, err = v.UpdateValidator(owner, tc)
		return v, secondKey, resultCode, err
	}

	t.Run("rotation to a revoked key, with invalid metadata too, reports the metadata", func(t *testing.T) {
		v, secondKey, resultCode, err := rotateToRevokedWithBadLogo(t, true)

		assert.Equal(t, transaction.Transaction_ParameterInvalid, resultCode)
		assert.Equal(t, common.ErrInvalidValue, err)
		requireLastErrorReceipt(t, v, common.ErrFieldInvalidLogo, common.ErrInvalidValue)
		// the metadata is checked before the rotation, which never starts
		requireBound(t, v, owner, secondKey.pub)
	})

	t.Run("V6 off: rotation to a revoked key, with invalid metadata too, reports the key", func(t *testing.T) {
		v, _, resultCode, err := rotateToRevokedWithBadLogo(t, false)

		assert.Equal(t, transaction.Transaction_InvalidPeerKey, resultCode)
		assert.Equal(t, common.ErrBLSKeyRevoked, err)
		requireLastErrorReceipt(t, v, common.ErrFieldBLSKeyRevoked, common.ErrBLSKeyRevoked)
	})

	t.Run("rotation to a key bound to another validator", func(t *testing.T) {
		v, currentKey := setup(t)
		otherKey := newTestBLSKey(t)
		resultCode, err := v.Register(other, newRegisterContractWithProof(other, otherKey.pub, otherKey.proof(t, other)))
		require.Equal(t, transaction.Transaction_Ok, resultCode)
		require.NoError(t, err)

		// the test holds the other key, so the sender can present a valid proof bound to
		// itself: only the owner check refuses it
		resultCode, err = v.UpdateValidator(owner, newRotateContract(otherKey.pub, otherKey.proof(t, owner)))

		assert.Equal(t, transaction.Transaction_InvalidPeerKey, resultCode)
		assert.Equal(t, common.ErrAccountValidatorNotOwner, err)
		requireLastErrorReceipt(t, v, common.ErrFieldBLSKeyNotOwned, common.ErrAccountValidatorNotOwner)
		requireBound(t, v, owner, currentKey.pub)
		requireBound(t, v, other, otherKey.pub)
	})

	t.Run("V6 off: a refused rotation revokes the current key before its writes are discarded", func(t *testing.T) {
		v, currentKey := setup(t)
		otherKey := newTestBLSKey(t)
		resultCode, err := v.Register(other, newRegisterContractWithProof(other, otherKey.pub, otherKey.proof(t, other)))
		require.Equal(t, transaction.Transaction_Ok, resultCode)
		require.NoError(t, err)
		setFixAuditChangesV6(v, false)

		resultCode, err = v.UpdateValidator(owner, newRotateContract(otherKey.pub, nil))

		assert.Equal(t, transaction.Transaction_InvalidPeerKey, resultCode)
		assert.Equal(t, common.ErrAccountValidatorNotOwner, err)
		requireLastErrorReceipt(t, v, common.ErrFieldBLSKeyNotOwned, common.ErrAccountValidatorNotOwner)
		// pre-fork order: the tx processor discards this write with the failed transaction
		peerAcc, err := v.loadPeerAccount(currentKey.pub)
		require.NoError(t, err)
		assert.True(t, peerAcc.GetRevoked())
	})
}

func TestUpdateValidator_AppliesConfig(t *testing.T) {
	t.Parallel()

	owner := makeAddress("owner")
	v := newProofTestKApp(t, true)
	key := newTestBLSKey(t)
	resultCode, err := v.Register(owner, newRegisterContractWithProof(owner, key.pub, key.proof(t, owner)))
	require.Equal(t, transaction.Transaction_Ok, resultCode)
	require.NoError(t, err)

	getValidator := func() *ValidatorData {
		app, err := v.getKApp()
		require.NoError(t, err)
		val, err := v.getValidator(app, owner)
		require.NoError(t, err)

		return val
	}

	tc := newRotateContract(nil, nil)
	tc.Config.RewardAddress = makeAddress("reward")
	tc.Config.Logo = "https://logo"
	tc.Config.URIs = map[string]string{"site": "https://site"}
	tc.Config.Name = "validator"
	tc.Config.Commission = 2000
	tc.Config.MaxDelegationAmount = 5
	tc.Config.CanDelegate = true

	resultCode, err = v.UpdateValidator(owner, tc)
	require.Equal(t, transaction.Transaction_Ok, resultCode)
	require.NoError(t, err)

	val := getValidator()
	assert.Equal(t, makeAddress("reward"), val.RewardsAddress)
	assert.Equal(t, "https://logo", val.Logo)
	assert.Equal(t, map[string]string{"site": "https://site"}, val.URIs)
	assert.Equal(t, "validator", val.Name)
	assert.Equal(t, uint32(2000), val.Commission)
	assert.Equal(t, int64(5), val.MaxDelegation)
	assert.True(t, val.CanDelegate)

	// empty reward address, logo, URIs and name keep the current values
	resultCode, err = v.UpdateValidator(owner, newRotateContract(nil, nil))
	require.Equal(t, transaction.Transaction_Ok, resultCode)
	require.NoError(t, err)

	val = getValidator()
	assert.Equal(t, makeAddress("reward"), val.RewardsAddress)
	assert.Equal(t, "https://logo", val.Logo)
	assert.Equal(t, map[string]string{"site": "https://site"}, val.URIs)
	assert.Equal(t, "validator", val.Name)
	assert.Equal(t, uint32(1000), val.Commission)
	assert.False(t, val.CanDelegate)
}

func TestUpdateValidator_RotationPeerState(t *testing.T) {
	t.Parallel()

	owner := makeAddress("owner")

	// setup registers owner with a fresh key, marks its peer elected and sets FixStakingBuckets
	setup := func(t *testing.T, fixStakingBuckets bool) *validatorsKApp {
		v := newProofTestKApp(t, true)
		key := newTestBLSKey(t)
		resultCode, err := v.Register(owner, newRegisterContractWithProof(owner, key.pub, key.proof(t, owner)))
		require.Equal(t, transaction.Transaction_Ok, resultCode)
		require.NoError(t, err)

		oldPeer, err := v.loadPeerAccount(key.pub)
		require.NoError(t, err)
		oldPeer.SetListAndIndex(state.List_elected, 0)

		v.forkController.(*mock.ForkControllerStub).FixStakingBucketsValue = fixStakingBuckets

		return v
	}

	t.Run("FixStakingBuckets on: an elected validator's new key starts jailed", func(t *testing.T) {
		v := setup(t, true)
		newKey := newTestBLSKey(t)

		resultCode, err := v.UpdateValidator(owner, newRotateContract(newKey.pub, newKey.proof(t, owner)))
		require.Equal(t, transaction.Transaction_Ok, resultCode)
		require.NoError(t, err)

		newPeer, err := v.loadPeerAccount(newKey.pub)
		require.NoError(t, err)
		assert.Equal(t, state.List_jailed, newPeer.GetList())
		assert.Zero(t, newPeer.GetRating())
		assert.Zero(t, newPeer.GetTempRating())
		requireBound(t, v, owner, newKey.pub)
	})

	t.Run("FixStakingBuckets off: the new key does not take the old peer's state", func(t *testing.T) {
		v := setup(t, false)
		newKey := newTestBLSKey(t)

		resultCode, err := v.UpdateValidator(owner, newRotateContract(newKey.pub, newKey.proof(t, owner)))
		require.Equal(t, transaction.Transaction_Ok, resultCode)
		require.NoError(t, err)

		newPeer, err := v.loadPeerAccount(newKey.pub)
		require.NoError(t, err)
		assert.NotEqual(t, state.List_elected, newPeer.GetList())
		assert.NotEqual(t, state.List_jailed, newPeer.GetList())
		requireBound(t, v, owner, newKey.pub)
	})
}
