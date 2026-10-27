package transaction

import (
	"errors"
	"testing"

	"github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/data/retriever"
	tdata "github.com/klever-io/klever-go/data/transaction"
	"github.com/klever-io/klever-go/integrationTest/processorNode"
	"github.com/klever-io/klever-go/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func Test_GetAndCheckTransaction(t *testing.T) {
	node, err := processorNode.NewProcessorNode()
	require.Nil(t, err)

	// committed txs are served from TransactionUnit, keyed by ChainID like the hash
	committed := make(map[string]*tdata.Transaction)
	node.InternalMarshalizer = &mock.MarshalizerStub{
		MarshalCalled: func(obj interface{}) ([]byte, error) {
			return obj.(*tdata.Transaction_Raw).ChainID, nil
		},
		UnmarshalCalled: func(obj interface{}, buff []byte) error {
			proto.Merge(obj.(*tdata.Transaction), committed[string(buff)])
			return nil
		},
	}
	node.Store = &mock.ChainStorerMock{
		GetStorerCalled: func(_ retriever.UnitType) storage.Storer {
			return &mock.StorerStub{
				SearchFirstCalled: func(key []byte) ([]byte, error) {
					if _, ok := committed[string(key)]; !ok {
						return nil, errors.New("not found")
					}
					return key, nil
				},
			}
		},
	}
	node.Hasher = &mock.HasherStub{
		ComputeCalled: func(data string) []byte {
			return []byte(data)
		},
	}
	node.DataPool = mock.NewPoolsHolderMock()

	tests := []struct {
		name        string
		tx          *tdata.Transaction
		committed   bool
		expectedErr error
	}{
		{
			name: "transaction not in block",
			tx: &tdata.Transaction{
				RawData: &tdata.Transaction_Raw{
					Nonce:   1,
					ChainID: []byte("0x1"),
				},
			},
			expectedErr: ErrNotInBlock,
		},
		{
			name: "successful transaction",
			tx: &tdata.Transaction{
				RawData: &tdata.Transaction_Raw{
					Nonce:   1,
					ChainID: []byte("0x2"),
				},
				Block: 1,
			},
			committed:   true,
			expectedErr: nil,
		},
		{
			name: "failed transaction status",
			tx: &tdata.Transaction{
				RawData: &tdata.Transaction_Raw{
					Nonce:   1,
					ChainID: []byte("0x3"),
				},
				Block:  1,
				Result: tdata.Transaction_FAILED,
			},
			committed:   true,
			expectedErr: ErrStatusNotSuccess,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.committed {
				committed[string(tt.tx.RawData.ChainID)] = tt.tx
			} else {
				node.DataPool.Transactions().AddData(
					tt.tx.RawData.ChainID,
					tt.tx,
					10,
					"meta",
				)
			}
			_, err := GetAndCheckTransaction(node, tt.tx.RawData.ChainID)
			assert.Equal(t, tt.expectedErr, err)
		})
	}
}
