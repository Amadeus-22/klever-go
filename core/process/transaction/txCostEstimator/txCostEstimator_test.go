package txcostestimator

import (
	"errors"
	"math"
	"testing"

	"github.com/klever-io/klever-go/common"
	"github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/core/process"
	"github.com/klever-io/klever-go/core/process/txsimulator/data"
	"github.com/klever-io/klever-go/data/transaction"
	"github.com/klever-io/klever-go/vmcommon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockTransactionCostEstimator() *transactionCostEstimator {
	accCacher := &mock.AccountsCacherStub{}
	feeHandler := &mock.FeeHandlerStub{MaxGasLimitPerTxValue: math.MaxInt64}
	forkController := &mock.ForkControllerStub{}
	txSimulator := &mock.TransactionSimulatorProcessorStub{}

	costEstimator, _ := NewTransactionCostEstimator(
		accCacher,
		feeHandler,
		txSimulator,
		forkController,
	)

	return costEstimator
}

// Test NewTransactionCostEstimator
func TestNewTransactionCostEstimator(t *testing.T) {
	accCacher := &mock.AccountsCacherStub{}
	feeHandler := &mock.FeeHandlerStub{}
	forkController := &mock.ForkControllerStub{}
	txSimulator := &mock.TransactionSimulatorProcessorStub{}

	t.Run("Valid creation", func(t *testing.T) {
		tce, err := NewTransactionCostEstimator(
			accCacher,
			feeHandler,
			txSimulator,
			forkController,
		)
		assert.NoError(t, err)
		assert.NotNil(t, tce)
	})

	t.Run("Nil accountsCacher", func(t *testing.T) {
		tce, err := NewTransactionCostEstimator(
			nil,
			feeHandler,
			txSimulator,
			forkController,
		)
		assert.Error(t, err)
		assert.Nil(t, tce)
		assert.Equal(t, process.ErrNilAccountsAdapter, err)
	})

	t.Run("Nil feeHandler", func(t *testing.T) {
		tce, err := NewTransactionCostEstimator(
			accCacher,
			nil,
			txSimulator,
			forkController,
		)
		assert.Error(t, err)
		assert.Nil(t, tce)
		assert.Equal(t, process.ErrNilEconomicsFeeHandler, err)
	})

	t.Run("Nil txSimulator", func(t *testing.T) {
		tce, err := NewTransactionCostEstimator(
			accCacher,
			feeHandler,
			nil,
			forkController,
		)
		assert.Error(t, err)
		assert.Nil(t, tce)
		assert.Equal(t, common.ErrNilTxSimulatorProcessor, err)
	})

	t.Run("Nil forkController", func(t *testing.T) {
		tce, err := NewTransactionCostEstimator(
			accCacher,
			feeHandler,
			txSimulator,
			nil,
		)
		assert.Error(t, err)
		assert.Nil(t, tce)
		assert.Equal(t, process.ErrNilEnableEpochsHandler, err)
	})
}

// Test EstimateTransactionGas
func TestEstimateTransactionGas(t *testing.T) {
	t.Run("Successful estimation", func(t *testing.T) {
		tce := newMockTransactionCostEstimator()
		tce.txSimulator = &mock.TransactionSimulatorProcessorStub{
			ProcessTXCalled: func(tx *transaction.Transaction) (*data.SimulationResults, error) {
				return &data.SimulationResults{
					VMOutput: &vmcommon.VMOutput{
						ReturnCode:    vmcommon.Ok,
						ReturnMessage: "Success",
						GasRemaining:  1000,
					},
				}, nil
			},
		}

		tx := &transaction.Transaction{
			GasMultiplier: 1,
			GasLimit:      100000,
		}

		response, err := tce.EstimateTransactionGas(tx)
		require.NoError(t, err)
		assert.Equal(t, uint64(0), response.GasEstimated) // Since mock ComputeTotalGasConsumed returns 0
		assert.Contains(t, response.RetMessage, "Success")
	})

	t.Run("ProcessTx error", func(t *testing.T) {
		tce := newMockTransactionCostEstimator()
		tce.txSimulator = &mock.TransactionSimulatorProcessorStub{
			ProcessTXCalled: func(tx *transaction.Transaction) (*data.SimulationResults, error) {
				return nil, errors.New("process error")
			},
		}

		tx := &transaction.Transaction{}

		response, err := tce.EstimateTransactionGas(tx)
		require.NoError(t, err)
		assert.Equal(t, uint64(0), response.GasEstimated)
		assert.Equal(t, "process error", response.RetMessage)
	})

	t.Run("ProcessTx fail reason", func(t *testing.T) {
		tce := newMockTransactionCostEstimator()
		tce.txSimulator = &mock.TransactionSimulatorProcessorStub{
			ProcessTXCalled: func(tx *transaction.Transaction) (*data.SimulationResults, error) {
				return &data.SimulationResults{
					FailReason: "simulation failed",
				}, nil
			},
		}

		tx := &transaction.Transaction{}

		response, err := tce.EstimateTransactionGas(tx)
		require.NoError(t, err)
		assert.Equal(t, uint64(0), response.GasEstimated)
		assert.Equal(t, "simulation failed", response.RetMessage)
	})

	t.Run("Nil VMOutput", func(t *testing.T) {
		tce := newMockTransactionCostEstimator()
		tce.txSimulator = &mock.TransactionSimulatorProcessorStub{
			ProcessTXCalled: func(tx *transaction.Transaction) (*data.SimulationResults, error) {
				return &data.SimulationResults{
					VMOutput: nil,
				}, nil
			},
		}

		tx := &transaction.Transaction{}

		response, err := tce.EstimateTransactionGas(tx)
		require.NoError(t, err)
		assert.Equal(t, uint64(0), response.GasEstimated)
		assert.Equal(t, process.ErrNilVMOutput.Error(), response.RetMessage)
	})
}

// Test addMissingFieldsIfNeeded
func TestAddMissingFieldsIfNeeded(t *testing.T) {
	tce := newMockTransactionCostEstimator()

	signature := [][]byte{[]byte("existing signature")}

	t.Run("Add signature", func(t *testing.T) {
		tx := &transaction.Transaction{}
		tce.addMissingFieldsIfNeeded(tx)
		assert.Equal(t, [][]byte{[]byte(dummySignature)}, tx.Signature)
	})

	t.Run("Add gas limit", func(t *testing.T) {
		tx := &transaction.Transaction{
			Signature: signature,
		}
		tce.addMissingFieldsIfNeeded(tx)
		assert.Equal(t, uint64(9223372036854775807), tx.GasLimit) // math.MaxInt64
	})

	t.Run("Don't modify existing fields", func(t *testing.T) {
		tx := &transaction.Transaction{
			Signature: signature,
			GasLimit:  1000,
		}
		tce.addMissingFieldsIfNeeded(tx)
		assert.Equal(t, signature, tx.Signature)
		assert.Equal(t, uint64(1000), tx.GasLimit)
	})

}

// the wired state is what tells a node still starting from a max gas limit that
// is legitimately 0 - MaxGasPerTX only exists once EnableSmartContracts is active
func TestEstimateTransactionGas_ProposalControllerWiredState(t *testing.T) {
	simulator := &mock.TransactionSimulatorProcessorStub{
		ProcessTXCalled: func(tx *transaction.Transaction) (*data.SimulationResults, error) {
			return &data.SimulationResults{}, nil
		},
	}

	newEstimator := func(feeHandler *mock.FeeHandlerStub) *transactionCostEstimator {
		tce, err := NewTransactionCostEstimator(
			&mock.AccountsCacherStub{},
			feeHandler,
			simulator,
			&mock.ForkControllerStub{},
		)
		require.NoError(t, err)

		return tce
	}

	unwired := &mock.FeeHandlerStub{IsProposalControllerSetCalled: func() bool { return false }}

	// both gas limits take the same path, so an estimate before wiring never
	// comes back as a CostResponse with an out-of-gas RetMessage
	for _, gasLimit := range []uint64{0, 1000} {
		tx := &transaction.Transaction{Signature: [][]byte{[]byte("sig")}, GasLimit: gasLimit}

		cost, err := newEstimator(unwired).EstimateTransactionGas(tx)
		require.Nil(t, cost)
		require.Equal(t, process.ErrProposalNotInitialized, err)
	}

	// wired, but MaxGasPerTX is missing before the smart contracts fork: 0 is a
	// real value here and the estimate must run
	wiredWithoutMaxGas := &mock.FeeHandlerStub{MaxGasLimitPerTxValue: 0}
	tx := &transaction.Transaction{Signature: [][]byte{[]byte("sig")}}

	cost, err := newEstimator(wiredWithoutMaxGas).EstimateTransactionGas(tx)
	require.NoError(t, err)
	require.NotNil(t, cost)
	require.Equal(t, process.ErrNilVMOutput.Error(), cost.RetMessage)
	require.Equal(t, uint64(0), tx.GasLimit)
}

// Test IsInterfaceNil
func TestIsInterfaceNil(t *testing.T) {
	t.Run("Non-nil", func(t *testing.T) {
		tce := newMockTransactionCostEstimator()
		assert.False(t, tce.IsInterfaceNil())
	})

	t.Run("Nil", func(t *testing.T) {
		var tce *transactionCostEstimator = nil
		assert.True(t, tce.IsInterfaceNil())
	})
}
