package economics

import (
	"math/big"
	"testing"

	"github.com/klever-io/klever-go/common"
	"github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/config"
	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/core/fork"
	"github.com/klever-io/klever-go/core/process"
	txSimData "github.com/klever-io/klever-go/core/process/txsimulator/data"
	"github.com/klever-io/klever-go/data/transaction"
	"github.com/klever-io/klever-go/kapps"
	"github.com/klever-io/klever-go/vmcommon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const MinFreezeAmount = int64(1_000)

// Helper function to create a new EconomicsData with mocks
func newMockEconomicsData() *EconomicsData {
	ed := &EconomicsData{
		leaderPercentage:     0.5,
		txSimulatorProcessor: &mock.TransactionSimulatorProcessorStub{},
	}
	ed.proposalController.Set(createProposalController(make(map[int32]*kapps.Parameter)))

	return ed
}

func createProposalController(params map[int32]*kapps.Parameter) kapps.ActiveProposalController {
	epochNotifier := &mock.EpochNotifierStub{}
	forkController, _ := fork.NewForkController(config.EnableEpochs{
		ClaimKFI:              0,
		ProcessorFlowITOPrice: 0,
		FixStakingBuckets:     0,
		KdaFpr:                0,
	}, epochNotifier)

	pc, _ := kapps.NewProposalController(forkController)

	pc.RestoreParameters(params)

	return pc
}

// Test NewEconomicsData
func TestNewEconomicsData(t *testing.T) {
	args := ArgsNewEconomicsData{
		EpochNotifier: &mock.EpochNotifierStub{},
	}

	ed, err := NewEconomicsData(args)
	assert.NoError(t, err)
	assert.NotNil(t, ed)
	assert.Equal(t, 0.5, ed.leaderPercentage)
}

// Test SetProposalController
func TestSetProposalController(t *testing.T) {
	ed := newMockEconomicsData()

	t.Run("Valid controller", func(t *testing.T) {
		controller := createProposalController(map[int32]*kapps.Parameter{
			int32(kapps.EnumParameter_GasMultiplier): {Type: kapps.EnumType_Int64, Value: []byte("1")},
		})
		err := ed.SetProposalController(controller)
		assert.NoError(t, err)
		assert.Equal(t, controller, ed.proposalController.Get())
	})

	t.Run("Nil controller", func(t *testing.T) {
		err := ed.SetProposalController(nil)
		assert.Error(t, err)
		assert.Equal(t, common.ErrNilProposalController, err)
	})
}

// Test SetTXSimulatorProcessor
func TestSetTXSimulatorProcessor(t *testing.T) {
	ed := newMockEconomicsData()

	t.Run("Valid processor", func(t *testing.T) {
		processor := &mock.TransactionSimulatorProcessorStub{}
		err := ed.SetTXSimulatorProcessor(processor)
		assert.NoError(t, err)
		assert.Equal(t, processor, ed.txSimulatorProcessor)
	})

	t.Run("Nil processor", func(t *testing.T) {
		err := ed.SetTXSimulatorProcessor(nil)
		assert.Error(t, err)
		assert.Equal(t, common.ErrNilTxSimulatorProcessor, err)
	})
}

// Test ComputeTransactionCost
func TestComputeTransactionCost(t *testing.T) {
	// ed.proposalController.Get().UpdateParameters(map[int32]*kapps.Parameter{
	// 	int32(kapps.EnumParameter_GasMultiplier):  {Type: kapps.EnumType_Int64, Value: []byte("1")},
	// 	int32(kapps.EnumParameter_FeePerDataByte): {Type: kapps.EnumType_Int64, Value: []byte("2")},
	// })

	baseSCTransaction := &transaction.Transaction{
		RawData: &transaction.Transaction_Raw{
			Contract: []*transaction.TXContract{
				{Type: transaction.TXContract_SmartContractType},
			},
			Data: [][]byte{
				make([]byte, 100),
			},
		},
	}

	t.Run("Valid transaction", func(t *testing.T) {
		ed := newMockEconomicsData()
		tx := &transaction.Transaction{
			RawData: &transaction.Transaction_Raw{
				Contract: []*transaction.TXContract{
					{Type: transaction.TXContract_TransferContractType},
				},
				Data: [][]byte{
					make([]byte, 100),
				},
			},
		}
		ed.proposalController.Get().UpdateParameters(map[int32]*kapps.Parameter{
			int32(kapps.EnumParameter_GasMultiplier):   {Type: kapps.EnumType_Int64, Value: []byte("1")},
			int32(kapps.EnumParameter_FeePerDataByte):  {Type: kapps.EnumType_Int64, Value: []byte("2")},
			int32(kapps.EnumParameter_KAppFeeTransfer): {Type: kapps.EnumType_Int64, Value: []byte("10")},
		})

		cost, err := ed.ComputeTransactionCost(tx, false)
		assert.NoError(t, err)
		assert.Equal(t, uint64(1), cost.GasMultiplier)
		assert.Equal(t, int64(10), cost.KAppFee)
		assert.Equal(t, int64(700), cost.BandwidthFee) // (100 (DataSize) + 250 (BaseTxSize)) * 2 (FeePerDataByte)
	})

	t.Run("Smart contract transaction", func(t *testing.T) {
		ed := newMockEconomicsData()
		ed.proposalController.Get().UpdateParameters(map[int32]*kapps.Parameter{
			int32(kapps.EnumParameter_KAppFeeSmartContract): {Type: kapps.EnumType_Int64, Value: []byte("20")},
			int32(kapps.EnumParameter_GasMultiplier):        {Type: kapps.EnumType_Int64, Value: []byte("2")},
			int32(kapps.EnumParameter_FeePerDataByte):       {Type: kapps.EnumType_Int64, Value: []byte("2")},
		})

		ed.txSimulatorProcessor.(*mock.TransactionSimulatorProcessorStub).ProcessTXCalled = func(tx *transaction.Transaction) (*txSimData.SimulationResults, error) {
			return &txSimData.SimulationResults{
				VMOutput: &vmcommon.VMOutput{
					Logs: []*vmcommon.LogEntry{
						{
							Identifier:  []byte(core.TotalConsumedGasString),
							Topics:      [][]byte{big.NewInt(100).Bytes()},
							IsSystemLog: true,
						},
					},
				},
			}, nil
		}

		cost, err := ed.ComputeTransactionCost(baseSCTransaction, true)
		assert.NoError(t, err)
		assert.Equal(t, uint64(2), cost.GasMultiplier)
		assert.Equal(t, int64(20), cost.KAppFee)
		assert.Equal(t, int64(700), cost.BandwidthFee)  // (100 (DataSize) + 250 (BaseTxSize)) * 2 (FeePerDataByte)
		assert.Equal(t, uint64(102), cost.GasEstimated) // 100 Consumed * 2 GasMultiplier + GasMultiplier
	})

	t.Run("Invalid contract type", func(t *testing.T) {
		ed := newMockEconomicsData()
		tx := &transaction.Transaction{
			RawData: &transaction.Transaction_Raw{
				Contract: []*transaction.TXContract{
					{Type: 9999}, // Invalid type
				},
			},
		}

		_, err := ed.ComputeTransactionCost(tx, false)
		assert.Error(t, err)
		assert.Equal(t, process.ErrInvalidTransactionType, err)
	})

	t.Run("Multiple contracts with smart contract", func(t *testing.T) {
		ed := newMockEconomicsData()
		tx := &transaction.Transaction{
			RawData: &transaction.Transaction_Raw{
				Contract: []*transaction.TXContract{
					{Type: transaction.TXContract_SmartContractType},
					{Type: transaction.TXContract_TransferContractType},
				},
			},
		}

		_, err := ed.ComputeTransactionCost(tx, false)
		assert.Error(t, err)
		assert.Equal(t, process.ErrSmartContractFailMaxContracts, err)
	})

	t.Run("Invalid proposal controller", func(t *testing.T) {
		ed := newMockEconomicsData()
		ed.proposalController.Set(nil)

		_, err := ed.ComputeTransactionCost(baseSCTransaction, false)
		assert.Error(t, err)
		assert.Equal(t, process.ErrProposalNotInitialized, err)
	})

	t.Run("Simulation failure on processTX", func(t *testing.T) {
		ed := newMockEconomicsData()
		ed.txSimulatorProcessor.(*mock.TransactionSimulatorProcessorStub).ProcessTXCalled = func(tx *transaction.Transaction) (*txSimData.SimulationResults, error) {
			return nil, process.ErrInvalidChainID
		}

		_, err := ed.ComputeTransactionCost(baseSCTransaction, true)
		assert.Error(t, err)
		assert.Equal(t, process.ErrInvalidChainID, err)
	})

	t.Run("Simulation failure on VMOutput", func(t *testing.T) {
		ed := newMockEconomicsData()
		ed.txSimulatorProcessor.(*mock.TransactionSimulatorProcessorStub).ProcessTXCalled = func(tx *transaction.Transaction) (*txSimData.SimulationResults, error) {
			return &txSimData.SimulationResults{FailReason: "failed"}, nil
		}

		_, err := ed.ComputeTransactionCost(baseSCTransaction, true)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed")
	})

	t.Run("Nil VMOutput", func(t *testing.T) {
		ed := newMockEconomicsData()
		ed.txSimulatorProcessor.(*mock.TransactionSimulatorProcessorStub).ProcessTXCalled = func(tx *transaction.Transaction) (*txSimData.SimulationResults, error) {
			return &txSimData.SimulationResults{VMOutput: nil}, nil
		}

		_, err := ed.ComputeTransactionCost(baseSCTransaction, true)
		assert.Error(t, err)
		assert.Equal(t, process.ErrNilVMOutput, err)
	})

	t.Run("Simulation failure with return message", func(t *testing.T) {
		ed := newMockEconomicsData()
		ed.txSimulatorProcessor.(*mock.TransactionSimulatorProcessorStub).ProcessTXCalled = func(tx *transaction.Transaction) (*txSimData.SimulationResults, error) {
			return &txSimData.SimulationResults{
				FailReason: "failed",
				VMOutput: &vmcommon.VMOutput{
					ReturnMessage: "error occurred",
				},
			}, nil
		}

		_, err := ed.ComputeTransactionCost(baseSCTransaction, true)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "error occurred")
	})
}

// Test CheckValidityTxValues
func TestCheckValidityTxValues(t *testing.T) {
	ed := newMockEconomicsData()

	ed.proposalController.Get().UpdateParameters(map[int32]*kapps.Parameter{
		int32(kapps.EnumParameter_GasMultiplier):   {Type: kapps.EnumType_Int64, Value: []byte("1")},
		int32(kapps.EnumParameter_FeePerDataByte):  {Type: kapps.EnumType_Int64, Value: []byte("1")},
		int32(kapps.EnumParameter_KAppFeeTransfer): {Type: kapps.EnumType_Int64, Value: []byte("10")},
	})

	t.Run("Valid transaction", func(t *testing.T) {
		tx := &transaction.Transaction{
			RawData: &transaction.Transaction_Raw{
				Contract: []*transaction.TXContract{
					{Type: transaction.TXContract_TransferContractType},
				},
				Data: [][]byte{
					make([]byte, 100),
				},
				BandwidthFee: 350, // (100 (DataSize) + 250 (BaseTxSize)) * 1 (FeePerDataByte)
				KAppFee:      10,
			},
		}

		cost, err := ed.CheckValidityTxValues(tx)
		assert.NoError(t, err)
		assert.NotNil(t, cost)
	})

	t.Run("Invalid bandwidth fee", func(t *testing.T) {
		tx := &transaction.Transaction{
			RawData: &transaction.Transaction_Raw{
				Contract: []*transaction.TXContract{
					{Type: transaction.TXContract_TransferContractType},
				},
				Data: [][]byte{
					make([]byte, 100),
				},
				BandwidthFee: 123, // Should be 124
				KAppFee:      10,
			},
		}

		_, err := ed.CheckValidityTxValues(tx)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), process.ErrInvalidTransactionFees.Error())
	})

	t.Run("Invalid KApp fee", func(t *testing.T) {
		tx := &transaction.Transaction{
			RawData: &transaction.Transaction_Raw{
				Contract: []*transaction.TXContract{
					{Type: transaction.TXContract_TransferContractType},
				},
				Data: [][]byte{
					make([]byte, 100),
				},
				BandwidthFee: 124,
				KAppFee:      9, // Should be 10
			},
		}

		_, err := ed.CheckValidityTxValues(tx)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), process.ErrInvalidTransactionFees.Error())
	})

	t.Run("Fail compute transaction cost", func(t *testing.T) {
		ed.proposalController.Set(nil)

		tx := &transaction.Transaction{
			RawData: &transaction.Transaction_Raw{
				Contract: []*transaction.TXContract{
					{Type: transaction.TXContract_TransferContractType},
				},
				Data: [][]byte{
					make([]byte, 100),
				},
			},
		}

		_, err := ed.CheckValidityTxValues(tx)
		assert.Error(t, err)
		assert.Equal(t, process.ErrProposalNotInitialized, err)
	})

}

func TestComputeGas(t *testing.T) {
	ed := newMockEconomicsData()

	ed.proposalController.Get().UpdateParameters(map[int32]*kapps.Parameter{
		int32(kapps.EnumParameter_MaxGasPerTX): {Type: kapps.EnumType_Int64, Value: []byte("1000")},
	})

	baseGasMultiplier := uint64(1)
	baseBandwidthFee := int64(350)

	t.Run("Should work", func(t *testing.T) {
		tx := &transaction.Transaction{
			RawData: &transaction.Transaction_Raw{
				Contract: []*transaction.TXContract{
					{Type: transaction.TXContract_TransferContractType},
				},
				Data: [][]byte{
					make([]byte, 100),
				},
				BandwidthFee: baseBandwidthFee,
				KAppFee:      10,
			},
		}

		costResponse := transaction.CostResponse{
			GasMultiplier: baseGasMultiplier,
		}

		gasLimit, gasMultiplier, err := ed.ComputeGas(tx, &costResponse)
		require.NoError(t, err)
		assert.Equal(t, baseGasMultiplier, gasMultiplier)
		assert.Equal(t, uint64(350), gasLimit)
	})

	t.Run("negative free bandwidth", func(t *testing.T) {
		tx := &transaction.Transaction{
			RawData: &transaction.Transaction_Raw{
				Contract: []*transaction.TXContract{
					{Type: transaction.TXContract_TransferContractType},
				},
				Data: [][]byte{
					make([]byte, 100),
				},
				BandwidthFee: baseBandwidthFee,
				KAppFee:      10,
			},
		}

		costResponse := transaction.CostResponse{
			GasMultiplier: baseGasMultiplier,
			BandwidthFee:  baseBandwidthFee + 1,
		}

		_, _, err := ed.ComputeGas(tx, &costResponse)
		require.Error(t, err)
		assert.Contains(t, err.Error(), process.ErrInvalidTXFees.Error())
	})

	t.Run("free bandwidth is greather than max limit per tx", func(t *testing.T) {
		tx := &transaction.Transaction{
			RawData: &transaction.Transaction_Raw{
				Contract: []*transaction.TXContract{
					{Type: transaction.TXContract_TransferContractType},
				},
				Data: [][]byte{
					make([]byte, 100),
				},
				BandwidthFee: baseBandwidthFee * 10,
				KAppFee:      10,
			},
		}

		costResponse := transaction.CostResponse{
			GasMultiplier: baseGasMultiplier,
			BandwidthFee:  baseBandwidthFee,
		}

		_, _, err := ed.ComputeGas(tx, &costResponse)
		require.Error(t, err)
		assert.Contains(t, err.Error(), process.ErrInvalidMaxGasLimitPerTx.Error())
	})

}

// Test LeaderPercentage
func TestLeaderPercentage(t *testing.T) {
	ed := newMockEconomicsData()
	assert.Equal(t, 0.5, ed.LeaderPercentage())
}

// Test IsInterfaceNil
func TestIsInterfaceNil(t *testing.T) {
	t.Run("Non-nil", func(t *testing.T) {
		ed := newMockEconomicsData()
		assert.False(t, ed.IsInterfaceNil())
	})

	t.Run("Nil", func(t *testing.T) {
		var ed *EconomicsData
		assert.True(t, ed.IsInterfaceNil())
	})
}

func TestGasParams(t *testing.T) {
	ed := newMockEconomicsData()
	ed.proposalController.Get().UpdateParameters(map[int32]*kapps.Parameter{
		int32(kapps.EnumParameter_MaxGasPerBlock): {Type: kapps.EnumType_Int64, Value: []byte("1")},
		int32(kapps.EnumParameter_MaxGasPerTX):    {Type: kapps.EnumType_Int64, Value: []byte("1")},
	})

	assert.Equal(t, uint64(1), ed.MaxGasLimitPerTX())
	assert.Equal(t, uint64(1), ed.MaxGasLimitPerBlock())

	ed.proposalController.Get().UpdateParameters(map[int32]*kapps.Parameter{
		int32(kapps.EnumParameter_MaxGasPerBlock): {Type: kapps.EnumType_Int64, Value: []byte("2")},
		int32(kapps.EnumParameter_MaxGasPerTX):    {Type: kapps.EnumType_Int64, Value: []byte("2")},
	})

	assert.Equal(t, uint64(2), ed.MaxGasLimitPerTX())
	assert.Equal(t, uint64(2), ed.MaxGasLimitPerBlock())
}

// Test EpochConfirmed
func TestEpochConfirmed(t *testing.T) {
	ed := newMockEconomicsData()
	ed.EpochConfirmed(10) // This method doesn't do anything in the current implementation
}

func TestEconomicsData_SetProposalControllerConcurrently(t *testing.T) {
	t.Parallel()

	ed := &EconomicsData{leaderPercentage: 0.5}
	tx := &transaction.Transaction{
		RawData: &transaction.Transaction_Raw{
			Contract: []*transaction.TXContract{
				{Type: transaction.TXContract_TransferContractType},
			},
		},
	}

	// the REST API serves fee reads before StartConsensus wires the controller in
	require.False(t, ed.IsProposalControllerSet())
	require.Zero(t, ed.MaxGasLimitPerTX())

	_, err := ed.ComputeTransactionCost(tx, false)
	require.Equal(t, process.ErrProposalNotInitialized, err)

	_, err = ed.CheckValidityTxValues(tx)
	require.Equal(t, process.ErrProposalNotInitialized, err)

	require.Equal(t, common.ErrNilProposalController, ed.SetProposalController(nil))

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			_ = ed.MaxGasLimitPerTX()
			_, _ = ed.ComputeTransactionCost(tx, false)
		}
	}()

	require.Nil(t, ed.SetProposalController(createProposalController(map[int32]*kapps.Parameter{
		int32(kapps.EnumParameter_MaxGasPerTX): {Type: kapps.EnumType_Uint64, Value: []byte("42")},
	})))
	<-done

	require.Equal(t, uint64(42), ed.MaxGasLimitPerTX())
	require.True(t, ed.IsProposalControllerSet())

	// wired, but MaxGasPerTX is missing until EnableSmartContracts is active: 0
	// is a real limit here, not a node still starting
	require.Nil(t, ed.SetProposalController(createProposalController(map[int32]*kapps.Parameter{})))
	require.Zero(t, ed.MaxGasLimitPerTX())
	require.True(t, ed.IsProposalControllerSet())
}

// MaxGasLimitPerBlock only runs on the consensus path, so it stays unguarded: a
// missing controller must surface rather than yield a zero gas bandwidth.
func TestEconomicsData_MaxGasLimitPerBlock(t *testing.T) {
	t.Parallel()

	ed := &EconomicsData{leaderPercentage: 0.5}
	require.Panics(t, func() { _ = ed.MaxGasLimitPerBlock() })

	require.Nil(t, ed.SetProposalController(createProposalController(map[int32]*kapps.Parameter{
		int32(kapps.EnumParameter_MaxGasPerBlock): {Type: kapps.EnumType_Uint64, Value: []byte("77")},
	})))
	require.Equal(t, uint64(77), ed.MaxGasLimitPerBlock())
}
