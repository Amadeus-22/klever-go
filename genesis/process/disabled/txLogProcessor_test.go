package disabled

import (
	"testing"

	"github.com/klever-io/klever-go/core/process"
	"github.com/stretchr/testify/require"
)

// A saved log must never be readable back, so a discarded execution cannot leak logs into the block
func TestTxLogProcessor_DiscardsLogs(t *testing.T) {
	t.Parallel()

	tlp := &TxLogProcessor{}
	require.False(t, tlp.IsInterfaceNil())

	require.Nil(t, tlp.SaveLog([]byte("txHash"), nil, nil, 0, nil))

	l, err := tlp.GetLog([]byte("txHash"))
	require.Nil(t, l)
	require.ErrorIs(t, err, process.ErrLogNotFound)
	require.Empty(t, tlp.GetAllCurrentLogs())

	require.Nil(t, tlp.SaveLogsToStorage())
	tlp.Clean()
}
