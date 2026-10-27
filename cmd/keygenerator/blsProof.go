package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"

	utils "github.com/klever-io/klever-go/cmd/operator/utils"
	"github.com/klever-io/klever-go/crypto/signing"
	"github.com/klever-io/klever-go/crypto/signing/mcl"
	"github.com/klever-io/klever-go/crypto/signing/mcl/singlesig"
	"github.com/klever-io/klever-go/data/transaction"
	"github.com/klever-io/klever-go/tools"
	"github.com/spf13/cobra"
)

const defaultValidatorKeyFile = "./validatorKey.pem"

// keyPasswordEnv is the variable the node reads the validator key password from
const keyPasswordEnv = "KEY_PASSWORD"

func newBLSProofCmd() *cobra.Command {
	var (
		keyFile string
		keyIdx  int
	)

	cmd := &cobra.Command{
		Use:   "bls-proof [OWNER_ADDRESS]",
		Short: "prove possession of a validator BLS key for an owner address",
		Long: `Signs, with the BLS private key of a validatorKey.pem, the owner-bound proof of possession
that validator create and validator config require from fork FixAuditChangesV6 on.

Run it on the node host: the BLS private key is read locally and never printed.
Pass the printed proof to the operator with --bls-proof and check that the printed
BLS public key is the one passed with --bls.

For 'validator create' the owner is the validator owner address, which from the same fork on
must also send the transaction. For 'validator config' it is the transaction sender: the
--fromAddress account when set, otherwise the signing wallet.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			password, err := utils.GetPassphrase(pwd, pwdFilePath)
			if err != nil {
				return err
			}
			if len(password) == 0 {
				password = os.Getenv(keyPasswordEnv)
			}

			pubKeyHex, proofHex, err := computeBLSKeyProof(keyFile, keyIdx, password, args[0])
			if err != nil {
				return err
			}

			fmt.Printf("BLS public key: %s\n", pubKeyHex)
			fmt.Printf("BLS proof: %s\n", proofHex)

			return nil
		},
	}

	cmd.Flags().StringVar(&keyFile, "key-file", defaultValidatorKeyFile, "validator key pem file --key-file=./validatorKey.pem")
	cmd.Flags().IntVar(&keyIdx, "key-index", 0, "0-based index of the key to use from the pem file")
	cmd.Flags().StringVarP(&pwd, "password", "p", "", "Password of the pem file; -p alone prompts for it, so it stays out of the shell history (default: $"+keyPasswordEnv+")")
	cmd.Flags().Lookup("password").NoOptDefVal = "*"

	return cmd
}

// computeBLSKeyProof loads the validator BLS key at skIndex of pemFile and signs the proof of possession of its
// public key for the bech32 owner address. It returns the hex BLS public key and the hex proof.
func computeBLSKeyProof(pemFile string, skIndex int, password string, ownerAddress string) (string, string, error) {
	owner, err := walletPubKeyConverter.Decode(ownerAddress)
	if err != nil {
		return "", "", fmt.Errorf("invalid owner address %s: %w", ownerAddress, err)
	}

	encodedSk, pkString, err := tools.LoadSkPkFromPemFile(pemFile, skIndex, password)
	if err != nil {
		return "", "", fmt.Errorf("loading validator key: %w", err)
	}

	// decode the label first, as the node does: a wallet or p2p pem fails here, before its key is parsed
	labelPubKey, err := validatorPubKeyConverter.Decode(pkString)
	if err != nil {
		return "", "", fmt.Errorf("%s is not a validator key file: %w for encoded public key %s", pemFile, err, pkString)
	}

	// the errors below do not wrap the cause: the decoders echo their input, which is the private key
	skBytes, err := hex.DecodeString(string(encodedSk))
	if err != nil {
		return "", "", fmt.Errorf("%s holds a secret key that is not hex", pemFile)
	}

	keyGen := signing.NewKeyGenerator(mcl.NewSuiteBLS12())
	sk, err := keyGen.PrivateKeyFromByteArray(skBytes)
	if err != nil {
		return "", "", fmt.Errorf("%s is not a validator BLS key", pemFile)
	}

	pubKeyBytes, err := sk.GeneratePublic().ToByteArray()
	if err != nil {
		return "", "", err
	}

	if !bytes.Equal(labelPubKey, pubKeyBytes) {
		return "", "", fmt.Errorf("public key in %s does not match its private key", pemFile)
	}

	proof, err := singlesig.NewBlsSigner().Sign(sk, transaction.BLSKeyProofMessage(owner, pubKeyBytes))
	if err != nil {
		return "", "", err
	}

	return hex.EncodeToString(pubKeyBytes), hex.EncodeToString(proof), nil
}
