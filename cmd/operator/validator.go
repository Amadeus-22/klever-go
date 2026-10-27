package main

import (
	"bytes"
	"encoding/hex"
	"fmt"

	"github.com/klever-io/klever-go/crypto/signing"
	"github.com/klever-io/klever-go/crypto/signing/mcl"
	"github.com/klever-io/klever-go/crypto/signing/mcl/singlesig"
	"github.com/klever-io/klever-go/data/transaction"
	"github.com/spf13/cobra"
)

const (
	blsPubKeyName          = "BLS publicKey"
	invalidOwnerAddressFmt = "invalid owner address %s: %w"
)

const blsProofUsage = "hex BLS key proof of possession, produced on the node host with keygenerator bls-proof; required from fork FixAuditChangesV6 on for registration and for key rotation"

// validatorFlags holds the flags shared by validator create and validator config
type validatorFlags struct {
	blsKey         string
	blsProof       string
	validatorName  string
	rewardsAddress string
	logo           string
	commission     float64
	maxDelegation  float64
	canDelegate    bool
	uris           map[string]string
}

func (f *validatorFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.blsKey, "bls", "", "set node BLS PublicKey")
	cmd.Flags().StringVar(&f.blsProof, "bls-proof", "", blsProofUsage)
	cmd.Flags().StringVar(&f.rewardsAddress, "rewards", "OWNER", "set validator rewards address")
	cmd.Flags().StringVar(&f.validatorName, "name", "", "set validator name")
	cmd.Flags().StringVar(&f.logo, "logo", "", "configure validator logo URI")
	cmd.Flags().Float64Var(&f.commission, "commission", 10.0, "set validator commission")
	cmd.Flags().Float64Var(&f.maxDelegation, "maxDelegation", 100_000_000, "set validator max delegation")
	cmd.Flags().StringToStringVar(&f.uris, "uris", nil, "validator uris")
	cmd.Flags().BoolVar(&f.canDelegate, "canDelegate", true, "set if validator can delegate")
}

// decodeHexLen decodes a hex flag value that must be size bytes long
func decodeHexLen(value string, size int, what string) ([]byte, error) {
	b, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", what, err)
	}
	if len(b) != size {
		return nil, fmt.Errorf("invalid %s length: %d <=> %d", what, len(b), size)
	}

	return b, nil
}

// checkBLSProof checks the optional BLS key proof against the BLS key and the address the node binds it to,
// so that a proof made for another key or address is refused before a fee is spent
func checkBLSProof(f *validatorFlags, ownerAddress string) error {
	if len(f.blsProof) == 0 {
		return nil
	}
	if len(f.blsKey) == 0 {
		return fmt.Errorf("--bls-proof proves the key given with --bls, which is missing")
	}

	proof, err := decodeHexLen(f.blsProof, transaction.BLSKeyProofSize, "BLS proof")
	if err != nil {
		return err
	}
	keyBytes, err := decodeHexLen(f.blsKey, blsPubkeyLen, blsPubKeyName)
	if err != nil {
		return err
	}
	owner, err := walletPubKeyConverter.Decode(ownerAddress)
	if err != nil {
		return fmt.Errorf("invalid BLS proof owner address %s: %w", ownerAddress, err)
	}

	pubKey, err := signing.NewKeyGenerator(mcl.NewSuiteBLS12()).PublicKeyFromByteArray(keyBytes)
	if err != nil {
		return fmt.Errorf("invalid BLS publicKey: %w", err)
	}
	err = singlesig.NewBlsSigner().Verify(pubKey, transaction.BLSKeyProofMessage(owner, keyBytes), proof)
	if err != nil {
		return fmt.Errorf("--bls-proof is not a proof of --bls for %s, run keygenerator bls-proof %s: %w", ownerAddress, ownerAddress, err)
	}

	return nil
}

// validateOwnerAndRewards checks the owner address and resolves the OWNER rewards address
func validateOwnerAndRewards(owner string, f *validatorFlags) error {
	_, err := walletPubKeyConverter.Decode(owner)
	if err != nil {
		return fmt.Errorf(invalidOwnerAddressFmt, owner, err)
	}

	if f.rewardsAddress == "OWNER" {
		f.rewardsAddress = owner
	}
	_, err = walletPubKeyConverter.Decode(f.rewardsAddress)
	if err != nil {
		return fmt.Errorf("invalid rewards address %s: %w", f.rewardsAddress, err)
	}

	return nil
}

func validateCommissionAndMaxDelegation(f *validatorFlags) error {
	if f.commission > 100 || f.commission < 0 {
		return fmt.Errorf("invalid commission, must be 0 <= commission <= 100")
	}

	if f.maxDelegation != 0 && f.maxDelegation < 10_000_000 {
		return fmt.Errorf("invalid commission, must be 0 or commission >= 10_000_000")
	}

	return nil
}

func runValidatorCreate(owner string, f *validatorFlags) error {
	err := validateOwnerAndRewards(owner, f)
	if err != nil {
		return err
	}

	_, err = decodeHexLen(f.blsKey, blsPubkeyLen, blsPubKeyName)
	if err != nil {
		return err
	}

	// the node binds a registration proof to the owner of the contract
	err = checkBLSProof(f, owner)
	if err != nil {
		return err
	}

	err = checkCreateSender(owner, f)
	if err != nil {
		return err
	}

	err = validateCommissionAndMaxDelegation(f)
	if err != nil {
		return err
	}

	return createValidator(owner, owner, f)
}

// checkCreateSender refuses a --fromAddress other than the owner when a proof is given. From fork
// FixAuditChangesV6 on, the node refuses a CreateValidator that the owner does not send, and the fee is still
// paid. The proof is only needed from that fork on, so without one a create for another owner is still sent.
func checkCreateSender(owner string, f *validatorFlags) error {
	if len(f.blsProof) == 0 || len(permFrom) == 0 {
		return nil
	}

	from, err := walletPubKeyConverter.Decode(permFrom)
	if err != nil {
		return fmt.Errorf("invalid --fromAddress %s: %w", permFrom, err)
	}
	ownerBytes, err := walletPubKeyConverter.Decode(owner)
	if err != nil {
		return fmt.Errorf(invalidOwnerAddressFmt, owner, err)
	}
	if !bytes.Equal(from, ownerBytes) {
		return fmt.Errorf("--fromAddress %s is not the owner %s, which must send the create from fork FixAuditChangesV6 on", permFrom, owner)
	}

	return nil
}

func runValidatorConfig(owner string, f *validatorFlags) error {
	err := validateOwnerAndRewards(owner, f)
	if err != nil {
		return err
	}

	if len(f.blsKey) != 0 {
		_, err = decodeHexLen(f.blsKey, blsPubkeyLen, blsPubKeyName)
		if err != nil {
			return err
		}
	}

	// the node binds a rotation proof to the transaction sender
	sender := signerAddress
	if len(permFrom) > 0 {
		sender = permFrom
	}
	err = checkBLSProof(f, sender)
	if err != nil {
		return err
	}

	err = validateCommissionAndMaxDelegation(f)
	if err != nil {
		return err
	}

	return validatorConfig(signerAddress, f)
}

func subValidator() []*cobra.Command {
	createFlags := &validatorFlags{}
	cmdCreate := &cobra.Command{
		Use:     "create [OWNER_ADDRESS]",
		Aliases: []string{"new"},
		Short:   "create a validator",
		Long: `create a validator

From fork FixAuditChangesV6 on, the owner must send the transaction: with --bls-proof, a --fromAddress
other than OWNER_ADDRESS is refused before anything is sent.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidatorCreate(args[0], createFlags)
		},
	}
	createFlags.bind(cmdCreate)

	configFlags := &validatorFlags{}
	cmdConfig := &cobra.Command{
		Use:     "config [OWNER_ADDRESS]",
		Aliases: []string{"c"},
		Short:   "config a validator",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidatorConfig(args[0], configFlags)
		},
	}
	configFlags.bind(cmdConfig)

	cmdUnjail := &cobra.Command{
		Use:     "unjail [OWNER_ADDRESS]",
		Aliases: []string{"uj"},
		Short:   "unjail validator",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// check owner address
			_, err := walletPubKeyConverter.Decode(args[0])
			if err != nil {
				return fmt.Errorf(invalidOwnerAddressFmt, args[0], err)
			}

			if len(multiSignKeys) == 0 && args[0] != signerAddress {
				return fmt.Errorf("owner and signer key does not match (%s: %s)", args[0], signerAddress)
			}

			return unjail(args[0])
		},
	}

	return []*cobra.Command{cmdCreate, cmdConfig, cmdUnjail}

}

func init() {
	cmdValidator := &cobra.Command{
		Use:   "validator",
		Short: "validator actions",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmdValidator.AddCommand(subValidator()...)
	rootCmd.AddCommand(cmdValidator)
}
