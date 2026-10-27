package transaction_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/klever-io/klever-go/common"
	"github.com/klever-io/klever-go/config"
	"github.com/klever-io/klever-go/core/process/kda/kdautils"
	"github.com/klever-io/klever-go/data/block"
	"github.com/klever-io/klever-go/data/transaction"
)

// KLC-2740: from FixAuditChangesV6 on, registering a validator or rotating its BLS key needs a
// proof of possession bound to the owner.

func freezeForValidator(c *Controller, blk *block.Block, owner []byte) {
	freezeContract := transaction.FreezeContract{
		AssetID: kdautils.KLVIdentifier,
		Amount:  MinFreezeAmount,
	}

	freezeTx, _ := createTransactionMock(&freezeContract, transaction.TXContract_FreezeContractType, owner, 0)
	_, freezeHash, err := c.execTx.PreProcessTransaction(freezeTx)
	require.Nil(c.t, err)

	err = c.execTx.ProcessTransaction(blk, freezeHash, freezeTx)
	require.Nil(c.t, err)
}

func runCreateValidatorWithProof(c *Controller, blk *block.Block, owner, blsKey, proof []byte) (*transaction.Transaction, error) {
	return runCreateValidatorFrom(c, blk, owner, owner, blsKey, proof)
}

func runCreateValidatorFrom(c *Controller, blk *block.Block, sender, owner, blsKey, proof []byte) (*transaction.Transaction, error) {
	contract := transaction.CreateValidatorContract{
		OwnerAddress: owner,
		Config: &transaction.ValidatorConfig{
			BLSPublicKey:        blsKey,
			BLSSignature:        proof,
			RewardAddress:       testReferralAddress,
			CanDelegate:         true,
			MaxDelegationAmount: 100_000_000_000,
		},
	}

	tx, _ := createTransactionMock(&contract, transaction.TXContract_CreateValidatorContractType, sender, 0)
	_, hash, err := c.execTx.PreProcessTransaction(tx)
	require.Nil(c.t, err)

	return tx, c.execTx.ProcessTransaction(blk, hash, tx)
}

func runValidatorConfigWithProof(c *Controller, blk *block.Block, sender, blsKey, proof []byte) (*transaction.Transaction, error) {
	contract := transaction.ValidatorConfigContract{
		Config: &transaction.ValidatorConfig{
			BLSPublicKey: blsKey,
			BLSSignature: proof,
		},
	}

	tx, _ := createTransactionMock(&contract, transaction.TXContract_ValidatorConfigContractType, sender, 0)
	_, hash, err := c.execTx.PreProcessTransaction(tx)
	require.Nil(c.t, err)

	return tx, c.execTx.ProcessTransaction(blk, hash, tx)
}

func TestCreateValidatorTxProcessor_V6_WithoutProof_Refused(t *testing.T) {
	c := NewController(t)
	c.AddUser(testOwnerAddress, 10_000_000_000, kdautils.KLVIdentifier)
	blk := createBlockHeader()
	freezeForValidator(c, blk, testOwnerAddress)

	tx, err := runCreateValidatorWithProof(c, blk, testOwnerAddress, peerAddress, nil)
	assert.ErrorIs(t, err, common.ErrInvalidBLSKeyProof)
	assert.Equal(t, transaction.Transaction_AccountError, tx.GetResultCode())

	// the refusal bound nothing, so the right proof is accepted afterwards
	_, err = runCreateValidatorWithProof(c, blk, testOwnerAddress, peerAddress, validBLSKeyProof("peerAddress", testOwnerAddress))
	assert.Nil(t, err)
}

func TestCreateValidatorTxProcessor_V6_FromAnotherSender_Refused(t *testing.T) {
	c := NewController(t)
	c.AddUser(testOwnerAddress, 10_000_000_000, kdautils.KLVIdentifier)
	c.AddUser(accountForValidator, 10_000_000_000, kdautils.KLVIdentifier)
	blk := createBlockHeader()
	freezeForValidator(c, blk, testOwnerAddress)
	proof := validBLSKeyProof("peerAddress", testOwnerAddress)

	// a copy of the owner's pending transaction, resent by another account
	tx, err := runCreateValidatorFrom(c, blk, accountForValidator, testOwnerAddress, peerAddress, proof)
	assert.ErrorIs(t, err, common.ErrValidatorOwnerNotSender)
	assert.Equal(t, transaction.Transaction_AccountError, tx.GetResultCode())

	// the refusal bound nothing, so the owner's own transaction goes through
	_, err = runCreateValidatorWithProof(c, blk, testOwnerAddress, peerAddress, proof)
	assert.Nil(t, err)
}

func TestValidatorConfigTxProcessor_V6_KeyRotation(t *testing.T) {
	c := NewController(t)
	c.AddUser(testOwnerAddress, 10_000_000_000, kdautils.KLVIdentifier)
	blk := createBlockHeader()
	freezeForValidator(c, blk, testOwnerAddress)

	firstKey := peerAddress
	_, err := runCreateValidatorWithProof(c, blk, testOwnerAddress, firstKey, validBLSKeyProof("peerAddress", testOwnerAddress))
	require.Nil(t, err)

	secondKey := validBLSKey("newBLS")

	tx, err := runValidatorConfigWithProof(c, blk, testOwnerAddress, secondKey, nil)
	assert.ErrorIs(t, err, common.ErrInvalidBLSKeyProof)
	assert.Equal(t, transaction.Transaction_InvalidPeerKey, tx.GetResultCode())

	_, err = runValidatorConfigWithProof(c, blk, testOwnerAddress, secondKey, validBLSKeyProof("newBLS", testOwnerAddress))
	require.Nil(t, err)
	require.True(t, peerOf(c, firstKey).GetRevoked())

	// rotating back to the revoked first key passes the proof check and is refused before the
	// current key is revoked, so the current key stays live
	tx, err = runValidatorConfigWithProof(c, blk, testOwnerAddress, firstKey, validBLSKeyProof("peerAddress", testOwnerAddress))
	assert.ErrorIs(t, err, common.ErrBLSKeyRevoked)
	assert.Equal(t, transaction.Transaction_InvalidPeerKey, tx.GetResultCode())

	current := peerOf(c, secondKey)
	assert.Equal(t, testOwnerAddress, current.GetOwnerAddress())
	assert.False(t, current.GetRevoked())
}

// Before FixAuditChangesV6 the proof is not required, so replay of old blocks is unchanged.
func TestCreateValidatorTxProcessor_BeforeV6_WithoutProof_Accepted(t *testing.T) {
	c := NewController(t)
	c.UpdateForkController(config.EnableEpochs{FixAuditChangesV6: math.MaxUint32})
	c.AddUser(testOwnerAddress, 10_000_000_000, kdautils.KLVIdentifier)
	blk := createBlockHeader()
	freezeForValidator(c, blk, testOwnerAddress)

	_, err := runCreateValidatorWithProof(c, blk, testOwnerAddress, peerAddress, nil)
	assert.Nil(t, err)
}

// Before FixAuditChangesV6 a refused rotation revokes the current key before it checks the new
// one; the failed transaction discards that write, so the current key stays live.
func TestValidatorConfigTxProcessor_BeforeV6_RefusedRotation_KeepsTheCurrentKey(t *testing.T) {
	c := NewController(t)
	c.UpdateForkController(config.EnableEpochs{FixAuditChangesV6: math.MaxUint32})
	c.AddUser(testOwnerAddress, 10_000_000_000, kdautils.KLVIdentifier)
	blk := createBlockHeader()
	freezeForValidator(c, blk, testOwnerAddress)

	firstKey := peerAddress
	_, err := runCreateValidatorWithProof(c, blk, testOwnerAddress, firstKey, nil)
	require.Nil(t, err)

	secondKey := validBLSKey("newBLS")
	_, err = runValidatorConfigWithProof(c, blk, testOwnerAddress, secondKey, nil)
	require.Nil(t, err)
	require.True(t, peerOf(c, firstKey).GetRevoked())

	tx, err := runValidatorConfigWithProof(c, blk, testOwnerAddress, firstKey, nil)
	assert.ErrorIs(t, err, common.ErrBLSKeyRevoked)
	assert.Equal(t, transaction.Transaction_InvalidPeerKey, tx.GetResultCode())

	current := peerOf(c, secondKey)
	assert.Equal(t, testOwnerAddress, current.GetOwnerAddress())
	assert.False(t, current.GetRevoked())
}
