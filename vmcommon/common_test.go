package vmcommon

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVMCommon_validateToken(t *testing.T) {
	var result bool
	result = ValidateToken([]byte("KLVRIDEFL-08d8e"))
	assert.False(t, result)
	result = ValidateToken([]byte("KLVRIDEFL-08d8e"))
	assert.False(t, result)
	result = ValidateToken([]byte("KLVRIDEFL08d8ef"))
	assert.False(t, result)
	result = ValidateToken([]byte("KLVRIDEFl-08d8ef"))
	assert.False(t, result)
	result = ValidateToken([]byte("KLVRIDEF*-08d8ef"))
	assert.False(t, result)
	result = ValidateToken([]byte("KLVRIDEFL-08d8eF"))
	assert.False(t, result)
	result = ValidateToken([]byte("KLVRIDEFL-08d*ef"))
	assert.False(t, result)

	result = ValidateToken([]byte("ALC6258d2"))
	assert.False(t, result)
	result = ValidateToken([]byte("AL-C6258"))
	assert.False(t, result)
	result = ValidateToken([]byte("alc-6258"))
	assert.False(t, result)
	result = ValidateToken([]byte("ALC-6258d2"))
	assert.False(t, result)
	result = ValidateToken([]byte("AL-6258"))
	assert.False(t, result)
	result = ValidateToken([]byte("ALCCCCCCCCC-6258"))
	assert.False(t, result)
	result = ValidateToken([]byte("KLVA"))
	assert.False(t, result)
	result = ValidateToken([]byte("KFIA"))
	assert.False(t, result)

	result = ValidateToken([]byte("KLVRIDEF2-08d8"))
	assert.True(t, result)
	result = ValidateToken([]byte("KLVRIDEFL-08d8"))
	assert.True(t, result)
	result = ValidateToken([]byte("ALC-6258"))
	assert.True(t, result)
	result = ValidateToken([]byte("ALC123-6258"))
	assert.True(t, result)
	result = ValidateToken([]byte("12345-6258"))
	assert.True(t, result)
	result = ValidateToken([]byte("KLV"))
	assert.True(t, result)
	result = ValidateToken([]byte("KFI"))
	assert.True(t, result)
}

func TestZeroValueIfNil(t *testing.T) {
	assert.Equal(t, big.NewInt(0), ZeroValueIfNil(nil))
	assert.Equal(t, big.NewInt(42), ZeroValueIfNil(big.NewInt(42)))
}

// ValidateTokenIdentifier answers 0, from FixAuditChangesV6, for a buffer longer than
// TokenIdentifierMaxLength without reading it, so nothing longer may ever be valid: not the
// official tokens either
func TestVMCommon_TokenIdentifierMaxLength(t *testing.T) {
	assert.Equal(t, identifierMaxLength, TokenIdentifierMaxLength)
	assert.LessOrEqual(t, len("KLV"), TokenIdentifierMaxLength)
	assert.LessOrEqual(t, len("KFI"), TokenIdentifierMaxLength)
	assert.True(t, ValidateToken([]byte("KLV")))
	assert.True(t, ValidateToken([]byte("KFI")))
	for _, tokenID := range []string{"ABCDEFGHIJK-1A2B", "ABCDEFGHIJ-1A2BC", "ABCDEFGHIJ--1A2B"} {
		assert.Greater(t, len(tokenID), TokenIdentifierMaxLength)
		assert.False(t, ValidateToken([]byte(tokenID)), tokenID)
	}
}
