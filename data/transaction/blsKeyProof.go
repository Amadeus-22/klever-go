package transaction

// BLSKeyProofDomainTag prefixes every BLS key proof-of-possession message. The same
// BLS key also signs marshalled block headers (consensus) and libp2p PID payloads
// (peer signatures); the tag keeps a proof from ever being equal to, or reused as,
// either of those messages.
const BLSKeyProofDomainTag = "KLEVER_BLS_KEY_PROOF_OF_POSSESSION_V1"

// BLSKeyProofSize is the length in bytes of a BLS key proof of possession: a
// compressed BLS12-381 G1 signature (singlesig.SignatureSize, kept here so callers
// without the mcl dependency can check it).
const BLSKeyProofSize = 48

// BLSKeyProofMessage returns the message a validator signs with its BLS private key
// to prove possession of blsPubKey for owner: tag || owner || blsPubKey. Binding the
// owner means a proof copied from a pending transaction cannot register or rotate
// the key under another address.
//
// The message is a plain concatenation without length prefixes: callers must
// validate the owner address length and the BLS public key before verifying a proof.
func BLSKeyProofMessage(owner, blsPubKey []byte) []byte {
	msg := make([]byte, 0, len(BLSKeyProofDomainTag)+len(owner)+len(blsPubKey))
	msg = append(msg, BLSKeyProofDomainTag...)
	msg = append(msg, owner...)
	msg = append(msg, blsPubKey...)

	return msg
}
