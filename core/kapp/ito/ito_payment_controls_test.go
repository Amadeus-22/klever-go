package ito

import (
	"encoding/hex"
	"testing"

	"github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/config"
	"github.com/klever-io/klever-go/core/kapp"
	"github.com/klever-io/klever-go/core/process"
	"github.com/klever-io/klever-go/data/block"
	"github.com/klever-io/klever-go/data/state"
	"github.com/klever-io/klever-go/data/transaction"
	"github.com/klever-io/klever-go/kapps"
	vmStub "github.com/klever-io/klever-go/kvm/mock/stub"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	controlsCurrencyID = []byte("RST-1234")
	controlsBuyer      = makeAddress("buyer")
	controlsReceiver   = makeAddress("receiver")
	controlsRoyalty    = makeAddress("royalty")
	controlsSplit      = makeAddress("split")
	controlsIssuer     = makeAddress("issuer")
)

// paymentControlsBuy is one buy of 1,000,000: a 10% royalty split half to the split and half to
// the royalty address, the remaining 900,000 to the receiver.
type paymentControlsBuy struct {
	status   transaction.Transaction_TXResultCode
	err      error
	debited  int64
	credited map[string]int64
}

func runPaymentControlsBuy(t *testing.T, fixOn bool, itoPercentage uint32, splitPercentage uint32, currency *kapps.KDAData) paymentControlsBuy {
	t.Helper()

	enableEpochs := config.EnableEpochs{}
	if !fixOn {
		enableEpochs.FixAuditChangesV6 = 1_000
	}

	itoKapp := setupITOKapp(t, enableEpochs)

	ito := &kapps.ITOData{
		IsActive:        true,
		StartTime:       1,
		EndTime:         100_00,
		ReceiverAddress: controlsReceiver,
		PackData: map[string]*kapps.PackData{
			string(controlsCurrencyID): {Packs: []*kapps.Pack{{Amount: 1_000_000, Price: 1_000_000}}},
		},
	}
	itoData, err := itoKapp.marshalizer.Marshal(ito)
	require.NoError(t, err)

	asset := &kapps.KDAData{
		Precision: 6,
		AssetType: kapps.KDAData_Fungible,
		Royalties: &kapps.RoyaltiesData{
			Address:       controlsRoyalty,
			ITOPercentage: itoPercentage,
			SplitRoyalties: map[string]*kapps.RoyaltySplitData{
				hex.EncodeToString(controlsSplit): {PercentITOPercentage: splitPercentage},
			},
		},
	}

	result := paymentControlsBuy{credited: make(map[string]int64)}
	account := func(address []byte) state.UserAccountHandler {
		return &mock.UserAccountHandlerStub{
			AddressBytesCalled: func() []byte { return address },
			SubFromBalanceCalled: func(value int64, assetID []byte, _ bool, _ ...*kapps.UserKDA) error {
				result.debited += value
				return nil
			},
			AddToBalanceCalled: func(value int64, assetID []byte, _ bool, _ ...*kapps.UserKDA) error {
				result.credited[string(address)] += value
				return nil
			},
		}
	}

	require.NoError(t, itoKapp.SetAccountsCacher(&mock.AccountsCacherStub{
		GetExistingUserCalled: func(address []byte) (state.UserAccountHandler, error) { return account(address), nil },
		LoadUserCalled:        func(address []byte) (state.UserAccountHandler, error) { return account(address), nil },
		LoadKAppCalled: func(address []byte) (state.KAppAccountHandler, error) {
			return &mock.KAppAccountHandlerStub{
				DataTrieTrackerCalled: func() state.DataTrieTracker {
					return &mock.DataTrieTrackerStub{
						RetrieveValueCalled: func(key []byte) ([]byte, error) { return itoData, nil },
						SaveKeyValueCalled:  func(key, value []byte) error { return nil },
					}
				},
			}, nil
		},
		UpdateUserCalled: func(account state.AccountHandler) error { return nil },
	}))

	require.NoError(t, itoKapp.SetKAppController(&vmStub.KAppControllerStub{
		GetCurrentKAppContextCalled: func() kapp.KappContext {
			return kapp.NewKappContext(kapp.ArgsNewKAppContext{
				Block: &block.Block{Header: &block.BlockHeader{Timestamp: 100}},
			})
		},
		GetKDAKAppCalled: func() kapp.KDAKapp {
			return &vmStub.KDAKappStub{
				GetKDACalled: func(assetID []byte) (state.KAppAccountHandler, *kapps.KDAData, error) {
					if string(assetID) == string(controlsCurrencyID) {
						return nil, currency, nil
					}
					return nil, asset, nil
				},
				MintCalled: func(sender []byte, tc *transaction.AssetTriggerContract) (transaction.Transaction_TXResultCode, error) {
					return transaction.Transaction_Ok, nil
				},
			}
		},
	}))

	result.status, result.err = itoKapp.Buy(controlsBuyer, &transaction.BuyContract{
		Amount:         1_000_000,
		CurrencyAmount: 1_000_000,
		ID:             []byte("ITO-1234"),
		CurrencyID:     controlsCurrencyID,
	})

	return result
}

func controlsCurrency(paused bool, limitTransfer bool, roles ...*kapps.RolesData) *kapps.KDAData {
	return &kapps.KDAData{
		ID:           controlsCurrencyID,
		OwnerAddress: controlsIssuer,
		AssetType:    kapps.KDAData_Fungible,
		Attributes:   &kapps.AttributesData{IsPaused: paused},
		Properties:   &kapps.PropertiesData{LimitTransfer: limitTransfer},
		Roles:        roles,
	}
}

func depositRole(address []byte) *kapps.RolesData {
	return &kapps.RolesData{Address: address, HasRoleDeposit: true}
}

func requirePaidInFull(t *testing.T, buy paymentControlsBuy) {
	t.Helper()

	require.NoError(t, buy.err)
	assert.Equal(t, transaction.Transaction_Ok, buy.status)
	assert.Equal(t, int64(1_000_000), buy.debited)
	assert.Equal(t, int64(900_000), buy.credited[string(controlsReceiver)])
	assert.Equal(t, int64(50_000), buy.credited[string(controlsRoyalty)])
	assert.Equal(t, int64(50_000), buy.credited[string(controlsSplit)])
}

// requireTransferRefused checks the buy failed, which is what undoes a destination paid before the refusal:
// the buyer was never debited and no refused destination was paid.
func requireTransferRefused(t *testing.T, buy paymentControlsBuy, refused ...[]byte) {
	t.Helper()

	require.ErrorIs(t, buy.err, process.ErrKDATransferNotAllowed)
	assert.Equal(t, transaction.Transaction_KDATransferNotAllowed, buy.status)
	assert.Zero(t, buy.debited, "every destination is checked before the buyer is debited")
	for _, destination := range refused {
		assert.Zero(t, buy.credited[string(destination)])
	}
}

func TestBuy_PaymentCurrencyControls(t *testing.T) {
	t.Parallel()

	t.Run("an unrestricted currency pays every destination", func(t *testing.T) {
		t.Parallel()
		requirePaidInFull(t, runPaymentControlsBuy(t, true, 10_00, 50_00, controlsCurrency(false, false)))
	})

	t.Run("a paused currency refuses the buy before any balance moves", func(t *testing.T) {
		t.Parallel()

		buy := runPaymentControlsBuy(t, true, 10_00, 50_00, controlsCurrency(true, false))
		require.ErrorIs(t, buy.err, process.ErrAssetIsPaused)
		assert.Equal(t, transaction.Transaction_AssetPaused, buy.status)
		assert.Zero(t, buy.debited)
		assert.Empty(t, buy.credited)
	})

	t.Run("a limit-transfer currency refuses a buyer with no route to the destinations", func(t *testing.T) {
		t.Parallel()

		buy := runPaymentControlsBuy(t, true, 10_00, 50_00, controlsCurrency(false, true))
		requireTransferRefused(t, buy, controlsReceiver, controlsRoyalty, controlsSplit)
	})

	t.Run("a buyer holding the transfer role pays every destination", func(t *testing.T) {
		t.Parallel()

		currency := controlsCurrency(false, true, &kapps.RolesData{Address: controlsBuyer, HasRoleTransfer: true})
		requirePaidInFull(t, runPaymentControlsBuy(t, true, 10_00, 50_00, currency))
	})

	t.Run("destinations holding the deposit role are paid", func(t *testing.T) {
		t.Parallel()

		currency := controlsCurrency(false, true, depositRole(controlsReceiver), depositRole(controlsRoyalty), depositRole(controlsSplit))
		requirePaidInFull(t, runPaymentControlsBuy(t, true, 10_00, 50_00, currency))
	})

	t.Run("the issuer buying with its own currency pays every destination", func(t *testing.T) {
		t.Parallel()

		currency := controlsCurrency(false, true)
		currency.OwnerAddress = controlsBuyer
		requirePaidInFull(t, runPaymentControlsBuy(t, true, 10_00, 50_00, currency))
	})

	mixed := []struct {
		name       string
		authorized [][]byte
		refused    []byte
	}{
		{"one split destination not authorized refuses the whole buy", [][]byte{controlsReceiver, controlsRoyalty}, controlsSplit},
		{"the royalty address not authorized refuses the whole buy", [][]byte{controlsReceiver, controlsSplit}, controlsRoyalty},
		{"the ITO receiver not authorized refuses the whole buy", [][]byte{controlsRoyalty, controlsSplit}, controlsReceiver},
	}
	for _, tt := range mixed {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			roles := make([]*kapps.RolesData, 0, len(tt.authorized))
			for _, address := range tt.authorized {
				roles = append(roles, depositRole(address))
			}
			requireTransferRefused(t, runPaymentControlsBuy(t, true, 10_00, 50_00, controlsCurrency(false, true, roles...)), tt.refused)
		})
	}

	t.Run("a royalty that pays nothing is not checked", func(t *testing.T) {
		t.Parallel()

		buy := runPaymentControlsBuy(t, true, 0, 50_00, controlsCurrency(false, true, depositRole(controlsReceiver)))
		require.NoError(t, buy.err)
		assert.Equal(t, transaction.Transaction_Ok, buy.status)
		assert.Equal(t, int64(1_000_000), buy.credited[string(controlsReceiver)])
		assert.Zero(t, buy.credited[string(controlsRoyalty)])
		assert.Zero(t, buy.credited[string(controlsSplit)])
	})

	t.Run("a split that pays nothing is not checked", func(t *testing.T) {
		t.Parallel()

		currency := controlsCurrency(false, true, depositRole(controlsReceiver), depositRole(controlsRoyalty))
		buy := runPaymentControlsBuy(t, true, 10_00, 0, currency)
		require.NoError(t, buy.err)
		assert.Equal(t, transaction.Transaction_Ok, buy.status)
		assert.Equal(t, int64(900_000), buy.credited[string(controlsReceiver)])
		assert.Equal(t, int64(100_000), buy.credited[string(controlsRoyalty)])
		assert.Zero(t, buy.credited[string(controlsSplit)])
	})

	t.Run("a receiver that is paid nothing is not checked", func(t *testing.T) {
		t.Parallel()

		currency := controlsCurrency(false, true, depositRole(controlsRoyalty), depositRole(controlsSplit))
		buy := runPaymentControlsBuy(t, true, 100_00, 50_00, currency)
		require.NoError(t, buy.err)
		assert.Equal(t, transaction.Transaction_Ok, buy.status)
		assert.Zero(t, buy.credited[string(controlsReceiver)])
		assert.Equal(t, int64(500_000), buy.credited[string(controlsRoyalty)])
		assert.Equal(t, int64(500_000), buy.credited[string(controlsSplit)])
	})

	t.Run("a royalty remainder that pays nothing is not checked", func(t *testing.T) {
		t.Parallel()

		currency := controlsCurrency(false, true, depositRole(controlsReceiver), depositRole(controlsSplit))
		buy := runPaymentControlsBuy(t, true, 10_00, 100_00, currency)
		require.NoError(t, buy.err)
		assert.Equal(t, transaction.Transaction_Ok, buy.status)
		assert.Equal(t, int64(900_000), buy.credited[string(controlsReceiver)])
		assert.Equal(t, int64(100_000), buy.credited[string(controlsSplit)])
		assert.Zero(t, buy.credited[string(controlsRoyalty)])
	})

	t.Run("before the fork a paused currency still pays every destination", func(t *testing.T) {
		t.Parallel()
		requirePaidInFull(t, runPaymentControlsBuy(t, false, 10_00, 50_00, controlsCurrency(true, false)))
	})

	t.Run("before the fork a limit-transfer currency still pays every destination", func(t *testing.T) {
		t.Parallel()
		requirePaidInFull(t, runPaymentControlsBuy(t, false, 10_00, 50_00, controlsCurrency(false, true)))
	})
}
