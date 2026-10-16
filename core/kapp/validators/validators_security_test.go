package validators

import (
	"bytes"
	"testing"

	"github.com/klever-io/klever-go/common"
	"github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/core/kapp"
	"github.com/klever-io/klever-go/core/process"
	"github.com/klever-io/klever-go/crypto"
	"github.com/klever-io/klever-go/crypto/signing"
	"github.com/klever-io/klever-go/crypto/signing/mcl"
	"github.com/klever-io/klever-go/crypto/signing/mcl/singlesig"
	"github.com/klever-io/klever-go/data/block"
	"github.com/klever-io/klever-go/data/state"
	"github.com/klever-io/klever-go/data/transaction"
	"github.com/klever-io/klever-go/kvm/mock/stub"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realBLSValidator returns the production BLS12-381 G2 key validator.
func realBLSValidator() blsPublicKeyValidator {
	return signing.NewKeyGenerator(mcl.NewSuiteBLS12())
}

// validBLSPublicKey generates a well-formed BLS12-381 G2 public key.
func validBLSPublicKey(t *testing.T) []byte {
	t.Helper()

	keyGen := signing.NewKeyGenerator(mcl.NewSuiteBLS12())
	_, pub := keyGen.GeneratePair()
	pkBytes, err := pub.ToByteArray()
	require.NoError(t, err)
	require.NotEmpty(t, pkBytes)

	return pkBytes
}

func setFixAuditChangesV3(v *validatorsKApp, enabled bool) {
	v.forkController.(*mock.ForkControllerStub).FixAuditChangesV3Value = enabled
}

func newRegisterContract(owner, blsPubKey []byte) *transaction.CreateValidatorContract {
	return &transaction.CreateValidatorContract{
		OwnerAddress: owner,
		Config: &transaction.ValidatorConfig{
			RewardAddress:       owner,
			Commission:          1000,
			MaxDelegationAmount: 1000000,
			BLSPublicKey:        blsPubKey,
		},
	}
}

// TestNewValidatorKApp_DefaultsBLSKeyValidator proves that production (which does
// not inject a validator) gets the real BLS12-381 G2 validator by default, so the
// runtime registration path is actually protected.
func TestNewValidatorKApp_DefaultsBLSKeyValidator(t *testing.T) {
	args := createMockArgs()
	args.BLSKeyValidator = nil

	v, err := NewValidatorKApp(args)
	require.NoError(t, err)
	require.NotNil(t, v.blsKeyValidator)

	// the default validator must reject an arbitrary (non-G2) key
	require.Error(t, v.blsKeyValidator.CheckPublicKeyValid([]byte("not-a-bls-key")))
	// and accept a well-formed one
	require.NoError(t, v.blsKeyValidator.CheckPublicKeyValid(validBLSPublicKey(t)))
}

// TestRegister_BLSKeyValidation_GHSA_9wh6_9hq7_9688 covers the consensus-liveness
// DoS fix: runtime validator registration must reject BLS keys that are not valid
// G2 points once the FixAuditChangesV3 fork is active.
func TestRegister_BLSKeyValidation_GHSA_9wh6_9hq7_9688(t *testing.T) {
	t.Parallel()

	owner := makeAddress("owner")

	t.Run("rejects arbitrary (non-96-byte) key post-fork", func(t *testing.T) {
		v := setupValidatorsKApp(t)
		v.blsKeyValidator = realBLSValidator()
		addFunctionalCacher(t, v)
		addStorageCacher(v)
		addContext(v)
		setFixAuditChangesV3(v, true)

		resultCode, err := v.Register(owner, newRegisterContract(owner, []byte("blspubkey")))

		assert.Equal(t, transaction.Transaction_ParameterInvalid, resultCode)
		assert.Equal(t, common.ErrInvalidBLSPublicKey, err)
	})

	t.Run("rejects correct-length but malformed key post-fork", func(t *testing.T) {
		v := setupValidatorsKApp(t)
		v.blsKeyValidator = realBLSValidator()
		addFunctionalCacher(t, v)
		addStorageCacher(v)
		addContext(v)
		setFixAuditChangesV3(v, true)

		// same length as a real key, but not a valid curve point
		malformed := bytes.Repeat([]byte{0xFF}, len(validBLSPublicKey(t)))

		resultCode, err := v.Register(owner, newRegisterContract(owner, malformed))

		assert.Equal(t, transaction.Transaction_ParameterInvalid, resultCode)
		assert.Equal(t, common.ErrInvalidBLSPublicKey, err)
	})

	t.Run("accepts valid key post-fork", func(t *testing.T) {
		v := setupValidatorsKApp(t)
		v.blsKeyValidator = realBLSValidator()
		addFunctionalCacher(t, v)
		addStorageCacher(v)
		addContext(v)
		setFixAuditChangesV3(v, true)

		key := newTestBLSKey(t)
		resultCode, err := v.Register(owner, newRegisterContractWithProof(owner, key.pub, key.proof(t, owner)))

		assert.Equal(t, transaction.Transaction_Ok, resultCode)
		assert.NoError(t, err)
	})

	t.Run("accepts arbitrary key pre-fork (reprocessing determinism)", func(t *testing.T) {
		v := setupValidatorsKApp(t)
		v.blsKeyValidator = realBLSValidator()
		addFunctionalCacher(t, v)
		addStorageCacher(v)
		addContext(v)
		setFixAuditChangesV3(v, false)
		// a chain before FixAuditChangesV3 is also before FixAuditChangesV6
		setFixAuditChangesV6(v, false)

		resultCode, err := v.Register(owner, newRegisterContract(owner, []byte("blspubkey")))

		assert.Equal(t, transaction.Transaction_Ok, resultCode)
		assert.NoError(t, err)
	})
}

// TestUpdateValidator_BLSKeyValidation covers the config-update path, which also
// writes an unchecked BLS key before the fix.
func TestUpdateValidator_BLSKeyValidation(t *testing.T) {
	t.Parallel()

	owner := makeAddress("owner")

	t.Run("rejects switching to an invalid key post-fork", func(t *testing.T) {
		v := setupValidatorsKApp(t)
		v.blsKeyValidator = realBLSValidator()
		addFunctionalCacher(t, v)
		addStorageCacher(v)
		addContext(v)
		setFixAuditChangesV3(v, true)

		// register with a valid key first
		key := newTestBLSKey(t)
		resultCode, err := v.Register(owner, newRegisterContractWithProof(owner, key.pub, key.proof(t, owner)))
		require.Equal(t, transaction.Transaction_Ok, resultCode)
		require.NoError(t, err)

		// try to update to an invalid key
		update := &transaction.ValidatorConfigContract{
			Config: &transaction.ValidatorConfig{
				BLSPublicKey: []byte("newblspubkey"),
			},
		}

		resultCode, err = v.UpdateValidator(owner, update)

		assert.Equal(t, transaction.Transaction_InvalidPeerKey, resultCode)
		assert.Equal(t, common.ErrInvalidBLSPublicKey, err)
	})

	t.Run("accepts switching to a valid key post-fork", func(t *testing.T) {
		v := setupValidatorsKApp(t)
		v.blsKeyValidator = realBLSValidator()
		addFunctionalCacher(t, v)
		addStorageCacher(v)
		addContext(v)
		setFixAuditChangesV3(v, true)

		key := newTestBLSKey(t)
		resultCode, err := v.Register(owner, newRegisterContractWithProof(owner, key.pub, key.proof(t, owner)))
		require.Equal(t, transaction.Transaction_Ok, resultCode)
		require.NoError(t, err)

		newKey := newTestBLSKey(t)
		update := &transaction.ValidatorConfigContract{
			Config: &transaction.ValidatorConfig{
				BLSPublicKey: newKey.pub,
				BLSSignature: newKey.proof(t, owner),
			},
		}

		resultCode, err = v.UpdateValidator(owner, update)

		assert.Equal(t, transaction.Transaction_Ok, resultCode)
		assert.NoError(t, err)
	})
}

// KLC-2740: BLS key proof of possession. Register and a ValidatorConfig key rotation
// must carry a BLS signature, by the key being bound, over a message that binds the
// owner, so a pending CreateValidator cannot be front-run with the victim's key.

// testBLSKey is a real BLS12-381 key pair used to build proofs of possession.
type testBLSKey struct {
	priv crypto.PrivateKey
	pub  []byte
}

func newTestBLSKey(t *testing.T) testBLSKey {
	t.Helper()

	priv, pub := signing.NewKeyGenerator(mcl.NewSuiteBLS12()).GeneratePair()
	pubBytes, err := pub.ToByteArray()
	require.NoError(t, err)

	return testBLSKey{priv: priv, pub: pubBytes}
}

func (k testBLSKey) sign(t *testing.T, msg []byte) []byte {
	t.Helper()

	sig, err := singlesig.NewBlsSigner().Sign(k.priv, msg)
	require.NoError(t, err)

	return sig
}

// proof returns k's proof of possession of its own public key for owner.
func (k testBLSKey) proof(t *testing.T, owner []byte) []byte {
	t.Helper()

	return k.sign(t, transaction.BLSKeyProofMessage(owner, k.pub))
}

func setFixAuditChangesV6(v *validatorsKApp, enabled bool) {
	v.forkController.(*mock.ForkControllerStub).FixAuditChangesV6Value = enabled
}

func addContextWithNonce(v *validatorsKApp, nonce uint64) {
	ctx := kapp.NewKappContext(kapp.ArgsNewKAppContext{
		ContractID:     0,
		Block:          &block.Block{Header: &block.BlockHeader{Nonce: nonce}},
		TxHash:         make([]byte, 32),
		IsScSimulation: true,
	})
	v.KAppController = &stub.KAppControllerStub{
		GetCurrentKAppContextCalled: func() kapp.KappContext {
			return ctx
		},
	}
}

func newProofTestKApp(t *testing.T, v6 bool) *validatorsKApp {
	t.Helper()

	v := setupValidatorsKApp(t)
	v.blsKeyValidator = realBLSValidator()
	addFunctionalCacher(t, v)
	addStorageCacher(v)
	addContext(v)
	setFixAuditChangesV6(v, v6)

	return v
}

func newRegisterContractWithProof(owner, blsPubKey, proof []byte) *transaction.CreateValidatorContract {
	tc := newRegisterContract(owner, blsPubKey)
	tc.Config.BLSSignature = proof

	return tc
}

func newRotateContract(blsPubKey, proof []byte) *transaction.ValidatorConfigContract {
	return &transaction.ValidatorConfigContract{
		Config: &transaction.ValidatorConfig{
			BLSPublicKey: blsPubKey,
			BLSSignature: proof,
			Commission:   1000,
		},
	}
}

// requireBound asserts that key is bound to owner, both in the validator record and
// in the peer account, and that the peer account is not revoked.
func requireBound(t *testing.T, v *validatorsKApp, owner, key []byte) {
	t.Helper()

	app, err := v.getKApp()
	require.NoError(t, err)
	val, err := v.getValidator(app, owner)
	require.NoError(t, err)
	require.Equal(t, key, val.BlsPubKey)

	peerAcc, err := v.loadPeerAccount(key)
	require.NoError(t, err)
	require.Equal(t, owner, peerAcc.GetOwnerAddress())
	require.False(t, peerAcc.GetRevoked(), "bound key must not be revoked")
}

func requireNotRegistered(t *testing.T, v *validatorsKApp, owner, key []byte) {
	t.Helper()

	app, err := v.getKApp()
	require.NoError(t, err)
	_, err = v.getValidator(app, owner)
	require.Equal(t, common.ErrValidatorNotFound, err)

	peerAcc, err := v.loadPeerAccount(key)
	require.NoError(t, err)
	require.Empty(t, peerAcc.GetOwnerAddress())
	require.False(t, peerAcc.GetRevoked())
}

// TestBLSKeyProofSize_MatchesSignerSignatureSize pins the mcl-free copy of the proof
// length used by the tx builder and the operator to the signer's real size.
func TestBLSKeyProofSize_MatchesSignerSignatureSize(t *testing.T) {
	require.Equal(t, singlesig.SignatureSize, transaction.BLSKeyProofSize)

	owner := makeAddress("owner")
	key := newTestBLSKey(t)
	require.Len(t, key.proof(t, owner), transaction.BLSKeyProofSize)
}

func TestRegister_BLSKeyProof_KLC2740(t *testing.T) {
	t.Parallel()

	owner := makeAddress("owner")
	attacker := makeAddress("attacker")

	t.Run("V6 on: valid proof is accepted", func(t *testing.T) {
		v := newProofTestKApp(t, true)
		key := newTestBLSKey(t)

		resultCode, err := v.Register(owner, newRegisterContractWithProof(owner, key.pub, key.proof(t, owner)))

		require.Equal(t, transaction.Transaction_Ok, resultCode)
		require.NoError(t, err)
		requireBound(t, v, owner, key.pub)
	})

	t.Run("V6 on: missing proof is refused", func(t *testing.T) {
		v := newProofTestKApp(t, true)
		key := newTestBLSKey(t)

		resultCode, err := v.Register(owner, newRegisterContract(owner, key.pub))

		assert.Equal(t, transaction.Transaction_AccountError, resultCode)
		assert.Equal(t, common.ErrInvalidBLSKeyProof, err)
		requireNotRegistered(t, v, owner, key.pub)
	})

	t.Run("V6 on: garbage proof is refused", func(t *testing.T) {
		v := newProofTestKApp(t, true)
		key := newTestBLSKey(t)

		for _, garbage := range [][]byte{
			[]byte("garbage"),
			bytes.Repeat([]byte{0xFF}, singlesig.SignatureSize),
			make([]byte, singlesig.SignatureSize),
		} {
			resultCode, err := v.Register(owner, newRegisterContractWithProof(owner, key.pub, garbage))

			assert.Equal(t, transaction.Transaction_AccountError, resultCode)
			assert.Equal(t, common.ErrInvalidBLSKeyProof, err)
		}
		requireNotRegistered(t, v, owner, key.pub)
	})

	t.Run("V6 on: proof signed by a different key is refused", func(t *testing.T) {
		v := newProofTestKApp(t, true)
		key := newTestBLSKey(t)
		other := newTestBLSKey(t)

		// other signs the message for key: a valid signature, by the wrong key
		proof := other.sign(t, transaction.BLSKeyProofMessage(owner, key.pub))
		resultCode, err := v.Register(owner, newRegisterContractWithProof(owner, key.pub, proof))

		assert.Equal(t, transaction.Transaction_AccountError, resultCode)
		assert.Equal(t, common.ErrInvalidBLSKeyProof, err)
		requireNotRegistered(t, v, owner, key.pub)
	})

	t.Run("V6 on: proof over another message is refused", func(t *testing.T) {
		v := newProofTestKApp(t, true)
		key := newTestBLSKey(t)

		for name, msg := range map[string][]byte{
			"pubkey only":       key.pub,
			"owner and pubkey":  append(append([]byte{}, owner...), key.pub...),
			"tag and pubkey":    append([]byte(transaction.BLSKeyProofDomainTag), key.pub...),
			"pubkey then owner": append(append([]byte(transaction.BLSKeyProofDomainTag), key.pub...), owner...),
			"other tag":         append(append([]byte("KLEVER_BLS_KEY_PROOF_OF_POSSESSION_V2"), owner...), key.pub...),
			"arbitrary payload": []byte("block header"),
			"other owner":       transaction.BLSKeyProofMessage(attacker, key.pub),
		} {
			resultCode, err := v.Register(owner, newRegisterContractWithProof(owner, key.pub, key.sign(t, msg)))

			assert.Equal(t, transaction.Transaction_AccountError, resultCode, name)
			assert.Equal(t, common.ErrInvalidBLSKeyProof, err, name)
		}
		requireNotRegistered(t, v, owner, key.pub)
	})

	t.Run("V6 on: genesis (nonce 0) registers without a proof", func(t *testing.T) {
		v := newProofTestKApp(t, true)
		addContextWithNonce(v, 0)
		key := newTestBLSKey(t)

		resultCode, err := v.Register(owner, newRegisterContract(owner, key.pub))

		require.Equal(t, transaction.Transaction_Ok, resultCode)
		require.NoError(t, err)
		requireBound(t, v, owner, key.pub)

		peerAcc, err := v.loadPeerAccount(key.pub)
		require.NoError(t, err)
		assert.Equal(t, state.List_elected, peerAcc.GetList())
	})

	t.Run("V6 off: missing or garbage proof is ignored (replay determinism)", func(t *testing.T) {
		for name, proof := range map[string][]byte{
			"missing": nil,
			"garbage": []byte("garbage"),
		} {
			v := newProofTestKApp(t, false)
			key := newTestBLSKey(t)

			tc := newRegisterContractWithProof(owner, key.pub, proof)
			resultCode, err := v.Register(tc.GetOwnerAddress(), tc)

			require.Equal(t, transaction.Transaction_Ok, resultCode, name)
			require.NoError(t, err, name)
			requireBound(t, v, owner, key.pub)

			app, err := v.getKApp()
			require.NoError(t, err)
			val, err := v.getValidator(app, owner)
			require.NoError(t, err)
			assert.Equal(t, uint64(1), val.RegisterNonce, name)
			assert.Equal(t, tc.Config.Commission, val.Commission, name)
			assert.Equal(t, tc.Config.MaxDelegationAmount, val.MaxDelegation, name)

			peerAcc, err := v.loadPeerAccount(key.pub)
			require.NoError(t, err)
			assert.Equal(t, key.pub, peerAcc.GetBLSPublicKey(), name)
			assert.Equal(t, state.List_inactive, peerAcc.GetList(), name)
		}
	})

	t.Run("V6 off: front-run with the victim's key still succeeds (pre-fork behaviour)", func(t *testing.T) {
		v := newProofTestKApp(t, false)
		victimKey := newTestBLSKey(t)

		resultCode, err := v.Register(attacker, newRegisterContractWithProof(attacker, victimKey.pub, victimKey.proof(t, owner)))

		require.Equal(t, transaction.Transaction_Ok, resultCode)
		require.NoError(t, err)
		requireBound(t, v, attacker, victimKey.pub)
	})
}

func TestUpdateValidator_BLSKeyProof_KLC2740(t *testing.T) {
	t.Parallel()

	owner := makeAddress("owner")
	other := makeAddress("other")

	// setup registers owner with a fresh key under V6, then sets V6 to v6
	setup := func(t *testing.T, v6 bool) (*validatorsKApp, testBLSKey) {
		v := newProofTestKApp(t, true)
		oldKey := newTestBLSKey(t)
		resultCode, err := v.Register(owner, newRegisterContractWithProof(owner, oldKey.pub, oldKey.proof(t, owner)))
		require.Equal(t, transaction.Transaction_Ok, resultCode)
		require.NoError(t, err)
		setFixAuditChangesV6(v, v6)

		return v, oldKey
	}

	t.Run("V6 on: rotation with a proof bound to the sender is accepted", func(t *testing.T) {
		v, oldKey := setup(t, true)
		newKey := newTestBLSKey(t)

		resultCode, err := v.UpdateValidator(owner, newRotateContract(newKey.pub, newKey.proof(t, owner)))

		require.Equal(t, transaction.Transaction_Ok, resultCode)
		require.NoError(t, err)
		requireBound(t, v, owner, newKey.pub)

		oldPeer, err := v.loadPeerAccount(oldKey.pub)
		require.NoError(t, err)
		assert.True(t, oldPeer.GetRevoked())
	})

	t.Run("V6 on: rotation without a valid proof is refused and the old key stays bound", func(t *testing.T) {
		newKey := newTestBLSKey(t)

		proofs := map[string]func(oldKey testBLSKey) []byte{
			"missing":     func(testBLSKey) []byte { return nil },
			"garbage":     func(testBLSKey) []byte { return []byte("garbage") },
			"wrong owner": func(testBLSKey) []byte { return newKey.proof(t, other) },
			"wrong key": func(testBLSKey) []byte {
				return newTestBLSKey(t).sign(t, transaction.BLSKeyProofMessage(owner, newKey.pub))
			},
			"pubkey only":     func(testBLSKey) []byte { return newKey.sign(t, newKey.pub) },
			"old key's proof": func(oldKey testBLSKey) []byte { return oldKey.proof(t, owner) },
		}

		for name, proof := range proofs {
			v, oldKey := setup(t, true)

			resultCode, err := v.UpdateValidator(owner, newRotateContract(newKey.pub, proof(oldKey)))

			assert.Equal(t, transaction.Transaction_InvalidPeerKey, resultCode, name)
			assert.Equal(t, common.ErrInvalidBLSKeyProof, err, name)
			requireBound(t, v, owner, oldKey.pub)

			newPeer, err := v.loadPeerAccount(newKey.pub)
			require.NoError(t, err)
			assert.Empty(t, newPeer.GetOwnerAddress(), name)
		}
	})

	t.Run("V6 on: config-only updates need no proof", func(t *testing.T) {
		v, oldKey := setup(t, true)

		for name, key := range map[string][]byte{
			"empty key": nil,
			"same key":  oldKey.pub,
		} {
			tc := newRotateContract(key, nil)
			tc.Config.Commission = 2000

			resultCode, err := v.UpdateValidator(owner, tc)

			require.Equal(t, transaction.Transaction_Ok, resultCode, name)
			require.NoError(t, err, name)
			requireBound(t, v, owner, oldKey.pub)
		}
	})

	t.Run("V6 off: rotation without a proof is accepted (replay determinism)", func(t *testing.T) {
		v, oldKey := setup(t, false)
		newKey := newTestBLSKey(t)

		resultCode, err := v.UpdateValidator(owner, newRotateContract(newKey.pub, nil))

		require.Equal(t, transaction.Transaction_Ok, resultCode)
		require.NoError(t, err)
		requireBound(t, v, owner, newKey.pub)

		oldPeer, err := v.loadPeerAccount(oldKey.pub)
		require.NoError(t, err)
		assert.True(t, oldPeer.GetRevoked())
	})

}

// TestBLSKeyProof_KLC2740_TicketReproduction replays the ticket: an attacker who sees
// a pending CreateValidator for key K registers K first, then rotates away from K,
// which revokes K forever. With V6 on, the first step already fails.
func TestBLSKeyProof_KLC2740_TicketReproduction(t *testing.T) {
	t.Parallel()

	victim := makeAddress("victim")
	attacker := makeAddress("attacker")

	v := newProofTestKApp(t, true)
	victimKey := newTestBLSKey(t)
	victimProof := victimKey.proof(t, victim)

	// 1. the attacker front-runs the victim's pending tx with K, with and without the copied proof
	for _, proof := range [][]byte{nil, victimProof} {
		resultCode, err := v.Register(attacker, newRegisterContractWithProof(attacker, victimKey.pub, proof))
		require.Equal(t, transaction.Transaction_AccountError, resultCode)
		require.Equal(t, common.ErrInvalidBLSKeyProof, err)
	}
	requireNotRegistered(t, v, attacker, victimKey.pub)

	// 1b. nor can it resend the victim's exact contract, owned by the victim, with its own terms
	copied := newRegisterContractWithProof(victim, victimKey.pub, victimProof)
	copied.Config.RewardAddress = attacker
	resultCode, err := v.Register(attacker, copied)
	require.Equal(t, transaction.Transaction_AccountError, resultCode)
	require.Equal(t, common.ErrValidatorOwnerNotSender, err)
	requireNotRegistered(t, v, victim, victimKey.pub)

	// 2. so the attacker has no validator holding K, and nothing to rotate K away from
	attackerKey := newTestBLSKey(t)
	resultCode, err = v.UpdateValidator(attacker, newRotateContract(attackerKey.pub, attackerKey.proof(t, attacker)))
	require.Equal(t, transaction.Transaction_AccountError, resultCode)
	require.Equal(t, common.ErrValidatorNotFound, err)

	// 3. the victim's own transaction goes through and K stays live
	resultCode, err = v.Register(victim, newRegisterContractWithProof(victim, victimKey.pub, victimProof))
	require.Equal(t, transaction.Transaction_Ok, resultCode)
	require.NoError(t, err)
	requireBound(t, v, victim, victimKey.pub)

	// 4. an attacker with its own validator cannot take K over by rotation either
	resultCode, err = v.Register(attacker, newRegisterContractWithProof(attacker, attackerKey.pub, attackerKey.proof(t, attacker)))
	require.Equal(t, transaction.Transaction_Ok, resultCode)
	require.NoError(t, err)

	resultCode, err = v.UpdateValidator(attacker, newRotateContract(victimKey.pub, victimProof))
	require.Equal(t, transaction.Transaction_InvalidPeerKey, resultCode)
	require.Equal(t, common.ErrInvalidBLSKeyProof, err)
	requireBound(t, v, victim, victimKey.pub)
	requireBound(t, v, attacker, attackerKey.pub)
}

// TestRegister_OwnerMustBeSender_KLC2740 pins that from FixAuditChangesV6 on a validator is
// registered only by its owner, so a proof copied from a pending CreateValidator cannot be
// resent by another account with the victim as owner and the sender's terms.
func TestRegister_OwnerMustBeSender_KLC2740(t *testing.T) {
	t.Parallel()

	owner := makeAddress("owner")
	sender := makeAddress("sender")

	t.Run("V6 on: another sender is refused before any state change", func(t *testing.T) {
		v := newProofTestKApp(t, true)
		key := newTestBLSKey(t)

		resultCode, err := v.Register(sender, newRegisterContractWithProof(owner, key.pub, key.proof(t, owner)))

		assert.Equal(t, transaction.Transaction_AccountError, resultCode)
		assert.Equal(t, common.ErrValidatorOwnerNotSender, err)
		requireLastErrorReceipt(t, v, common.ErrFieldInvalidOwnerAddress, common.ErrValidatorOwnerNotSender)
		requireNotRegistered(t, v, owner, key.pub)
	})

	t.Run("V6 on: an owner address of the wrong length keeps its own error", func(t *testing.T) {
		v := newProofTestKApp(t, true)
		key := newTestBLSKey(t)

		resultCode, err := v.Register(sender, newRegisterContractWithProof([]byte("short"), key.pub, key.proof(t, owner)))

		assert.Equal(t, transaction.Transaction_AccountError, resultCode)
		assert.Equal(t, process.ErrInvalidRcvAddr, err)
		requireLastErrorReceipt(t, v, common.ErrFieldInvalidOwnerAddress, process.ErrInvalidRcvAddr)
	})

	t.Run("V6 off: another sender still registers (pre-fork behaviour)", func(t *testing.T) {
		v := newProofTestKApp(t, false)
		key := newTestBLSKey(t)

		resultCode, err := v.Register(sender, newRegisterContract(owner, key.pub))

		require.Equal(t, transaction.Transaction_Ok, resultCode)
		require.NoError(t, err)
		requireBound(t, v, owner, key.pub)
	})

	t.Run("genesis: validators are registered whatever the sender", func(t *testing.T) {
		v := newProofTestKApp(t, true)
		addContextWithNonce(v, 0)
		key := newTestBLSKey(t)

		resultCode, err := v.Register(nil, newRegisterContract(owner, key.pub))

		require.Equal(t, transaction.Transaction_Ok, resultCode)
		require.NoError(t, err)
		requireBound(t, v, owner, key.pub)
	})
}
