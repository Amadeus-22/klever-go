package kda_test

import (
	"errors"
	"testing"

	"github.com/klever-io/klever-go/common"
	"github.com/klever-io/klever-go/core/kapp"
	"github.com/klever-io/klever-go/core/process"
	txProcess "github.com/klever-io/klever-go/core/process/transaction"
	"github.com/klever-io/klever-go/data/transaction"
	"github.com/klever-io/klever-go/kapps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// KLC-2739: a paused asset cannot be minted, not even to the minter

var (
	pausedMintRecipient = makeAddress("pausedMintRecipient")
	pausedMintMinter    = makeAddress("pausedMintMinter")
	pausedMintDepositor = makeAddress("pausedMintDepositor")
)

// addPausedMintRole gives address a role on the default asset, through the owner's AddRole trigger.
func addPausedMintRole(t *testing.T, kc kapp.KAppController, role *transaction.RolesInfo) {
	t.Helper()

	_, err := kc.GetKDAKApp().Trigger(defaultSender, &transaction.AssetTriggerContract{
		TriggerType: transaction.AssetTriggerContract_AddRole,
		AssetID:     defaultAssetID,
		Role:        role,
	}, nil)
	require.NoError(t, err)
}

// pausedMintHoldings is what a mint of the default asset can change: the recipient's holding
// (the fungible balance, or the first minted NFT/SFT nonce) and the asset's supply counters.
type pausedMintHoldings struct {
	balance           int64
	mintedValue       int64
	circulatingSupply int64
}

func readPausedMintHoldings(t *testing.T, kc kapp.KAppController, assetType transaction.CreateAssetContract_EnumAssetType, recipient []byte) pausedMintHoldings {
	t.Helper()

	_, asset, err := kc.GetKDAKApp().GetKDA(defaultAssetID)
	require.NoError(t, err)

	acc, err := kc.GetKDAKApp().GetAccountsCacher().LoadUser(recipient)
	require.NoError(t, err)

	var nonce []byte
	if assetType != transaction.CreateAssetContract_Fungible {
		nonce = []byte("1")
	}

	return pausedMintHoldings{
		balance:           acc.GetBalanceWithNonce(defaultAssetID, nonce, true),
		mintedValue:       asset.MintedValue,
		circulatingSupply: asset.CirculatingSupply,
	}
}

func TestKDAKapp_MintOfAPausedAsset(t *testing.T) {
	t.Parallel()

	assetTypes := []struct {
		name      string
		assetType transaction.CreateAssetContract_EnumAssetType
	}{
		{"fungible", transaction.CreateAssetContract_Fungible},
		{"non-fungible", transaction.CreateAssetContract_NonFungible},
		{"semi-fungible", transaction.CreateAssetContract_SemiFungible},
	}

	tests := []struct {
		name         string
		paused       bool
		sender       []byte
		recipient    []byte
		expectedCode transaction.Transaction_TXResultCode
		expectedErr  error
		// expectedField is the error receipt the refusal writes; empty when none is expected
		expectedField string
	}{
		{
			name: "the owner minting to a third party is refused", paused: true,
			sender: defaultSender, recipient: pausedMintRecipient,
			expectedCode: transaction.Transaction_AssetPaused, expectedErr: process.ErrAssetIsPaused,
			expectedField: common.ErrFieldAssetPaused,
		},
		{
			name: "a mint role holder minting to a third party is refused", paused: true,
			sender: pausedMintMinter, recipient: pausedMintRecipient,
			expectedCode: transaction.Transaction_AssetPaused, expectedErr: process.ErrAssetIsPaused,
			expectedField: common.ErrFieldAssetPaused,
		},
		{
			name: "the ITO KApp minting to a buyer is refused", paused: true,
			sender: kapps.ITOKAppAddress, recipient: pausedMintRecipient,
			expectedCode: transaction.Transaction_AssetPaused, expectedErr: process.ErrAssetIsPaused,
			expectedField: common.ErrFieldAssetPaused,
		},
		{
			name: "the owner minting to itself is refused", paused: true,
			sender: defaultSender, recipient: defaultSender,
			expectedCode: transaction.Transaction_AssetPaused, expectedErr: process.ErrAssetIsPaused,
			expectedField: common.ErrFieldAssetPaused,
		},
		{
			name: "a mint role holder minting to itself is refused", paused: true,
			sender: pausedMintMinter, recipient: pausedMintMinter,
			expectedCode: transaction.Transaction_AssetPaused, expectedErr: process.ErrAssetIsPaused,
			expectedField: common.ErrFieldAssetPaused,
		},
		{
			name:   "an asset that is not paused is minted to a third party",
			sender: defaultSender, recipient: pausedMintRecipient,
			expectedCode: transaction.Transaction_Ok,
		},
		{
			name: "a sender with no role keeps its role error", paused: true,
			sender: defaultOtherSender, recipient: pausedMintRecipient,
			expectedCode: transaction.Transaction_AssetError, expectedErr: common.ErrRoleNotFound,
			expectedField: common.ErrFieldInvalidRoleAddress,
		},
		{
			name: "a sender with a role but no mint role keeps its permission error", paused: true,
			sender: pausedMintDepositor, recipient: pausedMintRecipient,
			expectedCode: transaction.Transaction_AccountNotOwner, expectedErr: common.ErrAccNotOwner,
			expectedField: common.ErrFieldInvalidPermission,
		},
	}

	for _, v6 := range []bool{false, true} {
		for _, at := range assetTypes {
			for _, tt := range tests {
				name := "pre-fork/"
				if v6 {
					name = "post-fork/"
				}

				t.Run(name+at.name+"/"+tt.name, func(t *testing.T) {
					t.Parallel()

					checkPausedMint(t, v6, at.assetType, tt.sender, tt.recipient, tt.paused, tt.expectedCode, tt.expectedErr, tt.expectedField)
				})
			}
		}
	}
}

func checkPausedMint(t *testing.T, v6 bool, assetType transaction.CreateAssetContract_EnumAssetType, sender, recipient []byte, paused bool, expectedCode transaction.Transaction_TXResultCode, expectedErr error, expectedField string) {
	t.Helper()

	// pre-fork: legacy behaviour, kept for replay. A paused asset is minted to anyone.
	if !v6 && errors.Is(expectedErr, process.ErrAssetIsPaused) {
		expectedCode, expectedErr, expectedField = transaction.Transaction_Ok, nil, ""
	}

	kc, err := createMockControllers()
	require.NoError(t, err)
	setFixAuditChangesV6(t, kc, v6)
	createDefaultAsset(t, kc, assetType, 0, 0, 0, nil)
	addPausedMintRole(t, kc, &transaction.RolesInfo{Address: pausedMintMinter, HasRoleMint: true})
	addPausedMintRole(t, kc, &transaction.RolesInfo{Address: kapps.ITOKAppAddress, HasRoleMint: true})
	addPausedMintRole(t, kc, &transaction.RolesInfo{Address: pausedMintDepositor, HasRoleDeposit: true})
	setAssetPaused(t, kc, defaultAssetID, paused)

	before := readPausedMintHoldings(t, kc, assetType, recipient)
	errorsBefore := len(kc.GetCurrentKAppContext().Receipts().GetByType(int8(txProcess.Error)))

	code, err := kc.GetKDAKApp().Mint(sender, &transaction.AssetTriggerContract{
		AssetID:   defaultAssetID,
		Amount:    1,
		ToAddress: recipient,
	})

	assert.Equal(t, expectedCode, code)
	after := readPausedMintHoldings(t, kc, assetType, recipient)
	errorReceipts := kc.GetCurrentKAppContext().Receipts().GetByType(int8(txProcess.Error))[errorsBefore:]

	if expectedErr == nil {
		require.NoError(t, err)
		assert.Equal(t, before.balance+1, after.balance)
		assert.Equal(t, before.mintedValue+1, after.mintedValue)
		assert.Equal(t, before.circulatingSupply+1, after.circulatingSupply)
		assert.Empty(t, errorReceipts)
		return
	}

	require.ErrorIs(t, err, expectedErr)
	assert.Equal(t, before, after, "a refused mint credits nothing and leaves the supply unchanged")
	require.Len(t, errorReceipts, 1)
	assert.Equal(t, expectedField, string(errorReceipts[0].Data[1]))
}

// A mint naming an existing semi-fungible nonce adds quantity to it instead of creating one, and
// is held by the same check.
func TestKDAKapp_MintOfAPausedSemiFungibleNonce(t *testing.T) {
	t.Parallel()

	existingNonce := func(t *testing.T, v6 bool) kapp.KAppController {
		t.Helper()

		kc, err := createMockControllers()
		require.NoError(t, err)
		setFixAuditChangesV6(t, kc, v6)
		createDefaultAsset(t, kc, transaction.CreateAssetContract_SemiFungible, 0, 0, 0, nil)

		code, err := kc.GetKDAKApp().Mint(defaultSender, &transaction.AssetTriggerContract{
			AssetID:   defaultAssetID,
			Amount:    1,
			ToAddress: defaultSender,
		})
		require.NoError(t, err)
		require.Equal(t, transaction.Transaction_Ok, code)

		setAssetPaused(t, kc, defaultAssetID, true)

		return kc
	}

	addQuantity := func(kc kapp.KAppController, recipient []byte) (transaction.Transaction_TXResultCode, error) {
		return kc.GetKDAKApp().Mint(defaultSender, &transaction.AssetTriggerContract{
			AssetID:   append(append([]byte{}, defaultAssetID...), []byte(kapps.Sp+"1")...),
			Amount:    1,
			ToAddress: recipient,
		})
	}

	t.Run("adding quantity for a third party is refused", func(t *testing.T) {
		t.Parallel()

		kc := existingNonce(t, true)
		ownerBefore := readPausedMintHoldings(t, kc, transaction.CreateAssetContract_SemiFungible, defaultSender)
		recipientBefore := readPausedMintHoldings(t, kc, transaction.CreateAssetContract_SemiFungible, pausedMintRecipient)
		errorsBefore := len(kc.GetCurrentKAppContext().Receipts().GetByType(int8(txProcess.Error)))

		code, err := addQuantity(kc, pausedMintRecipient)

		require.ErrorIs(t, err, process.ErrAssetIsPaused)
		assert.Equal(t, transaction.Transaction_AssetPaused, code)
		assert.Equal(t, ownerBefore, readPausedMintHoldings(t, kc, transaction.CreateAssetContract_SemiFungible, defaultSender))
		assert.Equal(t, recipientBefore, readPausedMintHoldings(t, kc, transaction.CreateAssetContract_SemiFungible, pausedMintRecipient))
		errorReceipts := kc.GetCurrentKAppContext().Receipts().GetByType(int8(txProcess.Error))[errorsBefore:]
		require.Len(t, errorReceipts, 1)
		assert.Equal(t, common.ErrFieldAssetPaused, string(errorReceipts[0].Data[1]))
	})

	t.Run("before the fork adding quantity for a third party is accepted", func(t *testing.T) {
		t.Parallel()

		kc := existingNonce(t, false)
		recipientBefore := readPausedMintHoldings(t, kc, transaction.CreateAssetContract_SemiFungible, pausedMintRecipient)

		// pre-fork: legacy behaviour, kept for replay
		code, err := addQuantity(kc, pausedMintRecipient)

		require.NoError(t, err)
		assert.Equal(t, transaction.Transaction_Ok, code)
		recipientAfter := readPausedMintHoldings(t, kc, transaction.CreateAssetContract_SemiFungible, pausedMintRecipient)
		assert.Equal(t, recipientBefore.balance+1, recipientAfter.balance)
		assert.Empty(t, kc.GetCurrentKAppContext().Receipts().GetByType(int8(txProcess.Error)))
	})

	t.Run("adding quantity for the owner itself is refused", func(t *testing.T) {
		t.Parallel()

		kc := existingNonce(t, true)
		before := readPausedMintHoldings(t, kc, transaction.CreateAssetContract_SemiFungible, defaultSender)

		code, err := addQuantity(kc, defaultSender)

		require.ErrorIs(t, err, process.ErrAssetIsPaused)
		assert.Equal(t, transaction.Transaction_AssetPaused, code)
		assert.Equal(t, before, readPausedMintHoldings(t, kc, transaction.CreateAssetContract_SemiFungible, defaultSender))
	})

	t.Run("before the fork adding quantity for the owner itself is accepted", func(t *testing.T) {
		t.Parallel()

		kc := existingNonce(t, false)
		before := readPausedMintHoldings(t, kc, transaction.CreateAssetContract_SemiFungible, defaultSender)

		// pre-fork: legacy behaviour, kept for replay
		code, err := addQuantity(kc, defaultSender)

		require.NoError(t, err)
		assert.Equal(t, transaction.Transaction_Ok, code)
		assert.Equal(t, before.balance+1, readPausedMintHoldings(t, kc, transaction.CreateAssetContract_SemiFungible, defaultSender).balance)
	})
}

func TestKDAKapp_MintTriggerOfAPausedAsset(t *testing.T) {
	t.Parallel()

	mintTrigger := func(kc kapp.KAppController, recipient []byte) (transaction.Transaction_TXResultCode, error) {
		return kc.GetKDAKApp().Trigger(defaultSender, &transaction.AssetTriggerContract{
			TriggerType: transaction.AssetTriggerContract_Mint,
			AssetID:     defaultAssetID,
			Amount:      100,
			ToAddress:   recipient,
		}, nil)
	}

	pausedAsset := func(t *testing.T, v6 bool) kapp.KAppController {
		t.Helper()

		kc, err := createMockControllers()
		require.NoError(t, err)
		setFixAuditChangesV6(t, kc, v6)
		createDefaultAsset(t, kc, transaction.CreateAssetContract_Fungible, 0, 0, 0, nil)

		_, err = kc.GetKDAKApp().Trigger(defaultSender, &transaction.AssetTriggerContract{
			TriggerType: transaction.AssetTriggerContract_Pause,
			AssetID:     defaultAssetID,
		}, nil)
		require.NoError(t, err)

		return kc
	}

	t.Run("a mint trigger to a third party is refused", func(t *testing.T) {
		t.Parallel()

		kc := pausedAsset(t, true)
		before := readPausedMintHoldings(t, kc, transaction.CreateAssetContract_Fungible, pausedMintRecipient)

		code, err := mintTrigger(kc, pausedMintRecipient)

		require.ErrorIs(t, err, process.ErrAssetIsPaused)
		assert.Equal(t, transaction.Transaction_AssetPaused, code)
		assert.Equal(t, before, readPausedMintHoldings(t, kc, transaction.CreateAssetContract_Fungible, pausedMintRecipient))
		errorReceipts := kc.GetCurrentKAppContext().Receipts().GetByType(int8(txProcess.Error))
		require.Len(t, errorReceipts, 1)
		assert.Equal(t, common.ErrFieldAssetPaused, string(errorReceipts[0].Data[1]))
	})

	t.Run("a mint trigger to the owner itself is refused", func(t *testing.T) {
		t.Parallel()

		kc := pausedAsset(t, true)
		code, err := mintTrigger(kc, defaultSender)

		require.ErrorIs(t, err, process.ErrAssetIsPaused)
		assert.Equal(t, transaction.Transaction_AssetPaused, code)
		assert.Zero(t, senderBalance(t, kc, defaultAssetID))
	})

	t.Run("before the fork a mint trigger to the owner itself is accepted", func(t *testing.T) {
		t.Parallel()

		kc := pausedAsset(t, false)
		code, err := mintTrigger(kc, defaultSender)

		require.NoError(t, err)
		assert.Equal(t, transaction.Transaction_Ok, code)
		assert.Equal(t, int64(100), senderBalance(t, kc, defaultAssetID))
	})

	t.Run("before the fork a mint trigger to a third party is accepted", func(t *testing.T) {
		t.Parallel()

		kc := pausedAsset(t, false)
		code, err := mintTrigger(kc, pausedMintRecipient)

		require.NoError(t, err)
		assert.Equal(t, transaction.Transaction_Ok, code)
		assert.Equal(t, int64(100), readPausedMintHoldings(t, kc, transaction.CreateAssetContract_Fungible, pausedMintRecipient).balance)
	})
}
