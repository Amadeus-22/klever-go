package transaction_test

import (
	"math"
	"testing"
	"time"

	"github.com/klever-io/klever-go/config"
	"github.com/klever-io/klever-go/core/process"
	"github.com/klever-io/klever-go/core/process/kda/kdautils"
	"github.com/klever-io/klever-go/data/block"
	"github.com/klever-io/klever-go/data/transaction"
	"github.com/klever-io/klever-go/kapps"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// Transaction level tests of the asset controls (pause, LimitTransfer) that the market, the ITO
// buy and the mint enforce from FixAuditChangesV6. They run through the real transaction
// processor, and the fork is switched between transactions on the same state.

const (
	acFunds = int64(10_000_000_000)
	acBid   = int64(1_200_000)
	acPrice = int64(1_200_000)
	acMint  = int64(3)
)

var acCurrencyID = []byte("RST")

// setV6 swaps the transaction processor for one whose FixAuditChangesV6 is on or off, keeping
// the accounts and the market state.
func (c *Controller) setV6(active bool) {
	epoch := uint32(math.MaxUint32)
	if active {
		epoch = 0
	}

	c.UpdateForkController(config.EnableEpochs{FixAuditChangesV6: epoch})
}

// saveAssetControlsCurrency stores a fungible currency, optionally transfer-restricted, with no
// roles.
func (c *Controller) saveAssetControlsCurrency(limitTransfer bool) {
	currency := kapps.KDAData{
		ID:           acCurrencyID,
		AssetType:    kapps.KDAData_Fungible,
		Name:         acCurrencyID,
		Ticker:       acCurrencyID,
		OwnerAddress: testWhitelistAddress,
		Properties:   &kapps.PropertiesData{LimitTransfer: limitTransfer},
		Attributes:   &kapps.AttributesData{},
	}

	data, err := marshalizer.Marshal(&currency)
	require.NoError(c.t, err)
	require.NoError(c.t, c.kdaKapp.DataTrieTracker().SaveKeyValue(kdautils.ToKDAKey(acCurrencyID, nil), data))
	require.NoError(c.t, c.kappDB.SaveAccount(c.kdaKapp))
}

func (c *Controller) setAssetPaused(assetID []byte, paused bool) {
	asset, err := c.GetAsset(assetID)
	require.NoError(c.t, err)

	asset.Attributes.IsPaused = paused

	data, err := marshalizer.Marshal(asset)
	require.NoError(c.t, err)
	require.NoError(c.t, c.kdaKapp.DataTrieTracker().SaveKeyValue(kdautils.ToKDAKey(assetID, nil), data))
	require.NoError(c.t, c.kappDB.SaveAccount(c.kdaKapp))
}

// runAC processes a signed transaction and returns it with the processing error, leaving the
// assertion to the caller.
func (c *Controller) runAC(blk *block.Block, from []byte, ty transaction.TXContract_ContractType, contract proto.Message) (*transaction.Transaction, error) {
	tx, err := createTransactionMock(contract, ty, from, 0)
	require.NoError(c.t, err)

	_, hash, err := c.execTx.PreProcessTransaction(tx)
	require.NoError(c.t, err)

	return tx, c.execTx.ProcessTransaction(blk, hash, tx)
}

func (c *Controller) mustRunAC(blk *block.Block, from []byte, ty transaction.TXContract_ContractType, contract proto.Message) *transaction.Transaction {
	tx, err := c.runAC(blk, from, ty, contract)
	require.NoError(c.t, err)
	require.Equal(c.t, transaction.Transaction_Ok, tx.ResultCode)

	return tx
}

func (c *Controller) expectRefused(tx *transaction.Transaction, err error, code transaction.Transaction_TXResultCode, want error) {
	require.ErrorIs(c.t, err, want)
	require.Equal(c.t, code, tx.ResultCode)
}

type assetControlsMarket struct {
	*Controller
	marketplaceID []byte
	nft           []byte
	seller        []byte
	buyer         []byte
}

func nowBlock(c *Controller, nonce uint64) *block.Block {
	return c.CreateBlockHeader(0, 1, nonce)
}

func laterBlock(c *Controller, nonce uint64) *block.Block {
	return c.CreateBlockHeader(time.Now().AddDate(0, 0, 2).Unix(), 1, nonce)
}

// newAssetControlsMarket builds a market with an NFT (TEST/1) held by the seller and a buyer
// holding acFunds of the currency. The fork is set before anything is stored.
func newAssetControlsMarket(t *testing.T, v6 bool, limitTransfer bool) *assetControlsMarket {
	c := NewController(t)
	c.setV6(v6)

	c.CreateAssetWithSplitRoyalties(kapps.KDAData_NonFungible, &kapps.RoyaltiesData{})
	c.saveAssetControlsCurrency(limitTransfer)

	c.AddUser(sender, acFunds, kdautils.KLVIdentifier)
	c.AddUser(receiver, acFunds, kdautils.KLVIdentifier)
	c.AddUser(receiver, acFunds, acCurrencyID)
	c.AddUser(sender, 0, acCurrencyID)

	blk := nowBlock(c, 1)
	c.RunMintTX(blk, sender, sender, assetWithRoyaltiesIdentifier)
	marketplaceID := c.RunCreateMarketplaceTX(blk, sender, marketplaceReferralAddress, 0)

	return &assetControlsMarket{
		Controller:    c,
		marketplaceID: marketplaceID,
		nft:           []byte(string(assetWithRoyaltiesIdentifier) + kapps.Sp + "1"),
		seller:        sender,
		buyer:         receiver,
	}
}

func (m *assetControlsMarket) sellContract(marketType transaction.SellContract_EnumMarketType, price, reserve int64) *transaction.SellContract {
	return &transaction.SellContract{
		MarketType:    marketType,
		MarketplaceID: m.marketplaceID,
		AssetID:       m.nft,
		CurrencyID:    acCurrencyID,
		Price:         price,
		ReservePrice:  reserve,
		EndTime:       time.Now().AddDate(0, 0, 1).Unix(),
	}
}

func (m *assetControlsMarket) sellBuyItNow() []byte {
	return m.RunSellOrderTX(nowBlock(m.Controller, 2), m.seller, 0, m.marketplaceID, m.nft, acCurrencyID, acPrice, 0, time.Now().AddDate(0, 0, 1).Unix())
}

func (m *assetControlsMarket) sellAuction() []byte {
	return m.RunSellOrderTX(nowBlock(m.Controller, 2), m.seller, 1, m.marketplaceID, m.nft, acCurrencyID, 0, 1_000_000, time.Now().AddDate(0, 0, 1).Unix())
}

func (m *assetControlsMarket) buy(orderID []byte, amount int64) (*transaction.Transaction, error) {
	return m.runAC(nowBlock(m.Controller, 3), m.buyer, transaction.TXContract_BuyContractType, &transaction.BuyContract{
		BuyType:    transaction.BuyContract_MarketBuy,
		ID:         orderID,
		CurrencyID: acCurrencyID,
		Amount:     amount,
	})
}

func (m *assetControlsMarket) claim(blk *block.Block, from, orderID []byte) (*transaction.Transaction, error) {
	return m.runAC(blk, from, transaction.TXContract_ClaimContractType, &transaction.ClaimContract{
		ClaimType: transaction.ClaimContract_MarketClaim,
		ID:        orderID,
	})
}

// balances holds every balance the scenarios look at.
type acBalances struct {
	sellerKLV, buyerKLV, sellerCur, buyerCur, sellerNFT, buyerNFT int64
}

func (m *assetControlsMarket) balances() acBalances {
	get := func(addr, id []byte) int64 {
		return loadUserAccount(m.accCacher, addr).GetBalance(id, true)
	}

	return acBalances{
		sellerKLV: get(m.seller, kdautils.KLVIdentifier),
		buyerKLV:  get(m.buyer, kdautils.KLVIdentifier),
		sellerCur: get(m.seller, acCurrencyID),
		buyerCur:  get(m.buyer, acCurrencyID),
		sellerNFT: get(m.seller, m.nft),
		buyerNFT:  get(m.buyer, m.nft),
	}
}

var forkModes = []struct {
	name string
	v6   bool
}{{"pre-fork", false}, {"post-fork", true}}

// 1. Buy-it-now priced in a paused currency.
func TestAssetControls_MarketBuyPausedCurrency(t *testing.T) {
	for _, mode := range forkModes {
		t.Run(mode.name, func(t *testing.T) {
			m := newAssetControlsMarket(t, mode.v6, false)
			orderID := m.sellBuyItNow()
			m.setAssetPaused(acCurrencyID, true)
			before := m.balances()
			require.Equal(t, int64(0), before.sellerNFT)

			tx, err := m.buy(orderID, acPrice)

			if !mode.v6 {
				require.NoError(t, err)
				require.Equal(t, transaction.Transaction_Ok, tx.ResultCode)
				require.Equal(t, acBalances{before.sellerKLV, before.buyerKLV, acPrice, acFunds - acPrice, 0, 1}, m.balances())
				return
			}

			m.expectRefused(tx, err, transaction.Transaction_AssetPaused, process.ErrAssetIsPaused)
			require.Equal(t, before, m.balances())
		})
	}
}

// 2. Buy and Sell of an NFT from a paused collection.
func TestAssetControls_MarketBuyPausedCollection(t *testing.T) {
	for _, mode := range forkModes {
		t.Run(mode.name, func(t *testing.T) {
			m := newAssetControlsMarket(t, mode.v6, false)
			orderID := m.sellBuyItNow()
			m.setAssetPaused(assetWithRoyaltiesIdentifier, true)
			before := m.balances()

			tx, err := m.buy(orderID, acPrice)

			if !mode.v6 {
				require.NoError(t, err)
				require.Equal(t, transaction.Transaction_Ok, tx.ResultCode)
				require.Equal(t, acBalances{before.sellerKLV, before.buyerKLV, acPrice, acFunds - acPrice, 0, 1}, m.balances())
				return
			}

			m.expectRefused(tx, err, transaction.Transaction_AssetPaused, process.ErrAssetIsPaused)
			require.Equal(t, before, m.balances())
		})
	}
}

func TestAssetControls_MarketSellPausedCollection(t *testing.T) {
	for _, mode := range forkModes {
		t.Run(mode.name, func(t *testing.T) {
			m := newAssetControlsMarket(t, mode.v6, false)
			m.setAssetPaused(assetWithRoyaltiesIdentifier, true)
			before := m.balances()
			require.Equal(t, int64(1), before.sellerNFT)

			tx, err := m.runAC(nowBlock(m.Controller, 2), m.seller, transaction.TXContract_SellContractType,
				m.sellContract(transaction.SellContract_BuyItNowMarket, acPrice, 0))

			if !mode.v6 {
				require.NoError(t, err)
				require.Equal(t, transaction.Transaction_Ok, tx.ResultCode)
				after := before
				after.sellerNFT = 0
				require.Equal(t, after, m.balances())
				return
			}

			m.expectRefused(tx, err, transaction.Transaction_AssetPaused, process.ErrAssetIsPaused)
			require.Equal(t, before, m.balances())
		})
	}
}

// 3. A LimitTransfer currency between a roleless buyer and seller.
func TestAssetControls_MarketBuyLimitTransferCurrency(t *testing.T) {
	for _, mode := range forkModes {
		t.Run(mode.name, func(t *testing.T) {
			m := newAssetControlsMarket(t, mode.v6, true)
			orderID := m.sellBuyItNow()
			before := m.balances()

			tx, err := m.buy(orderID, acPrice)

			if !mode.v6 {
				require.NoError(t, err)
				require.Equal(t, transaction.Transaction_Ok, tx.ResultCode)
				require.Equal(t, acBalances{before.sellerKLV, before.buyerKLV, acPrice, acFunds - acPrice, 0, 1}, m.balances())
				return
			}

			m.expectRefused(tx, err, transaction.Transaction_KDATransferNotAllowed, process.ErrKDATransferNotAllowed)
			// no leg was credited and the NFT is still in escrow
			require.Equal(t, before, m.balances())
		})
	}
}

// 4. A bid placed before the fork with a roleless LimitTransfer currency leg.
func TestAssetControls_AuctionBidBeforeForkClaimedAfter(t *testing.T) {
	setup := func(t *testing.T) (*assetControlsMarket, []byte, acBalances) {
		m := newAssetControlsMarket(t, false, true)
		orderID := m.sellAuction()

		tx, err := m.buy(orderID, acBid)
		require.NoError(t, err)
		require.Equal(t, transaction.Transaction_Ok, tx.ResultCode)

		bidded := m.balances()
		require.Equal(t, acBalances{bidded.sellerKLV, bidded.buyerKLV, 0, acFunds - acBid, 0, 0}, bidded)

		return m, orderID, bidded
	}

	t.Run("fork activates: claim unwinds", func(t *testing.T) {
		m, orderID, bidded := setup(t)
		m.setV6(true)

		tx, err := m.claim(laterBlock(m.Controller, 4), m.buyer, orderID)
		require.NoError(t, err)
		require.Equal(t, transaction.Transaction_Ok, tx.ResultCode)

		// NFT back to the seller, the bid back to the bidder, nobody paid
		require.Equal(t, acBalances{bidded.sellerKLV, bidded.buyerKLV, 0, acFunds, 1, 0}, m.balances())
	})

	t.Run("fork stays off: claim settles", func(t *testing.T) {
		m, orderID, bidded := setup(t)

		tx, err := m.claim(laterBlock(m.Controller, 4), m.buyer, orderID)
		require.NoError(t, err)
		require.Equal(t, transaction.Transaction_Ok, tx.ResultCode)

		require.Equal(t, acBalances{bidded.sellerKLV, bidded.buyerKLV, acBid, acFunds - acBid, 0, 1}, m.balances())
	})
}

// 5. A collection paused after the listing and the bid, with the fork activating afterwards.
func TestAssetControls_AuctionPausedAfterBidClaimedAfterFork(t *testing.T) {
	setup := func(t *testing.T) (*assetControlsMarket, []byte, acBalances) {
		m := newAssetControlsMarket(t, false, false)
		orderID := m.sellAuction()

		tx, err := m.buy(orderID, acBid)
		require.NoError(t, err)
		require.Equal(t, transaction.Transaction_Ok, tx.ResultCode)

		m.setAssetPaused(assetWithRoyaltiesIdentifier, true)

		return m, orderID, m.balances()
	}

	t.Run("fork activates: claim unwinds", func(t *testing.T) {
		m, orderID, bidded := setup(t)
		m.setV6(true)

		tx, err := m.claim(laterBlock(m.Controller, 4), m.buyer, orderID)
		require.NoError(t, err)
		require.Equal(t, transaction.Transaction_Ok, tx.ResultCode)

		// the pause does not hold the bidder's currency: NFT back to the seller, the bid back
		// to the bidder, nobody paid
		require.Equal(t, acBalances{bidded.sellerKLV, bidded.buyerKLV, 0, acFunds, 1, 0}, m.balances())
	})

	t.Run("fork activates: cancel before the end still works", func(t *testing.T) {
		m, orderID, bidded := setup(t)
		m.setV6(true)

		m.mustRunAC(nowBlock(m.Controller, 4), m.seller, transaction.TXContract_CancelMarketOrderContractType,
			&transaction.CancelMarketOrderContract{OrderID: orderID})

		// NFT back to the seller, the bid back to the bidder
		require.Equal(t, acBalances{bidded.sellerKLV, bidded.buyerKLV, 0, acFunds, 1, 0}, m.balances())
	})

	t.Run("fork stays off: claim settles", func(t *testing.T) {
		m, orderID, bidded := setup(t)

		tx, err := m.claim(laterBlock(m.Controller, 4), m.buyer, orderID)
		require.NoError(t, err)
		require.Equal(t, transaction.Transaction_Ok, tx.ResultCode)
		require.Equal(t, acBalances{bidded.sellerKLV, bidded.buyerKLV, acBid, acFunds - acBid, 0, 1}, m.balances())
	})
}

// 6. Minting a paused asset through the asset trigger, to a third party or to oneself.
func TestAssetControls_MintPausedAsset(t *testing.T) {
	mint := func(c *Controller, to []byte) (*transaction.Transaction, error) {
		return c.runAC(nowBlock(c, 2), sender, transaction.TXContract_AssetTriggerContractType, &transaction.AssetTriggerContract{
			TriggerType: transaction.AssetTriggerContract_Mint,
			AssetID:     assetITOIdentifier,
			ToAddress:   to,
			Amount:      acMint,
		})
	}

	setup := func(t *testing.T, v6 bool) *Controller {
		c := NewController(t)
		c.setV6(v6)
		c.CreateAssetForITO()
		c.AddUser(sender, acFunds, kdautils.KLVIdentifier)
		c.AddUser(receiver, acFunds, kdautils.KLVIdentifier)
		c.setAssetPaused(assetITOIdentifier, true)

		return c
	}

	for _, mode := range forkModes {
		t.Run(mode.name+"/third party", func(t *testing.T) {
			c := setup(t, mode.v6)
			supply := func() int64 {
				asset, err := c.GetAsset(assetITOIdentifier)
				require.NoError(t, err)
				return asset.CirculatingSupply
			}
			before := supply()

			tx, err := mint(c, receiver)

			if !mode.v6 {
				require.NoError(t, err)
				require.Equal(t, transaction.Transaction_Ok, tx.ResultCode)
				c.CheckBalance(receiver, assetITOIdentifier, acMint)
				require.Equal(t, before+acMint, supply())
				return
			}

			c.expectRefused(tx, err, transaction.Transaction_AssetPaused, process.ErrAssetIsPaused)
			c.CheckBalance(receiver, assetITOIdentifier, 0)
			c.CheckBalance(sender, kdautils.KLVIdentifier, acFunds)
			require.Equal(t, before, supply())
		})

		t.Run(mode.name+"/to self", func(t *testing.T) {
			c := setup(t, mode.v6)

			tx, err := mint(c, sender)

			if !mode.v6 {
				require.NoError(t, err)
				require.Equal(t, transaction.Transaction_Ok, tx.ResultCode)
				c.CheckBalance(sender, assetITOIdentifier, acMint)
				return
			}

			c.expectRefused(tx, err, transaction.Transaction_AssetPaused, process.ErrAssetIsPaused)
			c.CheckBalance(sender, assetITOIdentifier, 0)
		})
	}
}

// 7. An ITO buy of a paused sold asset.
func TestAssetControls_ITOBuyPausedAsset(t *testing.T) {
	const price = int64(1)

	for _, mode := range forkModes {
		t.Run(mode.name, func(t *testing.T) {
			c := NewController(t)
			c.setV6(mode.v6)
			c.CreateAssetForITO()
			c.AddUser(sender, acFunds, kdautils.KLVIdentifier)
			c.AddUser(receiver, acFunds, kdautils.KLVIdentifier)

			c.RunConfigITOTX(nowBlock(c, 1), sender, admin, assetITOIdentifier, 1000, 10)
			c.setAssetPaused(assetITOIdentifier, true)

			tx, err := c.runAC(laterBlock(c, 2), receiver, transaction.TXContract_BuyContractType, &transaction.BuyContract{
				BuyType:        transaction.BuyContract_ITOBuy,
				ID:             assetITOIdentifier,
				CurrencyID:     kdautils.KLVIdentifier,
				Amount:         1,
				CurrencyAmount: price,
			})

			if !mode.v6 {
				require.NoError(t, err)
				require.Equal(t, transaction.Transaction_Ok, tx.ResultCode)
				c.CheckBalance(receiver, assetITOIdentifier, 1)
				c.CheckBalance(receiver, kdautils.KLVIdentifier, acFunds-price)
				return
			}

			c.expectRefused(tx, err, transaction.Transaction_AssetPaused, process.ErrAssetIsPaused)
			// the buyer pays only the fee, which the harness sets to zero
			c.CheckBalance(receiver, assetITOIdentifier, 0)
			c.CheckBalance(receiver, kdautils.KLVIdentifier, acFunds)
		})
	}
}
