package ito

import (
	"bytes"
	"encoding/hex"
	"math"
	"slices"
	"testing"

	"github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/config"
	"github.com/klever-io/klever-go/core/kapp"
	"github.com/klever-io/klever-go/data/state"
	"github.com/klever-io/klever-go/data/transaction"
	"github.com/klever-io/klever-go/kapps"
	"github.com/stretchr/testify/require"
)

// TestComputeITOFixedBuyRoyalties_SplitReceiptsInKeyOrder pins the order of the ITO fixed split receipts:
// they follow the sorted split keys on every run, not Go's random map order (KLC-2799). The sort is not fork
// gated, so the order must hold with FixAuditChangesV6 both on and off.
func TestComputeITOFixedBuyRoyalties_SplitReceiptsInKeyOrder(t *testing.T) {
	const numSplits = 8
	splitRoyalties := make(map[string]*kapps.RoyaltySplitData, numSplits)
	expected := make([][]byte, 0, numSplits)
	for i := numSplits; i > 0; i-- {
		addr := bytes.Repeat([]byte{byte(i * 0x11)}, 32)
		splitRoyalties[hex.EncodeToString(addr)] = &kapps.RoyaltySplitData{PercentITOFixed: 10_00}
		expected = append(expected, addr)
	}
	slices.SortFunc(expected, bytes.Compare)

	ownerAddr := bytes.Repeat([]byte{0xff}, 32)
	asset := &kapps.KDAData{
		Royalties: &kapps.RoyaltiesData{
			Address:        ownerAddr,
			ITOFixed:       1_000_000,
			SplitRoyalties: splitRoyalties,
		},
	}
	ownerAcc := &mock.UserAccountHandlerStub{
		AddressBytesCalled: func() []byte { return ownerAddr },
		GetBalanceCalled:   func(_ []byte, _ bool) int64 { return 1_000_000_000_000 },
		SubFromBalanceCalled: func(_ int64, _ []byte, _ bool, _ ...*kapps.UserKDA) error {
			return nil
		},
	}

	for _, tc := range []struct {
		name    string
		v6Epoch uint32
	}{
		{name: "v6_on", v6Epoch: 0},
		{name: "v6_off", v6Epoch: math.MaxUint32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			itoKapp := setupITOKapp(t, config.EnableEpochs{
				SmartContracts:    0,
				FixAuditChangesV6: tc.v6Epoch,
			})
			require.Equal(t, tc.v6Epoch == 0, itoKapp.forkController.FixAuditChangesV6())

			err := itoKapp.SetAccountsCacher(&mock.AccountsCacherStub{
				LoadUserCalled: func(address []byte) (state.UserAccountHandler, error) {
					return &mock.UserAccountHandlerStub{
						AddressBytesCalled: func() []byte { return address },
						AddToBalanceCalled: func(_ int64, _ []byte, _ bool, _ ...*kapps.UserKDA) error {
							return nil
						},
					}, nil
				},
				UpdateUserCalled: func(_ state.AccountHandler) error { return nil },
			})
			require.NoError(t, err)

			for run := 0; run < 20; run++ {
				ctx := kapp.NewKappContext(kapp.ArgsNewKAppContext{})
				status, err := itoKapp.ComputeITOFixedBuyRoyalties(ctx, asset, ownerAcc, 100_000_000)
				require.NoError(t, err)
				require.Equal(t, transaction.Transaction_Ok, status)

				receipts := ctx.Receipts().Get()
				require.Len(t, receipts, numSplits+1, "one receipt per split plus the owner remainder")
				for i, want := range expected {
					require.Equal(t, want, receipts[i].Data[2], "run %d: split receipt %d out of key order", run, i)
				}
			}
		})
	}
}
