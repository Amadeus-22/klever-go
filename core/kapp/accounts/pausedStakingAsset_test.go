package accounts

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/klever-io/klever-go/common"
	commonMock "github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/config"
	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/core/kapp"
	"github.com/klever-io/klever-go/core/process"
	"github.com/klever-io/klever-go/data/block"
	"github.com/klever-io/klever-go/data/state"
	"github.com/klever-io/klever-go/data/transaction"
	"github.com/klever-io/klever-go/kapps"
	kvmStub "github.com/klever-io/klever-go/kvm/mock/stub"
)

var (
	pausedPoolAssetID = []byte("STK-1A2B")
	errClaimReached   = errors.New("the claim was reached")
)

// pausedPoolFixture is an accounts kapp whose staking asset, pool type and fork are set per case.
// The holder's claim always fails with errClaimReached, so a staking operation that gets past the
// pause check stops right there and reports that it reached the claim.
type pausedPoolFixture struct {
	kapp *accountsKapp
	ctx  kapp.KappContext
}

func newPausedPoolFixture(t *testing.T, v6, paused bool, interestType kapps.StakingData_EnumInterestType) pausedPoolFixture {
	t.Helper()

	accsKapp := setupAccountsKapp(t, config.EnableEpochs{})
	accsKapp.forkController = commonMock.NewForkControllerStub().SetFork("FixAuditChangesV6", v6)

	ctx := kapp.NewKappContext(kapp.ArgsNewKAppContext{
		OriginalSender: txSender,
		ContractID:     0,
		Block:          &block.Block{Header: &block.BlockHeader{Epoch: 5}},
	})

	require.NoError(t, accsKapp.SetKAppController(&kvmStub.KAppControllerStub{
		GetCurrentKAppContextCalled: func() kapp.KappContext { return ctx },
		GetProposalControllerCalled: func() kapps.ActiveProposalController {
			return &commonMock.ProposalControllerStub{
				GetParameterIntCalled: func(kapps.EnumParameter) int64 { return 10 },
			}
		},
		GetKDAKAppCalled: func() kapp.KDAKapp {
			return &kvmStub.KDAKappStub{
				GetKDACalled: func([]byte) (state.KAppAccountHandler, *kapps.KDAData, error) {
					return nil, &kapps.KDAData{
						ID:         pausedPoolAssetID,
						AssetType:  kapps.KDAData_Fungible,
						Attributes: &kapps.AttributesData{IsPaused: paused},
					}, nil
				},
				GetStakingCalled: func([]byte) (state.KAppAccountHandler, *kapps.StakingData, error) {
					return nil, &kapps.StakingData{InterestType: interestType}, nil
				},
			}
		},
	}))

	require.NoError(t, accsKapp.SetAccountsCacher(&commonMock.AccountsCacherStub{
		GetExistingUserCalled: func([]byte) (state.UserAccountHandler, error) {
			return &commonMock.UserAccountHandlerStub{
				GetUserKDACalled: func(_, _ []byte, _ bool) (*kapps.UserKDA, error) {
					return &kapps.UserKDA{LastClaim: &kapps.LastClaim{}}, nil
				},
				ClaimCalled: func(transaction.ClaimContract_EnumClaimType, []byte, uint32, int64, *kapps.StakingData, *kapps.KDAData, *kapps.UserKDA, core.ForkController) (map[string]int64, error) {
					return nil, errClaimReached
				},
				AddressBytesCalled: func() []byte { return txSender },
			}, nil
		},
	}))

	return pausedPoolFixture{kapp: accsKapp, ctx: ctx}
}

func stakingOperations() []struct {
	name string
	run  func(a *accountsKapp) (transaction.Transaction_TXResultCode, error)
} {
	return []struct {
		name string
		run  func(a *accountsKapp) (transaction.Transaction_TXResultCode, error)
	}{
		{name: "ClaimStaking", run: func(a *accountsKapp) (transaction.Transaction_TXResultCode, error) {
			return a.ClaimStaking(txSender, &transaction.ClaimContract{
				ClaimType: transaction.ClaimContract_StakingClaim,
				ID:        pausedPoolAssetID,
			})
		}},
		{name: "Freeze", run: func(a *accountsKapp) (transaction.Transaction_TXResultCode, error) {
			return a.Freeze(txSender, &transaction.FreezeContract{AssetID: pausedPoolAssetID, Amount: 1})
		}},
		{name: "Unfreeze", run: func(a *accountsKapp) (transaction.Transaction_TXResultCode, error) {
			return a.Unfreeze(txSender, &transaction.UnfreezeContract{AssetID: pausedPoolAssetID, BucketID: testBucketID})
		}},
		{name: "Withdraw", run: func(a *accountsKapp) (transaction.Transaction_TXResultCode, error) {
			return a.Withdraw(txSender, &transaction.WithdrawContract{AssetID: pausedPoolAssetID})
		}},
	}
}

// After the fork, every staking operation on a paused asset is refused before the claim it carries,
// whatever the interest type of the pool.
func TestAccountsKapp_PausedStakingAssetRefusesTheStakingOperations(t *testing.T) {
	t.Parallel()

	pools := []struct {
		name         string
		interestType kapps.StakingData_EnumInterestType
	}{
		{name: "FPR pool", interestType: kapps.StakingData_FPRI},
		{name: "APR pool", interestType: kapps.StakingData_APRI},
	}
	for _, pool := range pools {
		for _, op := range stakingOperations() {
			t.Run(pool.name+"/"+op.name, func(t *testing.T) {
				t.Parallel()

				f := newPausedPoolFixture(t, true, true, pool.interestType)

				code, err := op.run(f.kapp)

				require.ErrorIs(t, err, process.ErrAssetIsPaused)
				assert.Equal(t, transaction.Transaction_AssetPaused, code)

				receipts := f.ctx.Receipts().Get()
				require.Len(t, receipts, 1)
				assert.Equal(t, common.ErrFieldAssetPaused, string(receipts[0].Data[1]))
				assert.Equal(t, process.ErrAssetIsPaused.Error(), string(receipts[0].Data[2]))
			})
		}
	}
}

// The pause check lets the operation through to its claim before the fork, and on an asset that is
// not paused.
func TestAccountsKapp_PausedStakingAssetCheckLetsTheOtherCasesThrough(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		v6           bool
		paused       bool
		interestType kapps.StakingData_EnumInterestType
	}{
		{name: "pre fork, paused FPR pool", v6: false, paused: true, interestType: kapps.StakingData_FPRI},
		{name: "pre fork, paused APR pool", v6: false, paused: true, interestType: kapps.StakingData_APRI},
		{name: "post fork, FPR pool not paused", v6: true, paused: false, interestType: kapps.StakingData_FPRI},
		{name: "post fork, APR pool not paused", v6: true, paused: false, interestType: kapps.StakingData_APRI},
	}
	for _, tt := range cases {
		for _, op := range stakingOperations() {
			t.Run(tt.name+"/"+op.name, func(t *testing.T) {
				t.Parallel()

				f := newPausedPoolFixture(t, tt.v6, tt.paused, tt.interestType)

				code, err := op.run(f.kapp)

				require.ErrorIs(t, err, errClaimReached)
				assert.NotEqual(t, transaction.Transaction_AssetPaused, code)
			})
		}
	}
}

// A record the claim needs that fails to load stops the operation with the code of that record.
func TestAccountsKapp_LoadStakingClaimStateReportsTheRecordThatFailed(t *testing.T) {
	t.Parallel()

	errLoad := errors.New("the record could not be loaded")

	cases := []struct {
		name         string
		failing      string
		expectedCode transaction.Transaction_TXResultCode
	}{
		{name: "the staking record", failing: "staking", expectedCode: transaction.Transaction_AssetError},
		{name: "the asset record", failing: "kda", expectedCode: transaction.Transaction_KAPPError},
		{name: "the holder's record", failing: "userKDA", expectedCode: transaction.Transaction_AccountError},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			failOn := func(record string) error {
				if record == tt.failing {
					return errLoad
				}

				return nil
			}

			accsKapp := setupAccountsKapp(t, config.EnableEpochs{})
			require.NoError(t, accsKapp.SetKAppController(setupKappController(&kvmStub.KAppControllerStub{
				GetKDAKAppCalled: func() kapp.KDAKapp {
					return &kvmStub.KDAKappStub{
						GetKDACalled: func([]byte) (state.KAppAccountHandler, *kapps.KDAData, error) {
							return nil, &kapps.KDAData{}, failOn("kda")
						},
						GetStakingCalled: func([]byte) (state.KAppAccountHandler, *kapps.StakingData, error) {
							return nil, &kapps.StakingData{}, failOn("staking")
						},
					}
				},
			})))

			ownerAcc := &commonMock.UserAccountHandlerStub{
				GetUserKDACalled: func(_, _ []byte, _ bool) (*kapps.UserKDA, error) {
					return &kapps.UserKDA{}, failOn("userKDA")
				},
			}

			claimState, code, err := accsKapp.loadStakingClaimState(ownerAcc, pausedPoolAssetID)

			require.ErrorIs(t, err, errLoad)
			assert.Equal(t, tt.expectedCode, code)
			assert.Nil(t, claimState)
		})
	}
}
