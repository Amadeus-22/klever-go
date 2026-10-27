package hostCoretest

import (
	"bytes"
	"fmt"
	"math/big"
	"testing"

	"github.com/klever-io/klever-go/common"
	blockchainConfig "github.com/klever-io/klever-go/config"
	"github.com/klever-io/klever-go/kvm/executor"
	mock "github.com/klever-io/klever-go/kvm/mock/context"
	"github.com/klever-io/klever-go/kvm/mock/contracts"
	worldmock "github.com/klever-io/klever-go/kvm/mock/world"
	test "github.com/klever-io/klever-go/kvm/testcommon"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/klever-io/klever-go/vmcommon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var smallKey = []byte("testKey")
var bigKey = make([]byte, 50)

const storageLoadGas = uint64(10)
const cachedStorageLoadGas = uint64(5)
const dataCopyGas = uint64(1)

func TestGasUsed_LoadStorage_SmallKey_FlagEnabled(t *testing.T) {
	loadStorage(t, smallKey)
}

func TestGasUsed_LoadStorage_BigKey_FlagEnabled(t *testing.T) {
	loadStorage(t, bigKey)
}

func loadStorage(t *testing.T, key []byte) {
	testConfig := makeTestConfig()
	value := []byte("testValue")

	expectedUsedGas := computeExpectedGasForGetStorage(key, value)

	_, err := test.BuildMockInstanceCallTest(t).
		WithContracts(
			test.CreateMockContract(test.ParentAddress).
				WithBalance(0).
				WithConfig(nil).
				WithMethods(contracts.LoadStore)).
		WithInput(test.CreateTestContractCallInputBuilder().
			WithRecipientAddr(test.ParentAddress).
			WithGasProvided(testConfig.GasProvided).
			WithFunction("loadStore").
			WithArguments(key).
			Build()).
		WithSetup(func(host vmhost.VMHost, world *worldmock.MockWorld) {
			setZeroCodeCosts(host)
			host.Metering().GasSchedule().BaseOpsAPICost.StorageLoad = storageLoadGas
			host.Metering().GasSchedule().BaseOpsAPICost.CachedStorageLoad = cachedStorageLoadGas
			host.Metering().GasSchedule().BaseOperationCost.DataCopyPerByte = dataCopyGas
			host.Metering().GasSchedule().BaseOperationCost.PersistPerByte = 0
			host.Metering().GasSchedule().DynamicStorageLoad.Constant = 10

			accountHandler, _ := world.GetUserAccount(test.ParentAddress)
			_ = accountHandler.DataTrieTracker().SaveKeyValue(key, value)
			_ = world.AccountsCacher.SaveAll()
		}).
		AndAssertResults(func(world *worldmock.MockWorld, verify *test.VMOutputVerifier) {
			verify.
				Ok().
				GasRemaining(testConfig.GasProvided-expectedUsedGas).
				GasUsed(test.ParentAddress, expectedUsedGas).
				ReturnData(value)
		})
	assert.Nil(t, err)
}

func TestGasUsed_LoadStorageFromAddress_SmallKey_FlagEnabled(t *testing.T) {
	loadStorageFromAddress(t, smallKey)
}

func TestGasUsed_LoadStorageFromAddress_BigKey_FlagEnabled(t *testing.T) {
	loadStorageFromAddress(t, bigKey)
}

func loadStorageFromAddress(t *testing.T, key []byte) {
	testConfig := makeTestConfig()
	value := []byte("testValue")

	expectedUsedGas := computeExpectedGasForGetStorage(key, value)

	_, err := test.BuildMockInstanceCallTest(t).
		WithContracts(
			test.CreateMockContract(test.UserAddress).
				WithBalance(0).
				WithConfig(nil).
				WithMethods(),
			test.CreateMockContract(test.ParentAddress).
				WithBalance(0).
				WithConfig(nil).
				WithMethods(contracts.LoadStoreFromAddress)).
		WithInput(test.CreateTestContractCallInputBuilder().
			WithRecipientAddr(test.ParentAddress).
			WithGasProvided(testConfig.GasProvided).
			WithFunction("loadStoreFromAddress").
			WithArguments(test.UserAddress, key).
			Build()).
		WithSetup(func(host vmhost.VMHost, world *worldmock.MockWorld) {
			setZeroCodeCosts(host)
			host.Metering().GasSchedule().BaseOpsAPICost.StorageLoad = storageLoadGas
			host.Metering().GasSchedule().BaseOpsAPICost.CachedStorageLoad = cachedStorageLoadGas
			host.Metering().GasSchedule().BaseOperationCost.DataCopyPerByte = dataCopyGas
			host.Metering().GasSchedule().BaseOperationCost.PersistPerByte = 0
			host.Metering().GasSchedule().DynamicStorageLoad.Constant = 10

			account, errAccount := world.GetAccount(test.UserAddress)
			require.Nil(t, errAccount)
			account.Storage[string(key)] = value
			account.CodeMetadata = []byte{vmcommon.MetadataReadable, 0}
			world.PutAccount(account)
		}).
		AndAssertResults(func(world *worldmock.MockWorld, verify *test.VMOutputVerifier) {
			verify.
				Ok().
				GasRemaining(testConfig.GasProvided-expectedUsedGas).
				GasUsed(test.ParentAddress, expectedUsedGas).
				ReturnData(value)
		})
	assert.Nil(t, err)
}

func computeExpectedGasForGetStorage(key []byte, value []byte) uint64 {
	extraBytesForKey := len(key) - vmhost.AddressLen
	if extraBytesForKey < 0 {
		extraBytesForKey = 0
	}

	expectedUsedGas := storageLoadGas + uint64(len(value))*dataCopyGas + cachedStorageLoadGas + uint64(extraBytesForKey)*dataCopyGas
	return expectedUsedGas
}

const (
	rereadKeyPtr     = executor.MemPtr(0)
	rereadAddressPtr = executor.MemPtr(32)
	rereadDataPtr    = executor.MemPtr(64)
)

var storageRereadHooks = map[string]func(instance *mock.InstanceMock, key []byte, iteration int){
	"storageLoad": func(instance *mock.InstanceMock, key []byte, _ int) {
		vmhooks.NewVMHooksImpl(instance.Host).StorageLoad(rereadKeyPtr, executor.MemLength(len(key)), rereadDataPtr)
	},
	"storageLoadFromAddress": func(instance *mock.InstanceMock, key []byte, _ int) {
		vmhooks.NewVMHooksImpl(instance.Host).StorageLoadFromAddress(rereadAddressPtr, rereadKeyPtr, executor.MemLength(len(key)), rereadDataPtr)
	},
	"bigIntStorageLoadUnsigned": func(instance *mock.InstanceMock, key []byte, iteration int) {
		vmhooks.NewVMHooksImpl(instance.Host).BigIntStorageLoadUnsigned(rereadKeyPtr, executor.MemLength(len(key)), int32(100+iteration)) // #nosec G115
	},
	"smallIntStorageLoadUnsigned": func(instance *mock.InstanceMock, key []byte, _ int) {
		vmhooks.NewVMHooksImpl(instance.Host).SmallIntStorageLoadUnsigned(rereadKeyPtr, executor.MemLength(len(key)))
	},
	"smallIntStorageLoadSigned": func(instance *mock.InstanceMock, key []byte, _ int) {
		vmhooks.NewVMHooksImpl(instance.Host).SmallIntStorageLoadSigned(rereadKeyPtr, executor.MemLength(len(key)))
	},
}

func TestGasUsed_CachedRereads_ChargedPerByteFromFixAuditChangesV6(t *testing.T) {
	const numberOfReads = 10
	key := []byte("cachedRereadKey.................")
	value := make([]byte, 20_000)
	value[len(value)-1] = 7

	for hookName, load := range storageRereadHooks {
		t.Run(hookName, func(t *testing.T) {
			forEachFixAuditChangesV6(t, uint64(len(key)+len(value))*dataCopyGas, func(t *testing.T, epochs blockchainConfig.EnableEpochs, copyGas uint64) {
				readGas := make([]uint64, 0, numberOfReads)

				_, err := test.BuildMockInstanceCallTest(t).
					WithEnableEpochs(epochs).
					WithContracts(
						test.CreateMockContract(test.ParentAddress).
							WithMethods(func(parentInstance *mock.InstanceMock, _ any) {
								parentInstance.AddMockMethod("rereadStorage", func() *mock.InstanceMock {
									_ = parentInstance.MemStore(rereadKeyPtr, key)
									_ = parentInstance.MemStore(rereadAddressPtr, test.ParentAddress)
									metering := parentInstance.Host.Metering()
									for i := 0; i < numberOfReads; i++ {
										gasLeft := metering.GasLeft()
										load(parentInstance, key, i)
										readGas = append(readGas, gasLeft-metering.GasLeft())
									}
									return parentInstance
								})
							})).
					WithInput(test.CreateTestContractCallInputBuilder().
						WithRecipientAddr(test.ParentAddress).
						WithGasProvided(10_000_000).
						WithFunction("rereadStorage").
						Build()).
					WithSetup(func(host vmhost.VMHost, world *worldmock.MockWorld) {
						setZeroCodeCosts(host)
						host.Metering().GasSchedule().BaseOpsAPICost.CachedStorageLoad = cachedStorageLoadGas
						host.Metering().GasSchedule().BaseOperationCost.DataCopyPerByte = dataCopyGas

						accountHandler, _ := world.GetUserAccount(test.ParentAddress)
						_ = accountHandler.DataTrieTracker().SaveKeyValue(key, value)
						_ = world.AccountsCacher.SaveAll()
					}).
					AndAssertResults(func(_ *worldmock.MockWorld, verify *test.VMOutputVerifier) {
						verify.Ok()
						require.Len(t, readGas, numberOfReads)
						for _, rereadGas := range readGas[1:] {
							require.Equal(t, cachedStorageLoadGas+copyGas, rereadGas)
						}
					})
				require.NoError(t, err)
			})
		})
	}
}

func TestGasUsed_SetStorage_FlagEnabled(t *testing.T) {
	setStorage(t, smallKey, 29)
}

func TestGasUsed_SetStorage_BigKey_FlagEnabled(t *testing.T) {
	setStorage(t, bigKey, 65)
}

func setStorage(t *testing.T, key []byte, expectedUsedGas uint64) {
	testConfig := makeTestConfig()
	value := []byte("testValue")

	storageStoreGas := uint64(10)
	dataCopyGas := uint64(1)

	_, err := test.BuildMockInstanceCallTest(t).
		WithContracts(
			test.CreateMockContract(test.UserAddress).
				WithBalance(0).
				WithConfig(nil).
				WithMethods(),
			test.CreateMockContract(test.ParentAddress).
				WithBalance(0).
				WithConfig(nil).
				WithMethods(contracts.SetStore)).
		WithInput(test.CreateTestContractCallInputBuilder().
			WithRecipientAddr(test.ParentAddress).
			WithGasProvided(testConfig.GasProvided).
			WithFunction("setStore").
			WithArguments(key, value).
			Build()).
		WithSetup(func(host vmhost.VMHost, world *worldmock.MockWorld) {
			setZeroCodeCosts(host)
			host.Metering().GasSchedule().BaseOpsAPICost.StorageStore = storageStoreGas
			host.Metering().GasSchedule().BaseOperationCost.DataCopyPerByte = dataCopyGas
			host.Metering().GasSchedule().BaseOperationCost.PersistPerByte = 0

			account, errAccount := world.GetAccount(test.UserAddress)
			require.Nil(t, errAccount)
			account.Storage[string(key)] = value
			account.CodeMetadata = []byte{vmcommon.MetadataReadable, 0}
			world.PutAccount(account)
		}).
		AndAssertResults(func(world *worldmock.MockWorld, verify *test.VMOutputVerifier) {
			verify.
				Ok().
				GasUsed(test.ParentAddress, expectedUsedGas).
				GasRemaining(testConfig.GasProvided - expectedUsedGas)
		})
	assert.Nil(t, err)
}

var expectedAddByParent = len(contracts.TestStorageValue1) +
	len(contracts.TestStorageValue2)

var expectedDeletedByParent = (len(contracts.TestStorageValue1) - len(contracts.TestStorageValue2)) +
	(len(contracts.TestStorageValue2) - len(contracts.TestStorageValue3))

var expectedAddByChild = len(contracts.TestStorageValue2) +
	(len(contracts.TestStorageValue1) - len(contracts.TestStorageValue2)) +
	len(contracts.TestStorageValue1)

var expectedDeletedByChild = len(contracts.TestStorageValue1) - len(contracts.TestStorageValue4)

func TestBytesCount_SetStorage_ExecuteOnSameCtx(t *testing.T) {
	simpleGasTestConfig := makeTestConfig()
	_, err := test.BuildMockInstanceCallTest(t).
		WithContracts(
			test.CreateMockContract(test.ParentAddress).
				WithBalance(simpleGasTestConfig.ParentBalance).
				WithConfig(simpleGasTestConfig).
				WithMethods(contracts.ParentSetStorageMock),
			test.CreateMockContract(test.ChildAddress).
				WithBalance(simpleGasTestConfig.ChildBalance).
				WithConfig(simpleGasTestConfig).
				WithMethods(contracts.ChildSetStorageMock),
		).
		WithInput(test.CreateTestContractCallInputBuilder().
			WithRecipientAddr(test.ParentAddress).
			WithGasProvided(simpleGasTestConfig.GasProvided).
			WithFunction("parentSetStorage").
			WithArguments([]byte{0}).
			Build()).
		WithSetup(func(host vmhost.VMHost, world *worldmock.MockWorld) {
			setZeroCodeCosts(host)
		}).
		AndAssertResults(func(world *worldmock.MockWorld, verify *test.VMOutputVerifier) {
			verify.
				Ok().
				BytesAddedToStorage(test.ParentAddress,
					expectedAddByParent+expectedAddByChild).
				BytesAddedToStorage(test.ChildAddress,
					0).
				BytesDeletedFromStorage(test.ParentAddress,
					expectedDeletedByParent+expectedDeletedByChild).
				BytesDeletedFromStorage(test.ChildAddress,
					0)
		})
	assert.Nil(t, err)
}

func TestBytesCount_SetStorage_ExecuteOnDestCtx(t *testing.T) {
	simpleGasTestConfig := makeTestConfig()
	_, err := test.BuildMockInstanceCallTest(t).
		WithContracts(
			test.CreateMockContract(test.ParentAddress).
				WithBalance(simpleGasTestConfig.ParentBalance).
				WithConfig(simpleGasTestConfig).
				WithMethods(contracts.ParentSetStorageMock),
			test.CreateMockContract(test.ChildAddress).
				WithBalance(simpleGasTestConfig.ChildBalance).
				WithConfig(simpleGasTestConfig).
				WithMethods(contracts.ChildSetStorageMock),
		).
		WithInput(test.CreateTestContractCallInputBuilder().
			WithRecipientAddr(test.ParentAddress).
			WithGasProvided(simpleGasTestConfig.GasProvided).
			WithFunction("parentSetStorage").
			WithArguments([]byte{1}).
			Build()).
		WithSetup(func(host vmhost.VMHost, world *worldmock.MockWorld) {
			setZeroCodeCosts(host)
		}).
		AndAssertResults(func(world *worldmock.MockWorld, verify *test.VMOutputVerifier) {
			verify.
				Ok().
				BytesAddedToStorage(test.ParentAddress,
					expectedAddByParent).
				BytesAddedToStorage(test.ChildAddress,
					expectedAddByChild).
				BytesDeletedFromStorage(test.ParentAddress,
					expectedDeletedByParent).
				BytesDeletedFromStorage(test.ChildAddress,
					expectedDeletedByChild)
		})
	assert.Nil(t, err)
}

var storageWriteHooks = map[string]func(instance *mock.InstanceMock, storedKeyLength int) int32{
	"storageStore": func(instance *mock.InstanceMock, storedKeyLength int) int32 {
		return vmhooks.StorageStoreWithTypedArgs(instance.Host, bytes.Repeat([]byte("k"), storedKeyLength), []byte("value"))
	},
	"smallIntStorageStoreUnsigned": func(instance *mock.InstanceMock, storedKeyLength int) int32 {
		_ = instance.MemStore(0, bytes.Repeat([]byte("k"), storedKeyLength))
		return vmhooks.NewVMHooksImpl(instance.Host).SmallIntStorageStoreUnsigned(0, executor.MemLength(storedKeyLength), 7)
	},
	"smallIntStorageStoreSigned": func(instance *mock.InstanceMock, storedKeyLength int) int32 {
		_ = instance.MemStore(0, bytes.Repeat([]byte("k"), storedKeyLength))
		return vmhooks.NewVMHooksImpl(instance.Host).SmallIntStorageStoreSigned(0, executor.MemLength(storedKeyLength), -7)
	},
	"bigIntStorageStoreUnsigned": func(instance *mock.InstanceMock, storedKeyLength int) int32 {
		_ = instance.MemStore(0, bytes.Repeat([]byte("k"), storedKeyLength))
		valueHandle := instance.Host.ManagedTypes().NewBigIntFromInt64(7)
		return vmhooks.NewVMHooksImpl(instance.Host).BigIntStorageStoreUnsigned(0, executor.MemLength(storedKeyLength), valueHandle)
	},
	"mBufferStorageStore": func(instance *mock.InstanceMock, storedKeyLength int) int32 {
		managedTypes := instance.Host.ManagedTypes()
		keyHandle := managedTypes.NewManagedBufferFromBytes(bytes.Repeat([]byte("k"), storedKeyLength))
		valueHandle := managedTypes.NewManagedBufferFromBytes([]byte("value"))
		return vmhooks.NewVMHooksImpl(instance.Host).MBufferStorageStore(keyHandle, valueHandle)
	},
	"setStorageLock": func(instance *mock.InstanceMock, storedKeyLength int) int32 {
		timeLockKeyPrefix := instance.Host.Storage().GetVmProtectedPrefix(vmhost.TimeLockKeyPrefix)
		return vmhooks.SetStorageLockWithTypedArgs(instance.Host, bytes.Repeat([]byte("k"), storedKeyLength-len(timeLockKeyPrefix)), 1)
	},
}

func requireWrittenKeyOfLength(t *testing.T, verify *test.VMOutputVerifier, address []byte, keyLength int) {
	outputAccount := verify.VmOutput.OutputAccounts[string(address)]
	require.NotNil(t, outputAccount)
	for _, update := range outputAccount.StorageUpdates {
		if update.Written && len(update.Offset) == keyLength {
			return
		}
	}
	require.Fail(t, fmt.Sprintf("no written storage update with a %d-byte key", keyLength))
}

func requireKeyNotWritten(t *testing.T, verify *test.VMOutputVerifier, key []byte) {
	if verify.VmOutput == nil {
		return
	}
	for _, outputAccount := range verify.VmOutput.OutputAccounts {
		update, found := outputAccount.StorageUpdates[string(key)]
		require.False(t, found && update.Written)
	}
}

func TestStorageKeyLength_WriteHooksEnforceLimitFromFixAuditChangesV6(t *testing.T) {
	for hookName, writeStorage := range storageWriteHooks {
		for _, storedKeyLength := range []int{common.MaxStorageKeyLength, common.MaxStorageKeyLength + 1} {
			t.Run(fmt.Sprintf("%s/key length %d", hookName, storedKeyLength), func(t *testing.T) {
				forEachFixAuditChangesV6(t, 0, func(t *testing.T, epochs blockchainConfig.EnableEpochs, _ uint64) {
					rejected := storedKeyLength > common.MaxStorageKeyLength && epochs.FixAuditChangesV6 == 0

					_, err := test.BuildMockInstanceCallTest(t).
						WithEnableEpochs(epochs).
						WithContracts(
							test.CreateMockContract(test.ParentAddress).
								WithMethods(func(parentInstance *mock.InstanceMock, _ any) {
									parentInstance.AddMockMethod("writeStorage", func() *mock.InstanceMock {
										writeStorage(parentInstance, storedKeyLength)
										return parentInstance
									})
								})).
						WithInput(test.CreateTestContractCallInputBuilder().
							WithRecipientAddr(test.ParentAddress).
							WithGasProvided(10_000_000).
							WithFunction("writeStorage").
							Build()).
						WithSetup(func(host vmhost.VMHost, _ *worldmock.MockWorld) {
							setZeroCodeCosts(host)
						}).
						AndAssertResults(func(_ *worldmock.MockWorld, verify *test.VMOutputVerifier) {
							if rejected {
								verify.ExecutionFailed().HasRuntimeErrors(vmhost.ErrStorageKeyTooLong.Error())
								return
							}
							verify.Ok()
							requireWrittenKeyOfLength(t, verify, test.ParentAddress, storedKeyLength)
						})
					require.NoError(t, err)
				})
			})
		}
	}
}

func TestStorageKeyLength_LegacyOverLimitKeyFromFixAuditChangesV6(t *testing.T) {
	legacyKey := bytes.Repeat([]byte("k"), common.MaxStorageKeyLength+1)
	legacyValue := []byte("legacy")

	runOnLegacyKey := func(t *testing.T, write func(host vmhost.VMHost), assertResults func(verify *test.VMOutputVerifier)) {
		_, err := test.BuildMockInstanceCallTest(t).
			WithContracts(
				test.CreateMockContract(test.ParentAddress).
					WithMethods(func(parentInstance *mock.InstanceMock, _ any) {
						parentInstance.AddMockMethod("writeLegacyKey", func() *mock.InstanceMock {
							write(parentInstance.Host)
							return parentInstance
						})
					})).
			WithInput(test.CreateTestContractCallInputBuilder().
				WithRecipientAddr(test.ParentAddress).
				WithGasProvided(10_000_000).
				WithFunction("writeLegacyKey").
				Build()).
			WithSetup(func(host vmhost.VMHost, world *worldmock.MockWorld) {
				setZeroCodeCosts(host)
				accountHandler, _ := world.GetUserAccount(test.ParentAddress)
				_ = accountHandler.DataTrieTracker().SaveKeyValue(legacyKey, legacyValue)
				_ = world.AccountsCacher.SaveAll()
			}).
			AndAssertResults(func(_ *worldmock.MockWorld, verify *test.VMOutputVerifier) {
				assertResults(verify)
			})
		require.NoError(t, err)
	}

	t.Run("identical rewrite is a no-op", func(t *testing.T) {
		runOnLegacyKey(t, func(host vmhost.VMHost) {
			vmhooks.StorageStoreWithTypedArgs(host, legacyKey, legacyValue)
		}, func(verify *test.VMOutputVerifier) {
			verify.Ok()
			requireKeyNotWritten(t, verify, legacyKey)
		})
	})

	t.Run("delete is allowed", func(t *testing.T) {
		runOnLegacyKey(t, func(host vmhost.VMHost) {
			vmhooks.StorageStoreWithTypedArgs(host, legacyKey, nil)
		}, func(verify *test.VMOutputVerifier) {
			verify.Ok()
			update := verify.VmOutput.OutputAccounts[string(test.ParentAddress)].StorageUpdates[string(legacyKey)]
			require.NotNil(t, update)
			require.True(t, update.Written)
			require.Empty(t, update.Data)
		})
	})

	t.Run("delete then rewrite is rejected", func(t *testing.T) {
		runOnLegacyKey(t, func(host vmhost.VMHost) {
			vmhooks.StorageStoreWithTypedArgs(host, legacyKey, nil)
			vmhooks.StorageStoreWithTypedArgs(host, legacyKey, []byte("value"))
		}, func(verify *test.VMOutputVerifier) {
			verify.ExecutionFailed().HasRuntimeErrors(vmhost.ErrStorageKeyTooLong.Error())
		})
	})
}

func TestStorageKeyLength_NestedCallWriteMergesBack(t *testing.T) {
	keyAtLimit := bytes.Repeat([]byte("k"), common.MaxStorageKeyLength)
	keyOverLimit := bytes.Repeat([]byte("k"), common.MaxStorageKeyLength+1)

	runNestedWrite := func(t *testing.T, childKey []byte, assertResults func(verify *test.VMOutputVerifier)) {
		_, err := test.BuildMockInstanceCallTest(t).
			WithContracts(
				test.CreateMockContract(test.ParentAddress).
					WithMethods(func(parentInstance *mock.InstanceMock, _ any) {
						parentInstance.AddMockMethod("callChild", func() *mock.InstanceMock {
							vmhooks.ExecuteOnDestContextWithTypedArgs(
								parentInstance.Host, 1_000_000, big.NewInt(0), []byte("writeStorage"), test.ChildAddress, nil)
							return parentInstance
						})
					}),
				test.CreateMockContract(test.ChildAddress).
					WithMethods(func(childInstance *mock.InstanceMock, _ any) {
						childInstance.AddMockMethod("writeStorage", func() *mock.InstanceMock {
							vmhooks.StorageStoreWithTypedArgs(childInstance.Host, childKey, []byte("value"))
							return childInstance
						})
					})).
			WithInput(test.CreateTestContractCallInputBuilder().
				WithRecipientAddr(test.ParentAddress).
				WithGasProvided(10_000_000).
				WithFunction("callChild").
				Build()).
			WithSetup(func(host vmhost.VMHost, _ *worldmock.MockWorld) {
				setZeroCodeCosts(host)
			}).
			AndAssertResults(func(_ *worldmock.MockWorld, verify *test.VMOutputVerifier) {
				assertResults(verify)
			})
		require.NoError(t, err)
	}

	t.Run("write at the limit merges back as written", func(t *testing.T) {
		runNestedWrite(t, keyAtLimit, func(verify *test.VMOutputVerifier) {
			verify.Ok().Storage(test.CreateStoreEntry(test.ChildAddress).WithKey(keyAtLimit).WithValue([]byte("value")))
		})
	})

	t.Run("write over the limit is rejected", func(t *testing.T) {
		runNestedWrite(t, keyOverLimit, func(verify *test.VMOutputVerifier) {
			verify.HasRuntimeErrors(vmhost.ErrStorageKeyTooLong.Error())
			requireKeyNotWritten(t, verify, keyOverLimit)
		})
	})
}
