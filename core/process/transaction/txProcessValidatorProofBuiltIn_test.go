package transaction_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/klever-io/klever-go/common"
	commonMock "github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/config"
	"github.com/klever-io/klever-go/core/fork"
	"github.com/klever-io/klever-go/core/kapp"
	"github.com/klever-io/klever-go/core/kapp/builtInFunctions"
	"github.com/klever-io/klever-go/data/state"
	"github.com/klever-io/klever-go/vmcommon"
)

// KLC-2740: the CreateValidator and ValidatorConfig built-ins reach the same validators kapp as the
// native transactions, so from FixAuditChangesV6 on they must refuse a missing or foreign BLS proof
// of possession too. These tests run the real built-ins on top of the real validators kapp.

// builtInValidatorProofEnv wires the real built-ins to the real kapp controller of c.
type builtInValidatorProofEnv struct {
	createFn    vmcommon.BuiltinFunction
	validatorFn vmcommon.BuiltinFunction
}

func newBuiltInValidatorProofEnv(t *testing.T, c *Controller) *builtInValidatorProofEnv {
	t.Helper()

	// A real block context of nonce > 0: the proof check is skipped at genesis (nonce 0).
	block := createBlockHeader()
	require.Greater(t, block.GetHeader().GetNonce(), uint64(0))

	c.kappController.SetCurrentKAppContext(kapp.NewKappContext(kapp.ArgsNewKAppContext{
		OriginalSender: testOwnerAddress,
		ContractID:     0,
		Block:          block,
	}))

	forkController, err := fork.NewForkController(config.EnableEpochs{}, &commonMock.EpochNotifierStub{})
	require.NoError(t, err)

	createFn, err := builtInFunctions.NewKleverCreateValidatorFunc(
		0, &commonMock.ProtoMarshalizerMock{}, c.accCacher, forkController, c.kappController)
	require.NoError(t, err)

	validatorFn, err := builtInFunctions.NewKleverValidatorConfigFunc(
		0, &commonMock.ProtoMarshalizerMock{}, c.accCacher, forkController, c.kappController)
	require.NoError(t, err)

	return &builtInValidatorProofEnv{createFn: createFn, validatorFn: validatorFn}
}

// createValidatorInput builds the CreateValidator arguments: owner, bls key, reward address, name,
// canDelegate, commission, max delegation, logo and, when withProof, the proof as the 9th argument.
func createValidatorInput(owner, blsKey, proof []byte, withProof bool) *vmcommon.ContractCallInput {
	args := [][]byte{
		owner,
		blsKey,
		testReferralAddress,
		[]byte("validator"),
		{1},
		{0x01, 0xf4},
		{0x0a},
		[]byte("logo"),
	}
	if withProof {
		args = append(args, proof)
	}

	return &vmcommon.ContractCallInput{
		VMInput: vmcommon.VMInput{CallerAddr: owner, Arguments: args},
	}
}

// validatorConfigInput builds the ValidatorConfig arguments: bls key, reward address, name,
// canDelegate, commission, max delegation, logo and, when withProof, the proof as the 8th argument.
func validatorConfigInput(caller, blsKey, proof []byte, withProof bool) *vmcommon.ContractCallInput {
	args := [][]byte{
		blsKey,
		testReferralAddress,
		[]byte("validator"),
		{1},
		{0x01, 0xf4},
		{0x0a},
		[]byte("logo"),
	}
	if withProof {
		args = append(args, proof)
	}

	return &vmcommon.ContractCallInput{
		VMInput: vmcommon.VMInput{CallerAddr: caller, Arguments: args},
	}
}

func peerOf(c *Controller, blsKey []byte) state.PeerAccountHandler {
	return loadPeerAccount(c.accCacher, blsKey)
}

func TestCreateValidatorBuiltIn_V6_RealKapp(t *testing.T) {
	c := NewController(t)
	env := newBuiltInValidatorProofEnv(t, c)

	t.Run("no proof argument is refused and registers nothing", func(t *testing.T) {
		out, err := env.createFn.ProcessBuiltinFunction(createValidatorInput(testOwnerAddress, peerAddress, nil, false))
		assert.ErrorIs(t, err, common.ErrInvalidBLSKeyProof)
		assert.Nil(t, out)
		assert.Empty(t, peerOf(c, peerAddress).GetOwnerAddress())
	})

	t.Run("empty proof argument is refused and registers nothing", func(t *testing.T) {
		out, err := env.createFn.ProcessBuiltinFunction(createValidatorInput(testOwnerAddress, peerAddress, []byte{}, true))
		assert.ErrorIs(t, err, common.ErrInvalidBLSKeyProof)
		assert.Nil(t, out)
		assert.Empty(t, peerOf(c, peerAddress).GetOwnerAddress())
	})

	t.Run("proof bound to another owner is refused and registers nothing", func(t *testing.T) {
		foreign := validBLSKeyProof("peerAddress", accountForValidator)
		out, err := env.createFn.ProcessBuiltinFunction(createValidatorInput(testOwnerAddress, peerAddress, foreign, true))
		assert.ErrorIs(t, err, common.ErrInvalidBLSKeyProof)
		assert.Nil(t, out)
		assert.Empty(t, peerOf(c, peerAddress).GetOwnerAddress())
	})

	t.Run("proof of another key is refused and registers nothing", func(t *testing.T) {
		otherKey := validBLSKeyProof("peerAddress2", testOwnerAddress)
		out, err := env.createFn.ProcessBuiltinFunction(createValidatorInput(testOwnerAddress, peerAddress, otherKey, true))
		assert.ErrorIs(t, err, common.ErrInvalidBLSKeyProof)
		assert.Nil(t, out)
		assert.Empty(t, peerOf(c, peerAddress).GetOwnerAddress())
	})

	t.Run("caller other than the owner is refused and registers nothing", func(t *testing.T) {
		input := createValidatorInput(testOwnerAddress, peerAddress, validBLSKeyProof("peerAddress", testOwnerAddress), true)
		input.CallerAddr = accountForValidator
		out, err := env.createFn.ProcessBuiltinFunction(input)
		assert.ErrorIs(t, err, common.ErrValidatorOwnerNotSender)
		assert.Nil(t, out)
		assert.Empty(t, peerOf(c, peerAddress).GetOwnerAddress())
	})

	t.Run("valid proof registers the validator", func(t *testing.T) {
		proof := validBLSKeyProof("peerAddress", testOwnerAddress)
		out, err := env.createFn.ProcessBuiltinFunction(createValidatorInput(testOwnerAddress, peerAddress, proof, true))
		require.NoError(t, err)
		require.NotNil(t, out)
		assert.Equal(t, vmcommon.Ok, out.ReturnCode)
		assert.Equal(t, testOwnerAddress, peerOf(c, peerAddress).GetOwnerAddress())
	})
}

func TestValidatorConfigBuiltIn_V6_RealKapp_KeyRotation(t *testing.T) {
	c := NewController(t)
	env := newBuiltInValidatorProofEnv(t, c)

	oldKey := peerAddress
	newKey := validBLSKey("newBLS")

	proof := validBLSKeyProof("peerAddress", testOwnerAddress)
	_, err := env.createFn.ProcessBuiltinFunction(createValidatorInput(testOwnerAddress, oldKey, proof, true))
	require.NoError(t, err)
	require.Equal(t, testOwnerAddress, peerOf(c, oldKey).GetOwnerAddress())

	requireOldKeyUntouched := func(t *testing.T) {
		t.Helper()
		old := peerOf(c, oldKey)
		assert.Equal(t, testOwnerAddress, old.GetOwnerAddress())
		assert.False(t, old.GetRevoked())
		assert.Empty(t, peerOf(c, newKey).GetOwnerAddress())
	}

	t.Run("rotation without proof is refused and keeps the old key", func(t *testing.T) {
		out, err := env.validatorFn.ProcessBuiltinFunction(validatorConfigInput(testOwnerAddress, newKey, nil, false))
		assert.ErrorIs(t, err, common.ErrInvalidBLSKeyProof)
		assert.Nil(t, out)
		requireOldKeyUntouched(t)
	})

	t.Run("rotation with a proof bound to another owner is refused and keeps the old key", func(t *testing.T) {
		foreign := validBLSKeyProof("newBLS", accountForValidator)
		out, err := env.validatorFn.ProcessBuiltinFunction(validatorConfigInput(testOwnerAddress, newKey, foreign, true))
		assert.ErrorIs(t, err, common.ErrInvalidBLSKeyProof)
		assert.Nil(t, out)
		requireOldKeyUntouched(t)
	})

	t.Run("rotation with a valid proof bound to the caller succeeds", func(t *testing.T) {
		valid := validBLSKeyProof("newBLS", testOwnerAddress)
		out, err := env.validatorFn.ProcessBuiltinFunction(validatorConfigInput(testOwnerAddress, newKey, valid, true))
		require.NoError(t, err)
		require.NotNil(t, out)
		assert.Equal(t, vmcommon.Ok, out.ReturnCode)
		assert.Equal(t, testOwnerAddress, peerOf(c, newKey).GetOwnerAddress())
		assert.True(t, peerOf(c, oldKey).GetRevoked())
	})
}

// Before FixAuditChangesV6 the proof is not required, so replay of old blocks is unchanged.
func TestCreateValidatorBuiltIn_BeforeV6_WithoutProof_Accepted(t *testing.T) {
	c := NewController(t)
	c.UpdateForkController(config.EnableEpochs{FixAuditChangesV6: math.MaxUint32})
	env := newBuiltInValidatorProofEnv(t, c)

	out, err := env.createFn.ProcessBuiltinFunction(createValidatorInput(testOwnerAddress, peerAddress, nil, false))
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, vmcommon.Ok, out.ReturnCode)
	assert.Equal(t, testOwnerAddress, peerOf(c, peerAddress).GetOwnerAddress())
}
