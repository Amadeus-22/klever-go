package main

import (
	"bytes"
	"encoding/hex"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/klever-io/klever-go/crypto/signing"
	"github.com/klever-io/klever-go/crypto/signing/mcl"
	"github.com/klever-io/klever-go/crypto/signing/mcl/singlesig"
	"github.com/klever-io/klever-go/data/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testOwner(t *testing.T, fill byte) ([]byte, string) {
	t.Helper()

	owner := bytes.Repeat([]byte{fill}, txSignPubkeyLen)
	return owner, walletPubKeyConverter.Encode(owner)
}

// writeValidatorKeys saves numKeys fresh validator keys into one validatorKey.pem and returns its path and the keys
func writeValidatorKeys(t *testing.T, numKeys int, password string) (string, []key) {
	t.Helper()

	dir := t.TempDir()
	t.Chdir(dir)

	validatorKeys, _, err := generateKeys(validatorType, numKeys)
	require.NoError(t, err)
	require.NoError(t, saveKeys(validatorKeys, nil, true, password))

	return filepath.Join(dir, "validatorKey.pem"), validatorKeys
}

func verifyProof(t *testing.T, pubKeyHex string, owner []byte, proofHex string) error {
	t.Helper()

	pubBytes, err := hex.DecodeString(pubKeyHex)
	require.NoError(t, err)
	sig, err := hex.DecodeString(proofHex)
	require.NoError(t, err)

	keyGen := signing.NewKeyGenerator(mcl.NewSuiteBLS12())
	pub, err := keyGen.PublicKeyFromByteArray(pubBytes)
	require.NoError(t, err)

	return singlesig.NewBlsSigner().Verify(pub, transaction.BLSKeyProofMessage(owner, pubBytes), sig)
}

func TestComputeBLSKeyProof_ProofVerifiesForTheOwner(t *testing.T) {
	pemFile, keys := writeValidatorKeys(t, 1, "")
	owner, ownerBech32 := testOwner(t, 0x07)

	pubKeyHex, proofHex, err := computeBLSKeyProof(pemFile, 0, "", ownerBech32)
	require.NoError(t, err)

	assert.Equal(t, hex.EncodeToString(keys[0].pkBytes), pubKeyHex)
	assert.NoError(t, verifyProof(t, pubKeyHex, owner, proofHex))
}

func TestComputeBLSKeyProof_ProofIsBoundToTheOwner(t *testing.T) {
	pemFile, _ := writeValidatorKeys(t, 1, "")
	_, ownerBech32 := testOwner(t, 0x07)
	otherOwner, _ := testOwner(t, 0x08)

	pubKeyHex, proofHex, err := computeBLSKeyProof(pemFile, 0, "", ownerBech32)
	require.NoError(t, err)

	assert.Error(t, verifyProof(t, pubKeyHex, otherOwner, proofHex))
}

func TestComputeBLSKeyProof_UsesTheRequestedKeyIndex(t *testing.T) {
	pemFile, keys := writeValidatorKeys(t, 2, "")
	owner, ownerBech32 := testOwner(t, 0x07)

	pubKeyHex, proofHex, err := computeBLSKeyProof(pemFile, 1, "", ownerBech32)
	require.NoError(t, err)

	assert.Equal(t, hex.EncodeToString(keys[1].pkBytes), pubKeyHex)
	assert.NoError(t, verifyProof(t, pubKeyHex, owner, proofHex))

	_, _, err = computeBLSKeyProof(pemFile, 2, "", ownerBech32)
	assert.Error(t, err)
}

func TestComputeBLSKeyProof_EncryptedPemFile(t *testing.T) {
	pemFile, _ := writeValidatorKeys(t, 1, "secret")
	owner, ownerBech32 := testOwner(t, 0x07)

	pubKeyHex, proofHex, err := computeBLSKeyProof(pemFile, 0, "secret", ownerBech32)
	require.NoError(t, err)
	assert.NoError(t, verifyProof(t, pubKeyHex, owner, proofHex))

	_, _, err = computeBLSKeyProof(pemFile, 0, "", ownerBech32)
	assert.Error(t, err)

	_, _, err = computeBLSKeyProof(pemFile, 0, "wrong", ownerBech32)
	assert.Error(t, err)
}

func TestComputeBLSKeyProof_RejectsInvalidInput(t *testing.T) {
	pemFile, _ := writeValidatorKeys(t, 1, "")
	_, ownerBech32 := testOwner(t, 0x07)

	t.Run("owner that is not bech32", func(t *testing.T) {
		_, _, err := computeBLSKeyProof(pemFile, 0, "", "not-an-address")
		assert.Error(t, err)
	})

	t.Run("owner of the wrong length", func(t *testing.T) {
		short := walletPubKeyConverter.Encode(bytes.Repeat([]byte{0x01}, txSignPubkeyLen-1))
		_, _, err := computeBLSKeyProof(pemFile, 0, "", short)
		assert.Error(t, err)
	})

	t.Run("owner as hex", func(t *testing.T) {
		_, _, err := computeBLSKeyProof(pemFile, 0, "", hex.EncodeToString(bytes.Repeat([]byte{0x07}, txSignPubkeyLen)))
		assert.Error(t, err)
	})

	t.Run("missing pem file", func(t *testing.T) {
		_, _, err := computeBLSKeyProof(filepath.Join(t.TempDir(), "missing.pem"), 0, "", ownerBech32)
		assert.Error(t, err)
	})

	t.Run("negative key index", func(t *testing.T) {
		_, _, err := computeBLSKeyProof(pemFile, -1, "", ownerBech32)
		assert.Error(t, err)
	})
}

func TestComputeBLSKeyProof_RejectsAWalletKeyFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	_, walletKeys, err := generateKeys(walletType, 1)
	require.NoError(t, err)
	require.NoError(t, saveKeys(nil, walletKeys, true, ""))

	_, ownerBech32 := testOwner(t, 0x07)
	_, _, err = computeBLSKeyProof(filepath.Join(dir, "walletKey.pem"), 0, "", ownerBech32)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), hex.EncodeToString(walletKeys[0].skBytes))
}

func TestComputeBLSKeyProof_ErrorsDoNotCarryTheKey(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// a pem whose label decodes as a validator public key but whose key is not a BLS scalar
	sk := bytes.Repeat([]byte{0xff}, 32)
	label := hex.EncodeToString(bytes.Repeat([]byte{0x01}, blsPubkeyLen))
	pemFile := filepath.Join(dir, "bad.pem")
	block := &pem.Block{Type: "PRIVATE KEY for " + label, Bytes: []byte(hex.EncodeToString(sk))}
	require.NoError(t, os.WriteFile(pemFile, pem.EncodeToMemory(block), 0o600))

	_, ownerBech32 := testOwner(t, 0x07)
	_, _, err := computeBLSKeyProof(pemFile, 0, "", ownerBech32)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a validator BLS key")
	assert.NotContains(t, err.Error(), hex.EncodeToString(sk))
}

// rootArgsError runs the steps cobra takes before RunE: find the command, parse its flags, validate the rest
func rootArgsError(t *testing.T, args []string) error {
	t.Helper()

	cmd, rest, err := rootCmd.Find(args)
	require.NoError(t, err, args)
	require.Equal(t, rootCmd, cmd, args)
	require.NoError(t, cmd.ParseFlags(rest), args)

	return cmd.ValidateArgs(cmd.Flags().Args())
}

func TestRootCmd_KeepsCombinedShortFlagsAndRefusesPositionals(t *testing.T) {
	saved := *argsConfig
	t.Cleanup(func() { *argsConfig = saved })

	for _, args := range [][]string{
		{"-sn", "2"},
		{"-ct", "wallet"},
		{"-sn", "2", "-c"},
	} {
		assert.NoError(t, rootArgsError(t, args), args)
	}

	// a mistyped subcommand must not fall through to the root, which would generate keys
	for _, args := range [][]string{
		{"bls-prof", "klv1owner"},
		{"stray", "-c"},
	} {
		err := rootArgsError(t, args)
		require.Error(t, err, args)
		assert.Contains(t, err.Error(), "unknown command", args)
	}

	cmd, _, err := rootCmd.Find([]string{"bls-proof", "klv1owner"})
	require.NoError(t, err)
	assert.Equal(t, "bls-proof", cmd.Name())
}
