package indexer

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/klever-io/klever-go/common"
	"github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/crypto/hashing/sha256"
	"github.com/klever-io/klever-go/data"
	dataBlock "github.com/klever-io/klever-go/data/block"
	"github.com/klever-io/klever-go/data/indexer"
	"github.com/klever-io/klever-go/data/transaction"
	idata "github.com/klever-io/klever-go/indexer/data"
	imock "github.com/klever-io/klever-go/indexer/mock"
	"github.com/klever-io/klever-go/indexer/workItems"
	"github.com/klever-io/klever-go/tools"
	"github.com/klever-io/klever-go/tools/check"
	"github.com/klever-io/klever-go/tools/marshal"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func NewDataIndexerArguments() ArgDataIndexer {
	return ArgDataIndexer{
		Marshalizer:        &mock.MarshalizerMock{},
		NodesCoordinator:   &mock.NodesCoordinatorMock{},
		EpochStartNotifier: &mock.EpochStartNotifierStub{},
		DataDispatcher:     &imock.DispatcherMock{},
		ElasticProcessor:   &imock.ElasticProcessorStub{},
	}
}

func TestDataIndexer_NewIndexerWithNilNodesCoordinatorShouldErr(t *testing.T) {
	arguments := NewDataIndexerArguments()
	arguments.NodesCoordinator = nil
	ei, err := NewDataIndexer(arguments)

	require.Nil(t, ei)
	require.Equal(t, common.ErrNilNodesCoordinator, err)
}

func TestDataIndexer_NewIndexerWithNilDataDispatcherShouldErr(t *testing.T) {
	arguments := NewDataIndexerArguments()
	arguments.DataDispatcher = nil
	ei, err := NewDataIndexer(arguments)

	require.Nil(t, ei)
	require.Equal(t, ErrNilDataDispatcher, err)
}

func TestDataIndexer_NewIndexerWithNilElasticProcessorShouldErr(t *testing.T) {
	arguments := NewDataIndexerArguments()
	arguments.ElasticProcessor = nil
	ei, err := NewDataIndexer(arguments)

	require.Nil(t, ei)
	require.Equal(t, ErrNilElasticProcessor, err)
}

func TestDataIndexer_NewIndexerWithNilMarshalizerShouldErr(t *testing.T) {
	arguments := NewDataIndexerArguments()
	arguments.Marshalizer = nil
	ei, err := NewDataIndexer(arguments)

	require.Nil(t, ei)
	require.Equal(t, common.ErrNilMarshalizer, err)
}

func TestDataIndexer_NewIndexerWithNilEpochStartNotifierShouldErr(t *testing.T) {
	arguments := NewDataIndexerArguments()
	arguments.EpochStartNotifier = nil
	ei, err := NewDataIndexer(arguments)

	require.Nil(t, ei)
	require.Equal(t, common.ErrNilEpochStartNotifier, err)
}

func TestDataIndexer_NewIndexerWithCorrectParamsShouldWork(t *testing.T) {
	arguments := NewDataIndexerArguments()

	ei, err := NewDataIndexer(arguments)

	require.Nil(t, err)
	require.False(t, check.IfNil(ei))
	require.False(t, ei.IsNilIndexer())
}

func TestDataIndexer_SaveBlock(t *testing.T) {
	called := false

	arguments := NewDataIndexerArguments()
	arguments.DataDispatcher = &imock.DispatcherMock{
		AddCalled: func(item workItems.WorkItemHandler) {
			called = true
		},
	}
	ei, _ := NewDataIndexer(arguments)

	args := &indexer.ArgsSaveBlockData{
		Header:     &dataBlock.Block{},
		HeaderHash: []byte("hash"),
	}
	ei.SaveBlock(args)
	require.True(t, called)
}

// The account snapshot taken on the commit goroutine must reach the queued work item:
// without it the worker indexes the block with no account documents (KLR-68).
func TestDataIndexer_SaveBlockQueuesPreparedBlockData(t *testing.T) {
	errResolve := errors.New("resolve failed")

	tests := []struct {
		name       string
		prepared   any
		payload    *idata.PreparedBlockData
		resolveErr error
	}{
		{name: "no payload from eventsProcessor", payload: &idata.PreparedBlockData{}},
		// eventsProcessor.prepare returns a typed nil for an empty pool
		{name: "typed-nil payload from eventsProcessor", prepared: (*idata.PreparedBlockData)(nil), payload: &idata.PreparedBlockData{}},
		{name: "resolve error still queues the block", payload: &idata.PreparedBlockData{}, resolveErr: errResolve},
		{name: "nothing resolved keeps the original payload", prepared: (*idata.PreparedBlockData)(nil)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var received, saved any
			var queued workItems.WorkItemHandler

			arguments := NewDataIndexerArguments()
			arguments.ElasticProcessor = &imock.ElasticProcessorStub{
				PrepareBlockDataCalled: func(_ data.HeaderHandler, _ *indexer.Pool, prepared any) (*idata.PreparedBlockData, error) {
					received = prepared
					return tt.payload, tt.resolveErr
				},
				SaveTransactionsCalled: func(_ data.HeaderHandler, _ *indexer.Pool, prepared any) error {
					saved = prepared
					return nil
				},
			}
			arguments.DataDispatcher = &imock.DispatcherMock{
				AddCalled: func(item workItems.WorkItemHandler) { queued = item },
			}
			di, err := NewDataIndexer(arguments)
			require.NoError(t, err)

			di.SaveBlock(&indexer.ArgsSaveBlockData{Header: &dataBlock.Block{}, Prepared: tt.prepared})

			require.Equal(t, tt.prepared, received)
			require.NotNil(t, queued)
			require.NoError(t, queued.Save())
			if tt.payload != nil {
				require.Same(t, tt.payload, saved)
			} else {
				require.Equal(t, tt.prepared, saved)
			}
		})
	}
}

// SaveBlock preps on the commit goroutine, ahead of the work item's nil guards, so with a
// real processor it must still tolerate a nil header or pool rather than crash the node.
func TestDataIndexer_SaveBlockToleratesNilHeaderAndPool(t *testing.T) {
	for name, args := range map[string]*indexer.ArgsSaveBlockData{
		"nil header": {TransactionsPool: &indexer.Pool{}},
		"nil pool":   {Header: &dataBlock.Block{Header: &dataBlock.BlockHeader{Nonce: 1}}},
	} {
		t.Run(name, func(t *testing.T) {
			var queued workItems.WorkItemHandler

			arguments := NewDataIndexerArguments()
			arguments.ElasticProcessor = newTestElasticSearchDatabase(&imock.DatabaseWriterStub{}, createMockElasticProcessorArgs())
			arguments.DataDispatcher = &imock.DispatcherMock{
				AddCalled: func(item workItems.WorkItemHandler) { queued = item },
			}
			di, err := NewDataIndexer(arguments)
			require.NoError(t, err)

			require.NotPanics(t, func() { di.SaveBlock(args) })
			require.NotNil(t, queued)
		})
	}
}

func TestDataIndexer(t *testing.T) {
	t.Skip("this is not a short test")

	testCreateIndexer(t)
}

// nolint
func testCreateIndexer(t *testing.T) {
	indexTemplates, indexPolicies := getIndexTemplateAndPolicies()

	dispatcher, _ := NewDataDispatcher(100)
	dbClient, _ := NewElasticClient(elasticsearch.Config{
		Addresses: []string{"http://localhost:9200"},
		Username:  "",
		Password:  "",
	})

	elasticIndexer, _ := NewElasticProcessor(ArgElasticProcessor{
		IndexTemplates:           indexTemplates,
		IndexPolicies:            indexPolicies,
		Marshalizer:              &marshal.JSONMarshalizer{},
		Hasher:                   sha256.Sha256{},
		AddressPubkeyConverter:   &mock.PubkeyConverterMock{},
		ValidatorPubkeyConverter: &mock.PubkeyConverterMock{},
		DBClient:                 dbClient,
		CacheExpirationTime:      time.Hour,
		CacheCleanUpInterval:     2 * time.Hour,
	})

	di, err := NewDataIndexer(ArgDataIndexer{
		Marshalizer:        &marshal.JSONMarshalizer{},
		EpochStartNotifier: &mock.EpochStartNotifierStub{},
		DataDispatcher:     dispatcher,
		ElasticProcessor:   elasticIndexer,
	})
	if err != nil {
		fmt.Println(err)
		t.Fail()
	}

	// Generate transaction and hashes
	numTransactions := 10
	dataSize := 1000
	signer := []byte("signature")

	for i := 0; i < 100; i++ {
		txs, hashes := generateTransactions(numTransactions, dataSize)

		header := &dataBlock.Block{Header: &dataBlock.BlockHeader{Nonce: uint64(i)}}

		txsPool := make(map[string]data.TransactionHandler)
		for j := 0; j < numTransactions; j++ {
			txsPool[hashes[j]] = &txs[j]
		}

		args := &indexer.ArgsSaveBlockData{
			HeaderHash:       []byte("hash"),
			Header:           header,
			Signer:           signer,
			TransactionsPool: &indexer.Pool{Txs: txsPool},
		}
		di.SaveBlock(args)
	}

	time.Sleep(100 * time.Second)
}

// nolint
func generateTransactions(numTxs int, datFieldSize int) ([]transaction.Transaction, []string) {
	txs := make([]transaction.Transaction, 0)
	hashes := make([]string, 0)

	randomByteArray := make([]byte, datFieldSize)
	_, _ = rand.Read(randomByteArray)

	for i := 0; i < numTxs; i++ {

		contract := transaction.TransferContract{
			ToAddress: []byte("klv1d05ju9jaj6u99zph0ant9jh7gksg"),
			Amount:    int64(i),
		}
		tx, _ := createTransactionMock(&contract, transaction.TXContract_TransferContractType, []byte("klv1d05ju9jaj6u99zph0ant9jh7gksf"))
		tx.RawData.Data = [][]byte{randomByteArray}

		// hash
		hashes = append(hashes, fmt.Sprintf("%032x", i))

		txs = append(txs, *tx.Clone())
	}

	return txs, hashes
}

func createTransactionMock(contract protoreflect.ProtoMessage, txType transaction.TXContract_ContractType, sender []byte) (*transaction.Transaction, error) {
	var tx transaction.Transaction

	var signatures [][]byte
	tx.Signature = append(signatures, []byte("txSigner"))

	tx.RawData = &transaction.Transaction_Raw{
		Sender: sender,
	}

	err := tx.PushContract(txType, contract)

	return &tx, err
}

// nolint
func getIndexTemplateAndPolicies() (map[string]*bytes.Buffer, map[string]*bytes.Buffer) {
	indexTemplates := make(map[string]*bytes.Buffer)
	indexPolicies := make(map[string]*bytes.Buffer)

	template := &bytes.Buffer{}
	_ = tools.LoadJSONFile(template, "./testdata/opendistro.json")
	indexTemplates["opendistro"] = template

	_ = tools.LoadJSONFile(template, "./testdata/blocks.json")
	indexTemplates["blocks"] = template
	policy := &bytes.Buffer{}
	_ = tools.LoadJSONFile(template, "./testdata/blocks_policy.json")
	indexPolicies["blocks_policy"] = policy

	return indexTemplates, indexPolicies
}
