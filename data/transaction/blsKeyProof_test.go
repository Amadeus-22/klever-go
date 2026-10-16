package transaction

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBLSKeyProofMessage pins the signed message layout (tag || owner || key): wallets
// and SDKs build the same bytes, so any change here breaks every existing proof.
func TestBLSKeyProofMessage(t *testing.T) {
	t.Parallel()

	owner := bytes.Repeat([]byte{0x01}, 32)
	blsPubKey := bytes.Repeat([]byte{0x02}, 96)

	expected := append([]byte("KLEVER_BLS_KEY_PROOF_OF_POSSESSION_V1"), owner...)
	expected = append(expected, blsPubKey...)

	assert.Equal(t, expected, BLSKeyProofMessage(owner, blsPubKey))
}
