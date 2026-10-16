
# Keygenerator CLI

The **Key generation Tool** exposes the following Command Line Interface:

```
$ keygenerator --help

NAME:
   Key generation Tool - This binary will generate a validatorKey.pem, walletKey.pem or p2pKey.pem, each containing private key(s)
USAGE:
   keygenerator [global options]
   
AUTHOR:
   KleverIO <contact@klever.org>
   
GLOBAL OPTIONS:
   --num-keys, -n value       How many keys should generate. Example: 1 (default: 1)
   --key-type, -t value       What kind of keys should generate. Available options: validator, wallet, both, p2p (default: "validator")
   --console-out, -c          Boolean option that will enable printing the generated keys directly on the console
   --no-split, -s             Boolean option that will make each generated key added in the same file
   --password, -p value       Password encryption for generated file. Example: --password=MY_SECRET
   --password-file value      Path to a file containing the password for encryption
   --help, -h                 show help
   --version, -v              print the version
   

```

## bls-proof

Run on the node host. It reads the validator BLS private key from the pem file and prints the BLS public key and the owner-bound proof of possession (hex). The private key is never printed.

```
$ keygenerator bls-proof [OWNER_ADDRESS] [flags]

FLAGS:
   --key-file value       Validator key pem file (default: "./validatorKey.pem")
   --key-index value      0-based index of the key to use from the pem file (default: 0)
   --password, -p value   Password of the pem file (default: $KEY_PASSWORD)
   --password-file value  Path to a file containing the password
```

The proof is required from fork FixAuditChangesV6 on. For `operator validator create` the owner is the validator owner address, which from the same fork on must also send the transaction. For `operator validator config` it is the transaction sender: the `--fromAddress` account when set, otherwise the signing wallet. Pass the proof with `--bls-proof` and check that the printed BLS public key is the one given with `--bls`.

Give the password with `-p` alone, which prompts for it, with `--password-file`, or with `$KEY_PASSWORD`, rather than as `--password=...`, which leaves it in the shell history and the process list.
