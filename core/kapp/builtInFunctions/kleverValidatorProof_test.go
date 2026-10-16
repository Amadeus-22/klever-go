package builtInFunctions

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/vmcommon"
)

// KLC-2740: the BLS proof of possession is the optional last positional argument of
// KleverCreateValidator (9th) and KleverValidatorConfig (8th).

func validatorProofArgs(withOwner bool, proof []byte) [][]byte {
	args := [][]byte{
		[]byte("bls-public-key"),
		[]byte("reward-address"),
		[]byte("validator-name"),
		{1},
		{0x01, 0xf4},
		{0x0a},
		[]byte("logo"),
	}
	if withOwner {
		args = append([][]byte{[]byte("owner-address")}, args...)
	}
	if proof != nil {
		args = append(args, proof)
	}

	return args
}

func newCreateValidatorForProofTest(t *testing.T) *kleverCreateValidator {
	t.Helper()

	e, err := NewKleverCreateValidatorFunc(0, &mock.ProtoMarshalizerMock{}, nil, &mock.ForkControllerStub{}, nil)
	require.NoError(t, err)

	return e
}

func newValidatorConfigForProofTest(t *testing.T) *kleverValidatorConfig {
	t.Helper()

	e, err := NewKleverValidatorConfigFunc(0, &mock.ProtoMarshalizerMock{}, nil, &mock.ForkControllerStub{}, nil)
	require.NoError(t, err)

	return e
}

func TestCreateValidator_GetContract_ReadsOptionalBLSSignature(t *testing.T) {
	t.Parallel()

	e := newCreateValidatorForProofTest(t)

	for _, proof := range [][]byte{[]byte("bls-signature-proof"), nil} {
		contract, err := e.getCreateValidatorContract(&vmcommon.ContractCallInput{
			VMInput: vmcommon.VMInput{Arguments: validatorProofArgs(true, proof)},
		})
		require.NoError(t, err)

		assert.Equal(t, []byte("owner-address"), contract.OwnerAddress)
		assert.Equal(t, "logo", contract.Config.Logo)
		assert.Equal(t, string(proof), string(contract.Config.BLSSignature))
	}
}

func TestValidatorConfig_GetContract_ReadsOptionalBLSSignature(t *testing.T) {
	t.Parallel()

	e := newValidatorConfigForProofTest(t)

	for _, proof := range [][]byte{[]byte("bls-signature-proof"), nil} {
		contract, err := e.getValidatorConfigContract(&vmcommon.ContractCallInput{
			VMInput: vmcommon.VMInput{Arguments: validatorProofArgs(false, proof)},
		})
		require.NoError(t, err)

		assert.Equal(t, []byte("bls-public-key"), contract.Config.BLSPublicKey)
		assert.Equal(t, "logo", contract.Config.Logo)
		assert.Equal(t, string(proof), string(contract.Config.BLSSignature))
	}
}
