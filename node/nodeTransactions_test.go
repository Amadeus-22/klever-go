package node_test

import (
	"encoding/hex"
	"errors"
	"testing"

	"github.com/klever-io/klever-go/common"
	commonMock "github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/data/api"
	"github.com/klever-io/klever-go/data/retriever"
	"github.com/klever-io/klever-go/data/transaction"
	"github.com/klever-io/klever-go/node"
	"github.com/klever-io/klever-go/storage"
	"github.com/klever-io/klever-go/tools"
	"github.com/stretchr/testify/require"
)

// createNodeForTxLookup wires only what GetTransaction reads: the transaction pool and
// the storers behind TransactionUnit and TxLogsUnit.
func createNodeForTxLookup(
	t *testing.T,
	pooled interface{},
	committed map[string][]byte,
	logs map[string][]byte,
) *node.Node {
	dataPool := &commonMock.PoolsHolderStub{
		TransactionsCalled: func() retriever.ShardedDataCacherNotifier {
			return &commonMock.ShardedDataStub{
				SearchFirstDataCalled: func(key []byte) (interface{}, bool) {
					if pooled == nil {
						return nil, false
					}
					return pooled, true
				},
			}
		},
	}

	storerFor := func(stored map[string][]byte) storage.Storer {
		return &commonMock.StorerStub{
			SearchFirstCalled: func(key []byte) ([]byte, error) {
				buff, ok := stored[string(key)]
				if !ok {
					return nil, errors.New("key not found")
				}
				return buff, nil
			},
		}
	}

	n, err := node.NewNode(
		node.WithDataPool(dataPool),
		node.WithInternalMarshalizer(getMarshalizer()),
		node.WithTxSignMarshalizer(getMarshalizer()),
		node.WithHasher(getHasher()),
		node.WithTxSignHasher(getHasher()),
		node.WithDataStore(&commonMock.ChainStorerMock{
			GetStorerCalled: func(unitType retriever.UnitType) storage.Storer {
				if unitType == retriever.TxLogsUnit {
					return storerFor(logs)
				}
				return storerFor(committed)
			},
		}),
		node.WithAddressPubkeyConverter(createMockPubkeyConverter()),
	)
	require.Nil(t, err)

	return n
}

// txHashHex mirrors prepareNormalTx: the API hash is computed over RawData.
func txHashHex(t *testing.T, tx *transaction.Transaction) string {
	hash, err := tools.CalculateHash(getMarshalizer(), getHasher(), tx.RawData)
	require.Nil(t, err)

	return hex.EncodeToString(hash)
}

func txForLookup() *transaction.Transaction {
	return &transaction.Transaction{
		RawData: &transaction.Transaction_Raw{
			Nonce:   1,
			Sender:  []byte("sender-address-for-lookup-tests!"),
			Data:    [][]byte{},
			Version: 1,
		},
	}
}

// A pool hit is pending regardless of the pooled object's fields.
func TestNode_GetTransactionFromPoolIsPending(t *testing.T) {
	t.Parallel()

	tx := txForLookup()
	tx.Block = 42
	n := createNodeForTxLookup(t, tx, map[string][]byte{}, nil)

	hash := txHashHex(t, tx)

	apiTx, err := n.GetTransaction(hash, true)
	require.Nil(t, err)
	require.Equal(t, api.TRANSACTION_STATUS_PENDING, apiTx.Status)
}

// Presence in TransactionUnit is written only by CommitBlock, so it is what makes a
// transaction on-chain - not a field on a shared pool object.
func TestNode_GetTransactionFromStorageIsOnChain(t *testing.T) {
	t.Parallel()

	tx := txForLookup()
	committedTx := txForLookup()
	committedTx.Block = 42
	committedTx.Receipts = []*transaction.Transaction_Receipt{{Data: [][]byte{[]byte("receipt")}}}

	buff, err := getMarshalizer().Marshal(committedTx)
	require.Nil(t, err)

	hash := txHashHex(t, tx)
	rawHash, err := hex.DecodeString(hash)
	require.Nil(t, err)

	n := createNodeForTxLookup(t, nil, map[string][]byte{string(rawHash): buff}, nil)

	apiTx, err := n.GetTransaction(hash, false)
	require.Nil(t, err)
	require.Equal(t, api.TRANSACTION_STATUS_ON_CHAIN, apiTx.Status)
	require.Equal(t, uint64(42), apiTx.Block)
}

// A committed transaction still sitting in the pool must not report as pending:
// removeTxsFromPools runs after the commit and its failure is not critical.
func TestNode_GetTransactionPrefersCommittedOverPooled(t *testing.T) {
	t.Parallel()

	tx := txForLookup()
	committedTx := txForLookup()
	committedTx.Block = 7

	buff, err := getMarshalizer().Marshal(committedTx)
	require.Nil(t, err)

	hash := txHashHex(t, tx)
	rawHash, err := hex.DecodeString(hash)
	require.Nil(t, err)

	// the transaction is in both places at once
	n := createNodeForTxLookup(t, tx, map[string][]byte{string(rawHash): buff}, nil)

	apiTx, err := n.GetTransaction(hash, false)
	require.Nil(t, err)
	require.Equal(t, api.TRANSACTION_STATUS_ON_CHAIN, apiTx.Status)
	require.Equal(t, uint64(7), apiTx.Block)
}

// Genesis txs are stored with Block == 0 and are still on-chain.
func TestNode_GetTransactionFromStorageWithZeroBlockIsOnChain(t *testing.T) {
	t.Parallel()

	tx := txForLookup()
	buff, err := getMarshalizer().Marshal(tx)
	require.Nil(t, err)

	hash := txHashHex(t, tx)
	rawHash, err := hex.DecodeString(hash)
	require.Nil(t, err)

	n := createNodeForTxLookup(t, nil, map[string][]byte{string(rawHash): buff}, nil)

	apiTx, err := n.GetTransaction(hash, false)
	require.Nil(t, err)
	require.Equal(t, api.TRANSACTION_STATUS_ON_CHAIN, apiTx.Status)
}

func TestNode_GetTransactionNotFound(t *testing.T) {
	t.Parallel()

	n := createNodeForTxLookup(t, nil, map[string][]byte{}, nil)

	apiTx, err := n.GetTransaction(txHashHex(t, txForLookup()), false)
	require.Nil(t, apiTx)
	require.Equal(t, common.ErrTransactionNotFound, err)
}

func TestNode_GetTransactionPoolErrorIsReturned(t *testing.T) {
	t.Parallel()

	n := createNodeForTxLookup(t, "not a transaction", map[string][]byte{}, nil)

	apiTx, err := n.GetTransaction(txHashHex(t, txForLookup()), false)
	require.Nil(t, apiTx)
	require.Equal(t, common.ErrWrongTypeAssertion, err)
}

// An SC tx read from storage carries its logs from TxLogsUnit when results are asked for.
func TestNode_GetTransactionWithResultsReadsLogsFromStorage(t *testing.T) {
	t.Parallel()

	tx := txForLookup()
	tx.RawData.Contract = []*transaction.TXContract{{Type: transaction.TXContract_SmartContractType}}
	tx.Block = 7
	txBuff, err := getMarshalizer().Marshal(tx)
	require.Nil(t, err)

	logBuff, err := getMarshalizer().Marshal(&transaction.Log{
		Address: []byte("contract-address-for-lookup-test"),
		Events:  []*transaction.Event{{Identifier: []byte("transfer")}},
	})
	require.Nil(t, err)

	hash := txHashHex(t, tx)
	rawHash, err := hex.DecodeString(hash)
	require.Nil(t, err)

	n := createNodeForTxLookup(t, nil,
		map[string][]byte{string(rawHash): txBuff},
		map[string][]byte{string(rawHash): logBuff},
	)

	apiTx, err := n.GetTransaction(hash, true)
	require.Nil(t, err)
	require.NotNil(t, apiTx.Logs)
	require.Len(t, apiTx.Logs.Events, 1)
	require.Equal(t, "transfer", apiTx.Logs.Events[0].Identifier)

	apiTx, err = n.GetTransaction(hash, false)
	require.Nil(t, err)
	require.Nil(t, apiTx.Logs)
}
