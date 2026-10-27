package hooks

import (
	"encoding/binary"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/klever-io/klever-go/common"
	commonMock "github.com/klever-io/klever-go/common/mock"
	chainconfig "github.com/klever-io/klever-go/config"
	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/core/fork"
	"github.com/klever-io/klever-go/core/kapp"
	"github.com/klever-io/klever-go/core/kapp/builtInFunctions"
	"github.com/klever-io/klever-go/core/process/kda/kdautils"
	"github.com/klever-io/klever-go/crypto/hashing/sha256"
	"github.com/klever-io/klever-go/data/block"
	"github.com/klever-io/klever-go/data/transaction"
	"github.com/klever-io/klever-go/kapps"
	vmconfig "github.com/klever-io/klever-go/kvm/config"
	mock "github.com/klever-io/klever-go/kvm/mock/context"
	worldmock "github.com/klever-io/klever-go/kvm/mock/world"
	test "github.com/klever-io/klever-go/kvm/testcommon"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/kvm/vmhost/hostCore"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/klever-io/klever-go/vmcommon"
	"github.com/klever-io/klever-go/vmcommon/parsers"
	"github.com/stretchr/testify/require"
)

// Hex keys differing only in case can pay different shares to the same address.
// A parent contract can observe their order through getBackTransfers and persist
// the first amount, so these receipts affect the user state root.
func TestITOPercentageRoyalties_BackTransferStorageOrder(t *testing.T) {
	for _, fixV6 := range []bool{false, true} {
		name := "before V6"
		if fixV6 {
			name = "V6"
		}
		t.Run(name, func(t *testing.T) {
			parentAddr := test.MakeTestSCAddressWithVMType("parentSC", common.WasmVirtualMachine)
			childAddr := test.MakeTestSCAddressWithVMType("childSC", common.WasmVirtualMachine)
			stored := map[int64]bool{}
			roots := map[string]bool{}
			gasRemaining := map[uint64]bool{}
			for range 100 {
				var world *worldmock.MockWorld
				var first int64
				world = worldmock.NewMockWorld()
				host, factory := newITOBackTransferHost(t, world, fixV6)
				func() {
					defer host.Reset()
					contracts := []test.MockTestSmartContract{
						test.CreateMockContract(parentAddr).WithBalance(1000).WithMethods(func(instance *mock.InstanceMock, _ interface{}) {
							instance.AddMockMethod("auditParent", func() *mock.InstanceMock {
								rc := vmhooks.ExecuteOnDestContextWithTypedArgs(instance.Host, 100000, big.NewInt(0), []byte("auditChild"), childAddr, nil)
								require.Zero(t, rc)
								managed := instance.Host.ManagedTypes()
								transferVectorHandle := managed.NewManagedBuffer()
								klvValueHandle := managed.NewBigInt(big.NewInt(0))
								vmhooks.NewVMHooksImpl(instance.Host).ManagedGetBackTransfers(transferVectorHandle, klvValueHandle)
								serializedTransfers, err := managed.GetBytes(transferVectorHandle)
								require.NoError(t, err)
								// Each entry contains a token handle (4 bytes), nonce (8), and value handle (4).
								require.Len(t, serializedTransfers, 2*16)
								firstValue, err := managed.GetBigInt(int32(binary.BigEndian.Uint32(serializedTransfers[12:16])))
								require.NoError(t, err)
								first = firstValue.Int64()
								_, err = instance.Host.Storage().SetStorage([]byte("firstRoyalty"), firstValue.Bytes())
								require.NoError(t, err)
								return instance
							})
						}),
						test.CreateMockContract(childAddr).WithBalance(1000).WithMethods(func(instance *mock.InstanceMock, _ interface{}) {
							instance.AddMockMethod("auditChild", func() *mock.InstanceMock {
								_, err := instance.Host.ExecuteOnDestContext(&vmcommon.ContractCallInput{VMInput: vmcommon.VMInput{CallerAddr: childAddr, GasProvided: 50000, Arguments: [][]byte{big.NewInt(int64(transaction.BuyContract_ITOBuy)).Bytes(), []byte("AUD-1234"), []byte("PAY-1234"), {1}, {100}}}, RecipientAddr: childAddr, Function: core.BuiltInFunctionBuy})
								require.NoError(t, err)
								return instance
							})
						}),
					}
					for _, contract := range contracts {
						contract.Initialize(t, host, factory.LastCreatedExecutor, true)
					}
					func(host vmhost.VMHost, w *worldmock.MockWorld) {
						host.SetBuiltInFunctionsContainer(w.BuiltinFuncs.Container)
						user, err := w.AccountsCacher.LoadUser(test.UserAddress)
						require.NoError(t, err)
						require.NoError(t, w.AccountsCacher.SaveUser(user))
						kc := w.KAppController
						kc.SetCurrentKAppContext(kapp.NewKappContext(kapp.ArgsNewKAppContext{OriginalSender: test.UserAddress, Block: &block.Block{Header: &block.BlockHeader{Timestamp: 1000}}}))
						parentKey := hex.EncodeToString(parentAddr)
						require.NotEqual(t, parentKey, strings.ToUpper(parentKey))
						asset := &kapps.KDAData{ID: []byte("AUD-1234"), AssetType: kapps.KDAData_Fungible, OwnerAddress: test.UserAddress, MaxSupply: 10000, Properties: &kapps.PropertiesData{CanMint: true}, Attributes: &kapps.AttributesData{}, Roles: []*kapps.RolesData{{Address: kapps.ITOKAppAddress, HasRoleMint: true}}, Royalties: &kapps.RoyaltiesData{Address: test.UserAddress, ITOPercentage: 10000, SplitRoyalties: map[string]*kapps.RoyaltySplitData{parentKey: {PercentITOPercentage: 1000}, strings.ToUpper(parentKey): {PercentITOPercentage: 2000}}}}
						payment := &kapps.KDAData{ID: []byte("PAY-1234"), AssetType: kapps.KDAData_Fungible, OwnerAddress: test.UserAddress, Properties: &kapps.PropertiesData{}, Attributes: &kapps.AttributesData{}, Royalties: &kapps.RoyaltiesData{}}
						ka, err := w.AccountsCacher.LoadKApp(kapps.KDAKAppAddress)
						require.NoError(t, err)
						require.NoError(t, kc.GetKDAKApp().SetKDA(ka, asset.ID, asset))
						require.NoError(t, kc.GetKDAKApp().SetKDA(ka, payment.ID, payment))
						require.NoError(t, w.AccountsCacher.UpdateKapp(ka))
						ia, err := w.AccountsCacher.LoadKApp(kapps.ITOKAppAddress)
						require.NoError(t, err)
						ito := &kapps.ITOData{IsActive: true, ReceiverAddress: test.UserAddress, PackData: map[string]*kapps.PackData{string(payment.ID): {Packs: []*kapps.Pack{{Amount: 1, Price: 100}}}}}
						data, err := worldmock.WorldMarshalizer.Marshal(ito)
						require.NoError(t, err)
						require.NoError(t, ia.DataTrieTracker().SaveKeyValue(kdautils.ToITOKey(asset.ID), data))
						require.NoError(t, w.AccountsCacher.UpdateKapp(ia))
						child, err := w.AccountsCacher.LoadUser(childAddr)
						require.NoError(t, err)
						require.NoError(t, child.AddToBalance(1000, payment.ID, true))
						require.NoError(t, w.AccountsCacher.UpdateUser(child))
						require.NoError(t, w.AccountsCacher.SaveAll())
						code, err := kc.GetKDAKApp().Trigger(test.UserAddress, &transaction.AssetTriggerContract{TriggerType: transaction.AssetTriggerContract_UpdateRoyalties, AssetID: asset.ID, Royalties: &transaction.RoyaltiesInfo{Address: test.UserAddress, ITOPercentage: 10000, SplitRoyalties: map[string]*transaction.RoyaltySplitInfo{parentKey: {PercentITOPercentage: 1000}, strings.ToUpper(parentKey): {PercentITOPercentage: 2000}}}}, nil)
						require.NoError(t, err)
						require.Equal(t, transaction.Transaction_Ok, code)
						require.NoError(t, w.AccountsCacher.SaveAll())
						// Use the actual production hook for receipt -> VM transfer conversion.
						buy, err := w.BuiltinFuncs.Container.Get(core.BuiltInFunctionBuy)
						require.NoError(t, err)
						functions := builtInFunctions.NewBuiltInFunctionContainer()
						require.NoError(t, functions.Add(core.BuiltInFunctionBuy, buy))
						hook := &BlockChainHookImpl{kappController: kc, builtInFunctions: functions, counter: &blockChainHookCounterPassthroughStub{}}
						require.NoError(t, w.BuiltinFuncs.Container.Replace(core.BuiltInFunctionBuy, &test.MockBuiltin{ProcessBuiltinFunctionCall: func(input *vmcommon.ContractCallInput) (*vmcommon.VMOutput, error) {
							return hook.ProcessBuiltInFunction(input)
						}}))
					}(host, world)
					world.CreateStateBackup()
					output, err := host.RunSmartContractCall(test.CreateTestContractCallInputBuilder().WithRecipientAddr(parentAddr).WithGasProvided(1000000).WithFunction("auditParent").Build())
					require.NoError(t, err)
					require.NotNil(t, output)
					require.Equal(t, vmcommon.Ok, output.ReturnCode)
					gasRemaining[output.GasRemaining] = true
					if fixV6 {
						require.Equal(t, int64(20), first)
					}
					w := world
					require.NoError(t, w.AccountsCacher.SaveAll())
					update := output.OutputAccounts[string(parentAddr)].StorageUpdates["firstRoyalty"]
					require.NotNil(t, update)
					require.Equal(t, first, new(big.Int).SetBytes(update.Data).Int64())
					// Apply the storage update as processVMOutput does, then inspect the real trie root.
					parent, err := w.AccountsCacher.LoadUser(parentAddr)
					require.NoError(t, err)
					require.NoError(t, parent.DataTrieTracker().SaveKeyValue(update.Offset, update.Data))
					require.NoError(t, w.AccountsCacher.UpdateUser(parent))
					require.NoError(t, w.AccountsCacher.SaveAll())
					root, err := w.AccountsAdapter.RootHash()
					require.NoError(t, err)
					roots[string(root)] = true
					stored[first] = true
				}()
			}
			if fixV6 {
				require.Len(t, stored, 1)
				require.Len(t, roots, 1)
			} else {
				// Preserve the legacy map order, without requiring Go to choose both orders.
				require.LessOrEqual(t, len(stored), 2)
				require.Equal(t, len(stored), len(roots))
			}
			require.Len(t, gasRemaining, 1)
			t.Logf("first royalty amounts %v; %d user trie roots; gas remaining %v", stored, len(roots), gasRemaining)
		})
	}
}

func newITOBackTransferHost(t *testing.T, world *worldmock.MockWorld, fixV6 bool) (vmhost.VMHost, *mock.ExecutorMockFactory) {
	t.Helper()
	epochs := chainconfig.EnableEpochs{}
	if !fixV6 {
		epochs.FixAuditChangesV6 = 1_000_000
	}
	notifier := &commonMock.EpochNotifierStub{}
	forks, err := fork.NewForkController(epochs, notifier)
	require.NoError(t, err)
	parser, err := parsers.NewKDATransferParser(worldmock.WorldMarshalizer)
	require.NoError(t, err)
	factory := mock.NewExecutorMockFactory(world)
	host, err := hostCore.NewVMHost(world, &vmhost.VMHostParameters{
		VMType: common.WasmVirtualMachine, GasSchedule: vmconfig.MakeGasMapForTests(),
		BuiltInFuncContainer: builtInFunctions.NewBuiltInFunctionContainer(),
		KDATransferParser:    parser, EpochNotifier: notifier, ForkController: forks,
		Hasher: &sha256.Sha256{}, OverrideVMExecutor: factory,
		ProtectedKeyPrefix: [][]byte{[]byte(kapps.ProtectedKleverKeyPrefix), []byte(kapps.ProtectedKLVKeyPrefix), []byte(kapps.ProtectedKFIKeyPrefix), []byte(kapps.KDAPrefix)},
	})
	require.NoError(t, err)
	require.NoError(t, world.InitBuiltinFunctions(host.GetGasScheduleMap(), forks))
	return host, factory
}
