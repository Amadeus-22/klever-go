package accounts

import (
	"bytes"
	"encoding/hex"
	"slices"
	"testing"

	commonMock "github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/core/kapp"
	"github.com/klever-io/klever-go/data/block"
	"github.com/klever-io/klever-go/data/state"
	"github.com/klever-io/klever-go/data/transaction"
	integrationMock "github.com/klever-io/klever-go/integrationTest/mock"
	"github.com/klever-io/klever-go/kapps"
	kvmStub "github.com/klever-io/klever-go/kvm/mock/stub"
	"github.com/stretchr/testify/require"
)

// TestRoyaltiesTransfer_SplitReceiptsInKeyOrder pins the order of the transfer royalty split receipts, fixed
// and percentage: they follow the sorted split keys on every run, not Go's random map order (KLC-2799). The sort
// is not fork gated, so the order must hold with FixAuditChangesV6 both on and off.
func TestRoyaltiesTransfer_SplitReceiptsInKeyOrder(t *testing.T) {
	const numSplits = 8
	senderAddr := bytes.Repeat([]byte{0x01}, 32)
	ownerAddr := bytes.Repeat([]byte{0xff}, 32)
	assetID := []byte("FUNGI-1234")

	splitRoyalties := make(map[string]*kapps.RoyaltySplitData, numSplits)
	expected := make([][]byte, 0, numSplits)
	for i := numSplits; i > 0; i-- {
		addr := bytes.Repeat([]byte{byte(i * 0x11)}, 32)
		splitRoyalties[hex.EncodeToString(addr)] = &kapps.RoyaltySplitData{
			PercentTransferFixed:      10_00,
			PercentTransferPercentage: 10_00,
		}
		expected = append(expected, addr)
	}
	slices.SortFunc(expected, bytes.Compare)

	kda := &kapps.KDAData{
		AssetType:    kapps.KDAData_Fungible,
		OwnerAddress: ownerAddr,
		Royalties: &kapps.RoyaltiesData{
			Address:       ownerAddr,
			TransferFixed: 1_000_000,
			TransferPercentage: []*kapps.RoyaltyData{
				{Amount: 1000, Percentage: 500},
			},
			SplitRoyalties: splitRoyalties,
		},
	}

	acntSrc := &commonMock.UserAccountHandlerStub{
		AddressBytesCalled: func() []byte { return senderAddr },
		GetBalanceCalled:   func(_ []byte, _ bool) int64 { return 1_000_000_000 },
		SubFromBalanceCalled: func(_ int64, _ []byte, _ bool, _ ...*kapps.UserKDA) error {
			return nil
		},
	}
	acntDst := &commonMock.UserAccountHandlerStub{
		AddressBytesCalled: func() []byte { return ownerAddr },
	}

	newKApp := func(ctx kapp.KappContext, v6 bool) *accountsKapp {
		return &accountsKapp{
			accountsCacher: &commonMock.AccountsCacherStub{
				LoadUserCalled: func(address []byte) (state.UserAccountHandler, error) {
					return &commonMock.UserAccountHandlerStub{
						AddressBytesCalled: func() []byte { return address },
						AddToBalanceCalled: func(_ int64, _ []byte, _ bool, _ ...*kapps.UserKDA) error {
							return nil
						},
					}, nil
				},
				UpdateUserCalled: func(_ state.AccountHandler) error { return nil },
			},
			forkController: &integrationMock.ForkControllerStub{
				KdaFprCalled:               func() bool { return true },
				EnableSmartContractsCalled: func() bool { return true },
				FixMarketBuyOverflowCalled: func() bool { return true },
				FixAuditChangesV6Called:    func() bool { return v6 },
			},
			KAppController: &kvmStub.KAppControllerStub{
				GetCurrentKAppContextCalled: func() kapp.KappContext { return ctx },
			},
		}
	}
	newCtx := func() kapp.KappContext {
		return kapp.NewKappContext(kapp.ArgsNewKAppContext{
			OriginalSender: senderAddr,
			ContractType:   transaction.TXContract_TransferContractType,
			Block:          &block.Block{},
		})
	}
	requireKeyOrder := func(t *testing.T, ctx kapp.KappContext, run int) {
		t.Helper()
		receipts := ctx.Receipts().Get()
		require.Len(t, receipts, numSplits+1, "one receipt per split plus the owner remainder")
		for i, want := range expected {
			require.Equal(t, want, receipts[i].Data[2], "run %d: split receipt %d out of key order", run, i)
		}
	}

	for _, v6 := range []bool{true, false} {
		name := "v6_off"
		if v6 {
			name = "v6_on"
		}

		t.Run(name+"/fixed", func(t *testing.T) {
			for run := 0; run < 20; run++ {
				ctx := newCtx()
				tc := &transaction.TransferContract{Amount: 800, KLVRoyalties: kda.Royalties.TransferFixed}
				status, err := newKApp(ctx, v6).processFixedRoyaltiesTransfer(tc, acntSrc, acntDst, kda)
				require.NoError(t, err)
				require.Equal(t, transaction.Transaction_Ok, status)
				requireKeyOrder(t, ctx, run)
			}
		})

		t.Run(name+"/percentage", func(t *testing.T) {
			for run := 0; run < 20; run++ {
				ctx := newCtx()
				tc := &transaction.TransferContract{Amount: 800, KDARoyalties: 40}
				status, err := newKApp(ctx, v6).processPercentageRoyaltiesTransfer(tc, assetID, nil, acntSrc, acntDst, kda)
				require.NoError(t, err)
				require.Equal(t, transaction.Transaction_Ok, status)
				requireKeyOrder(t, ctx, run)
			}
		})
	}
}
