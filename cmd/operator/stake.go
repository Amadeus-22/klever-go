package main

import (
	"math"

	"github.com/klever-io/klever-go/core/process/kda/kdautils"
	"github.com/klever-io/klever-go/data/transaction"
	"github.com/klever-io/klever-go/network/api/models"
)

func createValidator(fromAddr, ownerAddr string, f *validatorFlags) error {
	parsedCommission := f.commission * math.Pow10(2)
	parsedMaxDelegation := f.maxDelegation * math.Pow10(6)

	data, err := buildRequest(transaction.TXContract_CreateValidatorContractType, fromAddr, []interface{}{models.CreateValidatorTXRequest{
		BLSPublicKey:        f.blsKey,
		BLSSignature:        f.blsProof,
		OwnerAddress:        ownerAddr,
		RewardAddress:       f.rewardsAddress,
		Commission:          uint32(parsedCommission),
		CanDelegate:         f.canDelegate,
		MaxDelegationAmount: int64(parsedMaxDelegation),
		Logo:                f.logo,
		Name:                f.validatorName,
		URIs:                f.uris,
	}})
	if err != nil {
		return err
	}

	log.Info("creating validator", "data", string(data))
	_, err = sendSignAndBroadcast(data)
	return err
}

func validatorConfig(fromAddr string, f *validatorFlags) error {
	parsedCommission := f.commission * math.Pow10(2)
	parsedMaxDelegation := f.maxDelegation * math.Pow10(6)

	data, err := buildRequest(transaction.TXContract_ValidatorConfigContractType, fromAddr, []interface{}{models.ValidatorConfigTXRequest{
		BLSPublicKey:        f.blsKey,
		BLSSignature:        f.blsProof,
		RewardAddress:       f.rewardsAddress,
		CanDelegate:         f.canDelegate,
		Commission:          uint32(parsedCommission),
		MaxDelegationAmount: int64(parsedMaxDelegation),
		Logo:                f.logo,
		Name:                f.validatorName,
		URIs:                f.uris,
	}})

	if err != nil {
		return err
	}

	log.Info("configuring validator", "data", string(data))
	_, err = sendSignAndBroadcast(data)
	return err
}

func freeze(fromAddr, kda string, amount float64) error {
	precision := uint32(6)
	if len(kda) > 0 && kda != string(kdautils.KLVIdentifier) && kda != string(kdautils.KFIIdentifier) {
		kda, err := getAssetData(kda)
		if err != nil {
			return err
		}

		precision = kda.Precision
	}

	parsedAmount := amount * math.Pow10(int(precision))

	data, err := buildRequest(transaction.TXContract_FreezeContractType, fromAddr, []interface{}{models.FreezeTXRequest{
		Amount: int64(parsedAmount),
		KDA:    kda,
	}})
	if err != nil {
		return err
	}

	log.Info("requesting freeze", "data", string(data))
	_, err = sendSignAndBroadcast(data)
	return err
}

func unfreeze(fromAddr, bucketId, kda string) error {
	data, err := buildRequest(transaction.TXContract_UnfreezeContractType, fromAddr, []interface{}{models.UnfreezeTXRequest{
		BucketID: bucketId,
		KDA:      kda,
	}})
	if err != nil {
		return err
	}

	log.Info("requesting unfreeze", "data", string(data))
	_, err = sendSignAndBroadcast(data)
	return err
}

func delegate(fromAddr, toAddr, bucketId string) error {
	data, err := buildRequest(transaction.TXContract_DelegateContractType, fromAddr, []interface{}{models.DelegateTXRequest{
		Receiver: toAddr,
		BucketID: bucketId,
	}})
	if err != nil {
		return err
	}

	log.Info("requesting delegation", "data", string(data))
	_, err = sendSignAndBroadcast(data)
	return err
}

func undelegate(fromAddr, bucketId string) error {
	data, err := buildRequest(transaction.TXContract_UndelegateContractType, fromAddr, []interface{}{models.UndelegateTXRequest{
		BucketID: bucketId,
	}})
	if err != nil {
		return err
	}

	log.Info("requesting undelegation", "data", string(data))
	_, err = sendSignAndBroadcast(data)
	return err
}

func withdraw(fromAddr, kda string) error {
	data, err := buildRequest(transaction.TXContract_WithdrawContractType, fromAddr, []interface{}{models.WithdrawTXRequest{
		KDA: kda,
	}})
	if err != nil {
		return err
	}

	log.Info("requesting withdraw", "data", string(data))
	_, err = sendSignAndBroadcast(data)
	return err
}

func claim(fromAddr, id string, claimType int32) error {
	data, err := buildRequest(transaction.TXContract_ClaimContractType, fromAddr, []interface{}{models.ClaimTXRequest{
		ClaimType: claimType,
		ID:        id,
	}})
	if err != nil {
		return err
	}

	log.Info("requesting claim", "data", string(data))
	_, err = sendSignAndBroadcast(data)
	return err
}

func unjail(fromAddr string) error {
	data, err := buildRequest(transaction.TXContract_UnjailContractType, fromAddr, []interface{}{models.UnjailTXRequest{}})
	if err != nil {
		return err
	}

	log.Info("requesting unjail", "data", string(data))
	_, err = sendSignAndBroadcast(data)
	return err
}
