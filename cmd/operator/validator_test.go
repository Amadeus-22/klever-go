package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/klever-io/klever-go/crypto/signing"
	"github.com/klever-io/klever-go/crypto/signing/mcl"
	"github.com/klever-io/klever-go/crypto/signing/mcl/singlesig"
	"github.com/klever-io/klever-go/data/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testAddress returns a bech32 wallet address made of fill bytes
func testAddress(fill byte) string {
	return walletPubKeyConverter.Encode(bytes.Repeat([]byte{fill}, 32))
}

// testBLSKeyProof returns a fresh BLS public key and its proof of possession for ownerAddress, both hex
func testBLSKeyProof(t *testing.T, ownerAddress string) (string, string) {
	t.Helper()

	sk, pk := signing.NewKeyGenerator(mcl.NewSuiteBLS12()).GeneratePair()
	pkBytes, err := pk.ToByteArray()
	require.NoError(t, err)
	owner, err := walletPubKeyConverter.Decode(ownerAddress)
	require.NoError(t, err)

	proof, err := singlesig.NewBlsSigner().Sign(sk, transaction.BLSKeyProofMessage(owner, pkBytes))
	require.NoError(t, err)

	return hex.EncodeToString(pkBytes), hex.EncodeToString(proof)
}

// captureSendBody points the operator at a node that records the /transaction/send body and answers with an
// error, so the command stops before signing. It also resets signerAddress and permFrom after the test.
func captureSendBody(t *testing.T) *string {
	t.Helper()

	prevNodeAPI, prevSigner, prevPermFrom := nodeAPI, signerAddress, permFrom
	t.Cleanup(func() {
		nodeAPI, signerAddress, permFrom = prevNodeAPI, prevSigner, prevPermFrom
	})

	body := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/transaction/send", r.URL.Path)
		b, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		*body = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":"captured"}`))
	}))
	t.Cleanup(srv.Close)
	nodeAPI = srv.URL

	return body
}

// sentContract decodes the sender and the contract of a captured /transaction/send body
func sentContract(t *testing.T, body string) (string, map[string]interface{}) {
	t.Helper()

	var req struct {
		Sender   string                 `json:"sender"`
		Contract map[string]interface{} `json:"contract"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &req))

	return req.Sender, req.Contract
}

func TestDecodeHexLen(t *testing.T) {
	t.Parallel()

	valid := hex.EncodeToString(make([]byte, transaction.BLSKeyProofSize))

	b, err := decodeHexLen(valid, transaction.BLSKeyProofSize, "BLS proof")
	require.NoError(t, err)
	assert.Len(t, b, transaction.BLSKeyProofSize)

	for name, value := range map[string]string{
		"empty":      "",
		"not hex":    strings.Repeat("zz", transaction.BLSKeyProofSize),
		"odd length": valid[:len(valid)-1],
		"too short":  valid[2:],
		"too long":   valid + "00",
	} {
		_, err = decodeHexLen(value, transaction.BLSKeyProofSize, "BLS proof")
		assert.Error(t, err, name)
	}
}

func TestCheckBLSProof(t *testing.T) {
	t.Parallel()

	owner := testAddress(0x07)
	key, proof := testBLSKeyProof(t, owner)

	t.Run("no proof is allowed", func(t *testing.T) {
		t.Parallel()

		assert.NoError(t, checkBLSProof(&validatorFlags{blsKey: key}, owner))
	})

	t.Run("proof of the key for the owner is allowed", func(t *testing.T) {
		t.Parallel()

		assert.NoError(t, checkBLSProof(&validatorFlags{blsKey: key, blsProof: proof}, owner))
	})

	t.Run("proof without a key is refused", func(t *testing.T) {
		t.Parallel()

		err := checkBLSProof(&validatorFlags{blsProof: proof}, owner)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--bls, which is missing")
	})

	t.Run("proof made for another address is refused", func(t *testing.T) {
		t.Parallel()

		err := checkBLSProof(&validatorFlags{blsKey: key, blsProof: proof}, testAddress(0x08))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is not a proof of --bls")
	})

	t.Run("proof of another key is refused", func(t *testing.T) {
		t.Parallel()

		otherKey, _ := testBLSKeyProof(t, owner)
		assert.Error(t, checkBLSProof(&validatorFlags{blsKey: otherKey, blsProof: proof}, owner))
	})

	t.Run("malformed proof is refused", func(t *testing.T) {
		t.Parallel()

		assert.Error(t, checkBLSProof(&validatorFlags{blsKey: key, blsProof: proof[2:]}, owner))
		assert.Error(t, checkBLSProof(&validatorFlags{blsKey: key, blsProof: "zz" + proof[2:]}, owner))
	})
}

func TestRunValidatorCreate_SendsTheProofForTheOwner(t *testing.T) {
	body := captureSendBody(t)
	owner := testAddress(0x07)
	key, proof := testBLSKeyProof(t, owner)

	err := runValidatorCreate(owner, &validatorFlags{blsKey: key, blsProof: proof, rewardsAddress: "OWNER", commission: 10})
	require.ErrorContains(t, err, "captured")

	_, contract := sentContract(t, *body)
	assert.Equal(t, key, contract["blsPublicKey"])
	assert.Equal(t, proof, contract["blsSignature"])
	assert.Equal(t, owner, contract["ownerAddress"])
}

func TestRunValidatorCreate_RefusesAProofForAnotherOwner(t *testing.T) {
	body := captureSendBody(t)
	key, proof := testBLSKeyProof(t, testAddress(0x08))

	err := runValidatorCreate(testAddress(0x07), &validatorFlags{blsKey: key, blsProof: proof, rewardsAddress: "OWNER", commission: 10})
	require.ErrorContains(t, err, "is not a proof of --bls")
	assert.Empty(t, *body)
}

func TestRunValidatorCreate_FromAddressMustBeTheOwnerWithAProof(t *testing.T) {
	owner, other := testAddress(0x07), testAddress(0x08)

	t.Run("another --fromAddress is refused before sending", func(t *testing.T) {
		body := captureSendBody(t)
		signerAddress, permFrom = other, other
		key, proof := testBLSKeyProof(t, owner)

		err := runValidatorCreate(owner, &validatorFlags{blsKey: key, blsProof: proof, rewardsAddress: "OWNER", commission: 10})
		require.ErrorContains(t, err, "is not the owner")
		assert.Empty(t, *body)
	})

	t.Run("the owner as --fromAddress is sent", func(t *testing.T) {
		body := captureSendBody(t)
		signerAddress, permFrom = other, owner
		key, proof := testBLSKeyProof(t, owner)

		err := runValidatorCreate(owner, &validatorFlags{blsKey: key, blsProof: proof, rewardsAddress: "OWNER", commission: 10})
		require.ErrorContains(t, err, "captured")

		sender, _ := sentContract(t, *body)
		assert.Equal(t, owner, sender)
	})

	t.Run("without a proof another --fromAddress is still sent, as before the fork", func(t *testing.T) {
		body := captureSendBody(t)
		signerAddress, permFrom = other, other
		key, _ := testBLSKeyProof(t, owner)

		err := runValidatorCreate(owner, &validatorFlags{blsKey: key, rewardsAddress: "OWNER", commission: 10})
		require.ErrorContains(t, err, "captured")

		sender, contract := sentContract(t, *body)
		assert.Equal(t, other, sender)
		assert.Equal(t, owner, contract["ownerAddress"])
	})
}

func TestRunValidatorConfig_ProofIsForTheSender(t *testing.T) {
	signer, from := testAddress(0x07), testAddress(0x08)

	t.Run("the signing wallet when --fromAddress is not set", func(t *testing.T) {
		body := captureSendBody(t)
		signerAddress, permFrom = signer, ""
		key, proof := testBLSKeyProof(t, signer)

		err := runValidatorConfig(signer, &validatorFlags{blsKey: key, blsProof: proof, rewardsAddress: "OWNER", commission: 10})
		require.ErrorContains(t, err, "captured")

		sender, contract := sentContract(t, *body)
		assert.Equal(t, signer, sender)
		assert.Equal(t, key, contract["blsPublicKey"])
		assert.Equal(t, proof, contract["blsSignature"])
	})

	t.Run("the --fromAddress account when set", func(t *testing.T) {
		body := captureSendBody(t)
		signerAddress, permFrom = signer, from
		key, proof := testBLSKeyProof(t, from)

		err := runValidatorConfig(from, &validatorFlags{blsKey: key, blsProof: proof, rewardsAddress: "OWNER", commission: 10})
		require.ErrorContains(t, err, "captured")

		sender, contract := sentContract(t, *body)
		assert.Equal(t, from, sender)
		assert.Equal(t, proof, contract["blsSignature"])
	})

	t.Run("a proof for the signer is refused when --fromAddress is set", func(t *testing.T) {
		body := captureSendBody(t)
		signerAddress, permFrom = signer, from
		key, proof := testBLSKeyProof(t, signer)

		err := runValidatorConfig(from, &validatorFlags{blsKey: key, blsProof: proof, rewardsAddress: "OWNER", commission: 10})
		require.ErrorContains(t, err, "is not a proof of --bls")
		assert.Empty(t, *body)
	})
}

func TestRunValidatorConfig_RefusesBadBLSFlags(t *testing.T) {
	body := captureSendBody(t)
	signer := testAddress(0x07)
	signerAddress, permFrom = signer, ""
	_, proof := testBLSKeyProof(t, signer)

	t.Run("proof without a key", func(t *testing.T) {
		err := runValidatorConfig(signer, &validatorFlags{blsProof: proof, rewardsAddress: "OWNER", commission: 10})
		assert.ErrorContains(t, err, "--bls, which is missing")
	})

	t.Run("key of the hex length that is not hex", func(t *testing.T) {
		err := runValidatorConfig(signer, &validatorFlags{blsKey: strings.Repeat("zz", blsPubkeyLen), rewardsAddress: "OWNER", commission: 10})
		assert.ErrorContains(t, err, "invalid BLS publicKey")
	})

	assert.Empty(t, *body)
}
