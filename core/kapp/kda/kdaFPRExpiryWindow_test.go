package kda_test

import (
	"math"
	"strconv"
	"testing"

	"github.com/klever-io/klever-go/config"
	"github.com/klever-io/klever-go/core/fork"
	"github.com/klever-io/klever-go/core/kapp"
	"github.com/klever-io/klever-go/data/transaction"
	"github.com/klever-io/klever-go/eventNotifier/notifier"
	"github.com/klever-io/klever-go/kapps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setMaxEpochsUnclaimed writes the parameter the way a settled proposal does.
func setMaxEpochsUnclaimed(t *testing.T, kc kapp.KAppController, value int64) {
	t.Helper()

	kc.GetProposalController().UpdateParameters(map[int32]*kapps.Parameter{
		int32(kapps.EnumParameter_MaxEpochsUnclaimed): {
			Type:  kapps.EnumType_Int64,
			Value: []byte(strconv.FormatInt(value, 10)),
		},
	})
	require.Equal(t, value, kc.GetProposalController().GetParameterInt(kapps.EnumParameter_MaxEpochsUnclaimed))
}

// fprWithUnclaimed is a pool bucket that still owes 60 KLV and 20 of the asset.
func fprWithUnclaimed(epoch uint32) *kapps.FPRData {
	return &kapps.FPRData{
		Epoch:        epoch,
		TotalAmount:  100,
		TotalClaimed: 40,
		KDAS: map[string]*kapps.KDAFPRData{
			string(defaultAssetID): {TotalAmount: 30, TotalClaimed: 10},
		},
	}
}

// The expiry window of a pool bucket is its epoch plus MaxEpochsUnclaimed. Summed in uint32, a
// parameter that saturates the conversion wrapped the sum around zero, so every bucket past
// epoch 0 looked expired and the next deposit paid its unclaimed rewards out to the depositor.
func TestKDAKapp_DepositFPRExpiryWindow(t *testing.T) {
	t.Parallel()

	const (
		blockEpoch    = uint32(200)
		depositAmount = int64(10)
		unclaimedKLV  = int64(60)
		unclaimedKDA  = int64(20)
	)

	tests := []struct {
		name               string
		fixAuditChangesV6  bool
		maxEpochsUnclaimed int64
		fprEpochs          []uint32
		// keptEpochs are the buckets left in the pool, besides the one the deposit lands in
		keptEpochs []uint32
	}{
		{
			name:               "post fork, a parameter of MaxUint32 expires nothing",
			fixAuditChangesV6:  true,
			maxEpochsUnclaimed: math.MaxUint32,
			fprEpochs:          []uint32{1, 150, 200},
			keptEpochs:         []uint32{1, 150, 200},
		},
		{
			name:               "post fork, a parameter beyond uint32 expires nothing",
			fixAuditChangesV6:  true,
			maxEpochsUnclaimed: math.MaxInt64,
			fprEpochs:          []uint32{1, 150, 200},
			keptEpochs:         []uint32{1, 150, 200},
		},
		{
			name:               "post fork, a small parameter still expires what fell out of the window",
			fixAuditChangesV6:  true,
			maxEpochsUnclaimed: 10,
			// 190+10 reaches the block epoch, 189+10 does not
			fprEpochs:  []uint32{1, 189, 190},
			keptEpochs: []uint32{190},
		},
		{
			name:               "pre fork, a small parameter expires what fell out of the window",
			fixAuditChangesV6:  false,
			maxEpochsUnclaimed: 10,
			fprEpochs:          []uint32{1, 189, 190},
			keptEpochs:         []uint32{190},
		},
		{
			name:               "pre fork, a parameter of MaxUint32 still wraps and expires every bucket",
			fixAuditChangesV6:  false,
			maxEpochsUnclaimed: math.MaxUint32,
			fprEpochs:          []uint32{1, 150, 200},
			keptEpochs:         []uint32{},
		},
		{
			name:               "pre fork, a parameter beyond uint32 still wraps and expires every bucket",
			fixAuditChangesV6:  false,
			maxEpochsUnclaimed: math.MaxInt64,
			fprEpochs:          []uint32{1, 150, 200},
			keptEpochs:         []uint32{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			kc, err := createMockControllers()
			require.NoError(t, err)
			createStakingAsset(t, kc, transaction.StakingInfo_FPRI)
			setFixAuditChangesV6(t, kc, tt.fixAuditChangesV6)
			setMaxEpochsUnclaimed(t, kc, tt.maxEpochsUnclaimed)

			fprs := make([]*kapps.FPRData, 0, len(tt.fprEpochs))
			for _, epoch := range tt.fprEpochs {
				fprs = append(fprs, fprWithUnclaimed(epoch))
			}
			overwriteFPR(t, kc, fprs)

			cacher := kc.GetKDAKApp().GetAccountsCacher()
			before, err := cacher.GetExistingUser(defaultSender)
			require.NoError(t, err)
			klvBefore := before.GetBalance(nil, true)
			assetBefore := before.GetBalance(defaultAssetID, true)

			setBlockEpoch(kc, blockEpoch)

			code, err := kc.GetKDAKApp().Deposit(defaultSender, &transaction.DepositContract{
				ID:         defaultAssetID,
				CurrencyID: defaultAssetID,
				Amount:     depositAmount,
			})
			require.NoError(t, err)
			require.Equal(t, transaction.Transaction_Ok, code)

			_, staking, err := kc.GetKDAKApp().GetStaking(defaultAssetID)
			require.NoError(t, err)

			gotEpochs := make([]uint32, 0, len(staking.FPR))
			for _, fpr := range staking.FPR {
				gotEpochs = append(gotEpochs, fpr.GetEpoch())
			}
			wantEpochs := append(append([]uint32{}, tt.keptEpochs...), blockEpoch+1)
			require.Equal(t, wantEpochs, gotEpochs)

			// only the buckets that left the pool are paid back to the depositor
			expired := int64(len(tt.fprEpochs) - len(tt.keptEpochs))
			after, err := cacher.GetExistingUser(defaultSender)
			require.NoError(t, err)
			assert.Equal(t, klvBefore+expired*unclaimedKLV, after.GetBalance(nil, true))
			assert.Equal(t, assetBefore+expired*unclaimedKDA-depositAmount, after.GetBalance(defaultAssetID, true))

			// and the ones that stayed still hold what they owe
			for _, fpr := range staking.FPR[:len(tt.keptEpochs)] {
				assert.Equal(t, int64(100), fpr.GetTotalAmount(), "epoch %d", fpr.GetEpoch())
				assert.Equal(t, int64(40), fpr.GetTotalClaimed(), "epoch %d", fpr.GetEpoch())
				require.Contains(t, fpr.GetKDAS(), string(defaultAssetID), "epoch %d", fpr.GetEpoch())
				assert.Equal(t, int64(30), fpr.GetKDAS()[string(defaultAssetID)].GetTotalAmount(), "epoch %d", fpr.GetEpoch())
				assert.Equal(t, int64(10), fpr.GetKDAS()[string(defaultAssetID)].GetTotalClaimed(), "epoch %d", fpr.GetEpoch())
			}
		})
	}
}

// One pool across the activation epoch, with the gate answered by the production fork controller
// fed by the epoch notifier. The parameter saturates the uint32 conversion throughout: the last
// deposit before the fork still wraps and pays every bucket out, and from the activation epoch on
// a deposit expires nothing.
func TestKDAKapp_DepositFPRExpiryWindowAcrossTheForkActivationEpoch(t *testing.T) {
	t.Parallel()

	const (
		activationEpoch = uint32(200)
		depositAmount   = int64(10)
		unclaimedKLV    = int64(60)
		unclaimedKDA    = int64(20)
	)

	epochNotifier := notifier.NewGenericEpochNotifier()
	forkController, err := fork.NewForkController(
		config.EnableEpochs{FixAuditChangesV6: activationEpoch},
		epochNotifier,
	)
	require.NoError(t, err)

	kc, err := createMockControllersWithFork(forkController)
	require.NoError(t, err)
	createStakingAsset(t, kc, transaction.StakingInfo_FPRI)
	setMaxEpochsUnclaimed(t, kc, math.MaxUint32)
	overwriteFPR(t, kc, []*kapps.FPRData{fprWithUnclaimed(1), fprWithUnclaimed(150)})

	cacher := kc.GetKDAKApp().GetAccountsCacher()
	balances := func() (int64, int64) {
		acc, errGet := cacher.GetExistingUser(defaultSender)
		require.NoError(t, errGet)

		return acc.GetBalance(nil, true), acc.GetBalance(defaultAssetID, true)
	}
	// depositAt confirms the epoch, as block processing does, deposits in it and returns the
	// epochs of the buckets the pool is left with
	depositAt := func(epoch uint32) []uint32 {
		epochNotifier.CheckEpoch(epoch)
		setBlockEpoch(kc, epoch)

		code, errDeposit := kc.GetKDAKApp().Deposit(defaultSender, &transaction.DepositContract{
			ID:         defaultAssetID,
			CurrencyID: defaultAssetID,
			Amount:     depositAmount,
		})
		require.NoError(t, errDeposit)
		require.Equal(t, transaction.Transaction_Ok, code)

		_, staking, errStaking := kc.GetKDAKApp().GetStaking(defaultAssetID)
		require.NoError(t, errStaking)

		epochs := make([]uint32, 0, len(staking.FPR))
		for _, fpr := range staking.FPR {
			epochs = append(epochs, fpr.GetEpoch())
		}

		return epochs
	}

	klvBefore, assetBefore := balances()

	// pre fork: both buckets wrap below the block epoch and are paid out to the depositor
	assert.Equal(t, []uint32{activationEpoch}, depositAt(activationEpoch-1))
	require.False(t, forkController.FixAuditChangesV6())

	klvAfter, assetAfter := balances()
	assert.Equal(t, klvBefore+2*unclaimedKLV, klvAfter)
	assert.Equal(t, assetBefore+2*unclaimedKDA-depositAmount, assetAfter)

	// activation: the bucket of the previous deposit would wrap just the same, and stays
	klvBefore, assetBefore = klvAfter, assetAfter
	assert.Equal(t, []uint32{activationEpoch, activationEpoch + 1}, depositAt(activationEpoch))
	require.True(t, forkController.FixAuditChangesV6())

	klvAfter, assetAfter = balances()
	assert.Equal(t, klvBefore, klvAfter)
	assert.Equal(t, assetBefore-depositAmount, assetAfter)

	// post fork: nothing expires either
	klvBefore, assetBefore = klvAfter, assetAfter
	assert.Equal(t,
		[]uint32{activationEpoch, activationEpoch + 1, activationEpoch + 2},
		depositAt(activationEpoch+1),
	)

	klvAfter, assetAfter = balances()
	assert.Equal(t, klvBefore, klvAfter)
	assert.Equal(t, assetBefore-depositAmount, assetAfter)
}
