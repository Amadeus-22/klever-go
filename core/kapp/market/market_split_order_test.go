package market

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"slices"
	"testing"

	"github.com/klever-io/klever-go/core/process/kda/kdautils"
	txProcess "github.com/klever-io/klever-go/core/process/transaction"
	"github.com/klever-io/klever-go/data/transaction"
	"github.com/klever-io/klever-go/kapps"
	"github.com/stretchr/testify/require"
)

// KLC-2739: royalty splits are paid in key order
//
// Each royalty split paid at settlement adds a Transfer receipt, and a contract calling
// kleverBuy or kleverClaim reads those receipts back as transfers in receipt order. Under
// FixAuditChangesV6 both split loops, the fixed deposit in KLV and the percentage royalty in the
// currency, pay in key order, so every node emits the same receipts in the same order.

const splitOrderSplits = 8

// splitOrderCollection is a collection whose royalty is split evenly among splitOrderSplits
// addresses, for both the fixed deposit and the percentage royalty.
func splitOrderCollection() (*kapps.KDAData, [][]byte) {
	collection := controlsCollection(false, false)
	collection.Royalties.SplitRoyalties = map[string]*kapps.RoyaltySplitData{}

	splits := make([][]byte, 0, splitOrderSplits)
	for i := range splitOrderSplits {
		split := makeAddress(fmt.Sprintf("order-split-%d", splitOrderSplits-1-i))
		splits = append(splits, split)
		collection.Royalties.SplitRoyalties[hex.EncodeToString(split)] = &kapps.RoyaltySplitData{
			PercentMarketPercentage: 1000,
			PercentMarketFixed:      1000,
		}
	}

	return collection, splits
}

// splitPayees returns the receivers of the Transfer receipts paying assetID to one of splits, in
// receipt order.
func splitPayees(receipts []*receiptData, assetID []byte, splits [][]byte) [][]byte {
	var payees [][]byte
	for _, r := range receipts {
		if r.receiptType != txProcess.Transfer || !bytes.Equal(r.assetID, assetID) {
			continue
		}
		if slices.ContainsFunc(splits, func(split []byte) bool { return bytes.Equal(split, r.receiver) }) {
			payees = append(payees, r.receiver)
		}
	}

	return payees
}

type receiptData struct {
	receiptType txProcess.ReceiptType
	receiver    []byte
	assetID     []byte
}

// settleWithSplits settles an expired auction, with the fixed deposit, on splitOrderCollection
// and returns its receipts.
func settleWithSplits(t *testing.T, fixOn bool) ([]*receiptData, [][]byte) {
	t.Helper()

	collection, splits := splitOrderCollection()
	env := newControlsEnv(t, fixOn, controlsCurrency(false, false), collection)
	env.storeExpiredBid(true, ctrlFixedDeposit)

	status, err := env.claim(ctrlSeller)
	require.NoError(t, err)
	require.Equal(t, transaction.Transaction_Ok, status)
	require.True(t, env.holdsNFT(ctrlBuyer))

	var receipts []*receiptData
	for _, r := range env.receipts.Get() {
		// NewReceipt data: the type and contract ID, then the sender, the receiver, the amount
		// and the asset.
		if len(r.Data) < 5 {
			continue
		}
		receipts = append(receipts, &receiptData{
			receiptType: txProcess.ReceiptType(r.Data[0][0]),
			receiver:    r.Data[2],
			assetID:     r.Data[4],
		})
	}

	return receipts, splits
}

func TestMarketSplitRoyalties_PaidInKeyOrder(t *testing.T) {
	t.Parallel()

	sortedSplits := func(splits [][]byte) [][]byte {
		sorted := slices.Clone(splits)
		slices.SortFunc(sorted, func(a, b []byte) int {
			return bytes.Compare([]byte(hex.EncodeToString(a)), []byte(hex.EncodeToString(b)))
		})
		return sorted
	}

	t.Run("post-fork: every split is paid in key order", func(t *testing.T) {
		t.Parallel()

		// The splits are inserted in reverse key order, which no rotation of the map's slots
		// sorts, so a map-order loop fails every run; the repetition guards against luck anyway.
		for range 20 {
			receipts, splits := settleWithSplits(t, true)
			want := sortedSplits(splits)

			require.Equal(t, want, splitPayees(receipts, ctrlCurrencyID, splits), "percentage royalty splits")
			require.Equal(t, want, splitPayees(receipts, kdautils.KLVIdentifier, splits), "fixed deposit splits")
		}
	})

	t.Run("pre-fork: every split is paid, in map order", func(t *testing.T) {
		t.Parallel()

		// pre-fork: legacy behaviour, kept for replay
		receipts, splits := settleWithSplits(t, false)
		want := sortedSplits(splits)

		for _, assetID := range [][]byte{ctrlCurrencyID, kdautils.KLVIdentifier} {
			paid := sortedSplits(splitPayees(receipts, assetID, splits))
			require.Equal(t, want, paid)
		}
	})
}
