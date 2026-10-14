package vmhooks_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/klever-io/klever-go/data/state"
	"github.com/klever-io/klever-go/kapps"
	"github.com/klever-io/klever-go/kvm/config"
	"github.com/klever-io/klever-go/kvm/executor"
	contextmock "github.com/klever-io/klever-go/kvm/mock/context"
	"github.com/klever-io/klever-go/kvm/vmhost"
	hostmock "github.com/klever-io/klever-go/kvm/vmhost/mock"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/klever-io/klever-go/tools/marshal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// kdaDecodeGasMap is kdaGetterGasMap with distinct fixed costs for the shared KDA read and for
// BigIntGetKDAExternalBalance's own, so one cannot pass for the other
func kdaDecodeGasMap() config.GasScheduleMap {
	gasMap := kdaGetterGasMap()
	gasMap["BaseOpsAPICost"]["GetExternalBalance"] = 113
	gasMap["BigIntAPICost"]["BigIntGetExternalBalance"] = 127
	return gasMap
}

// kdaDecodeLargeUser is a UserKDA with a 100 KB metadata and a few buckets, built fresh each call
func kdaDecodeLargeUser() *kapps.UserKDA {
	buckets, _ := kdaGetterBuckets(5, 16, 32)
	return &kapps.UserKDA{
		Balance:       1_000_000,
		FrozenBalance: 7,
		LastClaim:     &kapps.LastClaim{Timestamp: 11, Epoch: 12},
		Buckets:       buckets,
		MIME:          []byte("application/octet-stream"),
		Metadata:      kdaGetterBytes(100*1024, 'm'),
	}
}

// kdaDecodeLargeToken is a KDAData with a name, URIs, roles, royalties and properties, built fresh
// each call
func kdaDecodeLargeToken() *kapps.KDAData {
	roles, _ := kdaGetterRoles(20, 32)
	uris, _ := kdaGetterURIs(10, 16, 256)
	royalties, _ := kdaGetterRoyalties(32, 4, 4, 32)
	return &kapps.KDAData{
		ID:           []byte("TKN-ABCD"),
		Name:         []byte("A Token With A Name"),
		Ticker:       []byte("TKN"),
		OwnerAddress: kdaGetterBytes(32, 'o'),
		AdminAddress: kdaGetterBytes(32, 'p'),
		Logo:         string(kdaGetterBytes(512, 'l')),
		URIs:         uris,
		Precision:    6,
		MaxSupply:    1 << 50,
		Royalties:    royalties,
		Properties:   &kapps.PropertiesData{CanMint: true, CanBurn: true, CanAddRoles: true},
		Attributes:   &kapps.AttributesData{IsPaused: true},
		Roles:        roles,
	}
}

// kdaDecodeShape is a pair of records GetKDAToken hands back
type kdaDecodeShape struct {
	name string
	kda  func() *kapps.KDAData
	user func() *kapps.UserKDA
}

func kdaDecodeShapes() []kdaDecodeShape {
	return []kdaDecodeShape{
		// Properties and Attributes are dereferenced unconditionally by the legacy GetKDATokenData
		{"empty",
			func() *kapps.KDAData {
				return &kapps.KDAData{Properties: &kapps.PropertiesData{}, Attributes: &kapps.AttributesData{}}
			},
			func() *kapps.UserKDA { return &kapps.UserKDA{} }},
		{"large", kdaDecodeLargeToken, kdaDecodeLargeUser},
	}
}

const (
	kdaDecodeAddressPtr    = executor.MemPtr(0)
	kdaDecodeTokenIDPtr    = executor.MemPtr(64)
	kdaDecodeResultPtr     = executor.MemPtr(128)
	kdaDecodePropertiesPtr = executor.MemPtr(256)
	kdaDecodeOutHandle     = int32(1 << 29)
)

// kdaDecodeSentinel fills the output memory, so a hook that writes any of it is caught
var kdaDecodeSentinel = bytes.Repeat([]byte{0xEE}, 64)

// kdaDecodeLegacyHook is a hook that reads the token through getKDADataFromBlockchainHook
type kdaDecodeLegacyHook struct {
	name string
	// fixed is what the hook charges before the read, the read's fixed cost included
	fixed func(gasMap config.GasScheduleMap) uint64
	// afterDecodeBytes is what it charges DataCopyPerByte for from FixAuditChangesV6 after the decode
	afterDecodeBytes func(kda *kapps.KDAData) uint64
	call             func(hooks *vmhooks.VMHooksImpl)
	// bigIntAPI marks the hook that faults on the BigInt API predicate
	bigIntAPI bool
}

func kdaDecodePropertiesJSON(kda *kapps.KDAData) uint64 {
	properties, err := json.Marshal(kda.Properties)
	if err != nil {
		panic(err)
	}
	return uint64(len(properties))
}

func kdaDecodeLegacyHooks() []kdaDecodeLegacyHook {
	readCost := func(gasMap config.GasScheduleMap) uint64 { return gasMap["BaseOpsAPICost"]["GetExternalBalance"] }
	none := func(*kapps.KDAData) uint64 { return 0 }
	tokenIDLen := executor.MemLength(len(kdaGetterTicker))
	return []kdaDecodeLegacyHook{
		{"GetKDABalance", readCost, none, func(hooks *vmhooks.VMHooksImpl) {
			hooks.GetKDABalance(kdaDecodeAddressPtr, kdaDecodeTokenIDPtr, tokenIDLen, 0, kdaDecodeResultPtr)
		}, false},
		{"GetKDANameLength", readCost, none, func(hooks *vmhooks.VMHooksImpl) {
			hooks.GetKDANameLength(kdaDecodeAddressPtr, kdaDecodeTokenIDPtr, tokenIDLen, 0)
		}, false},
		{"GetKDAURILength", readCost, none, func(hooks *vmhooks.VMHooksImpl) {
			hooks.GetKDAURILength(kdaDecodeAddressPtr, kdaDecodeTokenIDPtr, tokenIDLen, 0)
		}, false},
		{"GetKDATokenData", readCost, kdaDecodePropertiesJSON, func(hooks *vmhooks.VMHooksImpl) {
			hooks.GetKDATokenData(kdaDecodeAddressPtr, kdaDecodeTokenIDPtr, tokenIDLen, 0, kdaDecodeOutHandle,
				kdaDecodeResultPtr, kdaDecodeResultPtr, kdaDecodeResultPtr, kdaDecodeResultPtr, kdaDecodeResultPtr,
				kdaDecodeResultPtr, kdaDecodeResultPtr, kdaDecodeResultPtr, kdaDecodeResultPtr, kdaDecodeResultPtr,
				kdaDecodePropertiesPtr, kdaDecodeResultPtr, kdaDecodeResultPtr)
		}, false},
		{"BigIntGetKDAExternalBalance", func(gasMap config.GasScheduleMap) uint64 {
			return gasMap["BigIntAPICost"]["BigIntGetExternalBalance"] + readCost(gasMap)
		}, none, func(hooks *vmhooks.VMHooksImpl) {
			hooks.BigIntGetKDAExternalBalance(kdaDecodeAddressPtr, kdaDecodeTokenIDPtr, tokenIDLen, 0, kdaDecodeOutHandle)
		}, true},
	}
}

// kdaDecodeLegacyRun is what one call of a legacy hook left behind
type kdaDecodeLegacyRun struct {
	gasUsed    uint64
	failErr    error
	result     []byte
	properties []byte
	outHandle  bool
}

func runKDADecodeLegacy(
	t *testing.T,
	gasMap config.GasScheduleMap,
	fixV6 bool,
	hook kdaDecodeLegacyHook,
	kda *kapps.KDAData,
	user *kapps.UserKDA,
	budget uint64,
) kdaDecodeLegacyRun {
	t.Helper()
	host, _ := newMockVMHostWithRealMetering(t, fixV6, gasMap, flatCloneGasProvided)
	runtime := host.RuntimeContext.(*contextmock.RuntimeContextMock)
	kdaGetterStubToken(host, kda, user)
	require.NoError(t, host.MemStoreToMock(kdaDecodeAddressPtr, kdaGetterAddress))
	require.NoError(t, host.MemStoreToMock(kdaDecodeTokenIDPtr, kdaGetterTicker))
	require.NoError(t, host.MemStoreToMock(kdaDecodeResultPtr, kdaDecodeSentinel))
	require.NoError(t, host.MemStoreToMock(kdaDecodePropertiesPtr, kdaDecodeSentinel))

	run := kdaDecodeLegacyRun{}
	run.gasUsed, _ = runFlatCloneHook(host, budget, func() {
		hook.call(vmhooks.NewVMHooksImpl(host))
	})
	run.failErr = runtime.FailExecutionErr

	var err error
	run.result, err = host.MemLoadFromMock(kdaDecodeResultPtr, len(kdaDecodeSentinel))
	require.NoError(t, err)
	run.properties, err = host.MemLoadFromMock(kdaDecodePropertiesPtr, len(kdaDecodeSentinel))
	require.NoError(t, err)
	_, err = host.ManagedTypes().GetBigInt(kdaDecodeOutHandle)
	run.outHandle = err == nil
	return run
}

// The legacy KDA getters read the token through one shared helper that paid a flat cost for two
// protobuf decodes of any size. From FixAuditChangesV6 the decoded records are paid for,
// DataCopyPerByte a byte, right after the read and before anything uses them; GetKDATokenData also
// pays for the properties JSON it writes. Before the fork the gas is what it always was
func TestVMHooksImpl_LegacyKDARead_ChargesTheDecodeFromV6(t *testing.T) {
	t.Parallel()

	gasMap := kdaDecodeGasMap()
	perByte := gasMap["BaseOperationCost"]["DataCopyPerByte"]
	for _, hook := range kdaDecodeLegacyHooks() {
		for _, shape := range kdaDecodeShapes() {
			t.Run(fmt.Sprintf("%s/%s", hook.name, shape.name), func(t *testing.T) {
				t.Parallel()

				kda, user := shape.kda(), shape.user()
				decoded := vmhooks.KDATokenDecodedLen(shape.kda(), shape.user())
				if shape.name == "large" {
					require.Greater(t, decoded, uint64(100*1024))
				}

				pre := runKDADecodeLegacy(t, gasMap, false, hook, kda, user, kdaGetterBudget)
				require.NoError(t, pre.failErr)
				assert.Equal(t, hook.fixed(gasMap), pre.gasUsed, "pre-fork gas")

				post := runKDADecodeLegacy(t, gasMap, true, hook, shape.kda(), shape.user(), kdaGetterBudget)
				require.NoError(t, post.failErr)
				tokenID := uint64(len(kdaGetterTicker))
				expected := hook.fixed(gasMap) + perByte*(tokenID+decoded+hook.afterDecodeBytes(kda))
				assert.Equal(t, expected, post.gasUsed, "post-fork gas")

				assert.Equal(t, pre.result, post.result)
				assert.Equal(t, pre.properties, post.properties)
				assert.Equal(t, pre.outHandle, post.outHandle)
			})
		}
	}
}

// From FixAuditChangesV6 a legacy getter that can pay for everything before the decode but not for
// the decode itself fails with ErrNotEnoughGas and writes nothing; one more unit and it succeeds,
// for the hooks that charge nothing after it. Before the fork the same budget is enough
func TestVMHooksImpl_LegacyKDARead_UnderfundedDecodeWritesNothingFromV6(t *testing.T) {
	t.Parallel()

	gasMap := kdaDecodeGasMap()
	perByte := gasMap["BaseOperationCost"]["DataCopyPerByte"]
	for _, hook := range kdaDecodeLegacyHooks() {
		t.Run(hook.name, func(t *testing.T) {
			t.Parallel()

			kda, user := kdaDecodeLargeToken(), kdaDecodeLargeUser()
			throughDecode := hook.fixed(gasMap) + perByte*(uint64(len(kdaGetterTicker))+vmhooks.KDATokenDecodedLen(kda, user))

			post := runKDADecodeLegacy(t, gasMap, true, hook, kda, user, throughDecode-1)
			require.ErrorIs(t, post.failErr, vmhost.ErrNotEnoughGas)
			assert.Equal(t, throughDecode-1, post.gasUsed, "a fault burns the gas left")
			assert.Equal(t, kdaDecodeSentinel, post.result, "result written")
			assert.Equal(t, kdaDecodeSentinel, post.properties, "properties written")
			assert.False(t, post.outHandle, "big int output created")

			if hook.afterDecodeBytes(kda) == 0 {
				paid := runKDADecodeLegacy(t, gasMap, true, hook, kdaDecodeLargeToken(), kdaDecodeLargeUser(), throughDecode)
				require.NoError(t, paid.failErr)
				assert.Equal(t, throughDecode, paid.gasUsed)
			}

			pre := runKDADecodeLegacy(t, gasMap, false, hook, kdaDecodeLargeToken(), kdaDecodeLargeUser(), throughDecode-1)
			require.NoError(t, pre.failErr)
			assert.Equal(t, hook.fixed(gasMap), pre.gasUsed)
		})
	}
}

// GetKDATokenData wrote the properties JSON into contract memory for nothing. From
// FixAuditChangesV6 it is paid for before the write: one unit short and memory is untouched
func TestVMHooksImpl_GetKDATokenData_ChargesThePropertiesFromV6(t *testing.T) {
	t.Parallel()

	gasMap := kdaDecodeGasMap()
	perByte := gasMap["BaseOperationCost"]["DataCopyPerByte"]
	var hook kdaDecodeLegacyHook
	for _, h := range kdaDecodeLegacyHooks() {
		if h.name == "GetKDATokenData" {
			hook = h
		}
	}
	require.NotNil(t, hook.call)

	for _, shape := range kdaDecodeShapes() {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()

			kda := shape.kda()
			propertiesJSON, err := json.Marshal(kda.Properties)
			require.NoError(t, err)
			full := hook.fixed(gasMap) + perByte*(uint64(len(kdaGetterTicker))+vmhooks.KDATokenDecodedLen(kda, shape.user())+uint64(len(propertiesJSON)))

			paid := runKDADecodeLegacy(t, gasMap, true, hook, kda, shape.user(), full)
			require.NoError(t, paid.failErr)
			assert.Equal(t, full, paid.gasUsed)
			assert.Equal(t, propertiesJSON, paid.properties[:len(propertiesJSON)])

			short := runKDADecodeLegacy(t, gasMap, true, hook, shape.kda(), shape.user(), full-1)
			require.ErrorIs(t, short.failErr, vmhost.ErrNotEnoughGas)
			assert.Equal(t, kdaDecodeSentinel, short.properties, "properties written")
			assert.False(t, short.outHandle, "precision handle created")

			pre := runKDADecodeLegacy(t, gasMap, false, hook, shape.kda(), shape.user(), full-1)
			require.NoError(t, pre.failErr)
			assert.Equal(t, propertiesJSON, pre.properties[:len(propertiesJSON)])
		})
	}
}

// kdaDecodeManagedCases are the managed KDA reads whose outputs do not depend on the decoded size,
// on the empty and the large records: ManagedGetKDABalance, and ManagedGetKDARoles, which reads the
// token without an address and so must be charged for the KDAData alone, whatever the read hands
// back as the UserKDA
func kdaDecodeManagedCases(gasMap config.GasScheduleMap) []kdaGetterCase {
	balanceOut := kdaGetterOuts(1, 0)
	rolesOut := kdaGetterOuts(1, 1)
	cases := make([]kdaGetterCase, 0)
	for _, shape := range kdaDecodeShapes() {
		shape := shape
		kda := shape.kda()
		kda.Roles = nil
		cases = append(cases,
			kdaGetterCase{
				name: "ManagedGetKDABalance/" + shape.name,
				install: func(host *contextmock.VMHostMock) {
					kdaGetterStubToken(host, shape.kda(), shape.user())
				},
				call: func(hooks *vmhooks.VMHooksImpl, addressHandle, tickerHandle int32) {
					hooks.ManagedGetKDABalance(addressHandle, tickerHandle, 0, balanceOut[0])
				},
				fixed:         gasMap["BaseOpsAPICost"]["GetExternalBalance"],
				inputBytes:    uint64(len(kdaGetterAddress) + len(kdaGetterTicker)),
				v6DecodeBytes: vmhooks.KDATokenDecodedLen(shape.kda(), shape.user()),
				bigIntOuts:    balanceOut,
			},
			kdaGetterCase{
				name: "ManagedGetKDARoles/" + shape.name,
				install: func(host *contextmock.VMHostMock) {
					blockchain := host.BlockchainContext.(*hostmock.BlockchainContextStub)
					blockchain.GetKDATokenCalled = func(address []byte, _ []byte, _ uint64) (*kapps.KDAData, *kapps.UserKDA, error) {
						if address != nil {
							return nil, nil, fmt.Errorf("ManagedGetKDARoles read with an address")
						}
						rolesless := shape.kda()
						rolesless.Roles = nil
						return rolesless, kdaDecodeLargeUser(), nil
					}
				},
				call: func(hooks *vmhooks.VMHooksImpl, _, tickerHandle int32) {
					hooks.ManagedGetKDARoles(tickerHandle, rolesOut[0])
				},
				fixed:         gasMap["BaseOpsAPICost"]["GetKDARoles"],
				inputBytes:    uint64(len(kdaGetterTicker)),
				v6DecodeBytes: vmhooks.KDATokenDecodedLen(kda, nil),
				bufferOuts:    rolesOut,
			},
		)
	}
	return cases
}

// From FixAuditChangesV6 the managed KDA reads pay for the decoded records on top of everything
// else; before it the gas is unchanged
func TestVMHooksImpl_ManagedKDARead_ChargesTheDecodeFromV6(t *testing.T) {
	t.Parallel()

	gasMap := kdaDecodeGasMap()
	perByte := gasMap["BaseOperationCost"]["DataCopyPerByte"]
	for _, c := range kdaDecodeManagedCases(gasMap) {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			pre := runKDAGetter(t, gasMap, false, c, kdaGetterBudget)
			post := runKDAGetter(t, gasMap, true, c, kdaGetterBudget)
			require.NoError(t, pre.failErr)
			require.NoError(t, post.failErr)
			assert.Equal(t, c.fixed, pre.gasUsed, "pre-fork gas")
			assert.Equal(t, c.fixed+perByte*(c.inputBytes+c.v6DecodeBytes), post.gasUsed, "post-fork gas")
			assert.Equal(t, c.gas(gasMap, true), post.gasUsed)
			assert.Equal(t, pre.buffers, post.buffers)
			assert.Equal(t, pre.bigInts, post.bigInts)
		})
	}
}

// From FixAuditChangesV6 a managed KDA read that can pay for its fixed cost and its inputs but not
// for the decode fails with ErrNotEnoughGas, which faultKDAArg keeps (KLC-2719), before any handle
// is created or written
func TestVMHooksImpl_ManagedKDARead_UnderfundedDecodeBuildsNothingFromV6(t *testing.T) {
	t.Parallel()

	gasMap := kdaDecodeGasMap()
	perByte := gasMap["BaseOperationCost"]["DataCopyPerByte"]
	cases := append(kdaDecodeManagedCases(gasMap), kdaGetterAllCases(gasMap)...)
	for _, c := range cases {
		if c.v6DecodeBytes == 0 {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			budget := c.fixed + perByte*(c.inputBytes+c.v6DecodeBytes) - 1
			post := runKDAGetter(t, gasMap, true, c, budget)
			require.ErrorIs(t, post.failErr, vmhost.ErrNotEnoughGas)
			assert.Equal(t, budget, post.gasUsed, "a fault burns the gas left")
			assert.Zero(t, post.newBuffers, "buffers created")
			assert.Zero(t, post.newBigInts, "big ints created")
			for i, data := range post.buffers {
				assert.Nil(t, data, "buffer output %d", i)
			}
			for i, value := range post.bigInts {
				assert.Nil(t, value, "big int output %d", i)
			}
		})
	}
}

var benchmarkKDADecodeSink int

// BenchmarkKDATokenDecode measures what GetKDAToken's two decodes cost in time, through the
// marshalizer the state uses, against the flat GetExternalBalance a KDA read pays (7_000 on
// gasScheduleV1) and against that plus DataCopyPerByte (50) per decoded byte, as from
// FixAuditChangesV6. The UserKDA blob is at MaxLeafSize, the most a leaf can hold
func BenchmarkKDATokenDecode(b *testing.B) {
	const (
		flatCost        = 7_000
		dataCopyPerByte = 50
	)
	marshalizer := marshal.NewProtoMarshalizer()

	user := kdaDecodeLargeUser()
	user.Metadata = nil
	overhead := proto.Size(user) + 4 // field tag and a 3-byte length prefix
	user.Metadata = kdaGetterBytes(int(state.MaxLeafSize)-overhead, 'm')
	userBlob, err := proto.Marshal(user)
	require.NoError(b, err)
	require.Equal(b, int(state.MaxLeafSize), len(userBlob))

	// the same cap filled with small buckets instead: a map entry each, which decodes slower per
	// byte than one metadata copy
	bucketsUser := &kapps.UserKDA{Balance: 1, Buckets: map[string]*kapps.UserBucket{}}
	addBuckets := func(from, n int) {
		for i := from; i < from+n; i++ {
			bucketsUser.Buckets[fmt.Sprintf("%x", i)] = &kapps.UserBucket{Value: 1, StakedEpoch: 1}
		}
	}
	addBuckets(0, 1_000)
	perBucket := float64(proto.Size(bucketsUser)) / 1_000
	addBuckets(1_000, int(float64(state.MaxLeafSize)/perBucket)-1_100)
	for proto.Size(bucketsUser) > int(state.MaxLeafSize) {
		delete(bucketsUser.Buckets, fmt.Sprintf("%x", len(bucketsUser.Buckets)-1))
	}
	bucketsBlob, err := proto.Marshal(bucketsUser)
	require.NoError(b, err)

	kda := kdaDecodeLargeToken()
	kda.Roles, _ = kdaGetterRoles(50, 32)
	kda.URIs, _ = kdaGetterURIs(20, 32, 256)
	kdaBlob, err := proto.Marshal(kda)
	require.NoError(b, err)

	report := func(b *testing.B, size int) {
		nsPerOp := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
		b.ReportMetric(float64(size), "bytes")
		b.ReportMetric(nsPerOp/flatCost, "ns/flat-gas")
		newGas := float64(flatCost + dataCopyPerByte*size)
		b.ReportMetric(newGas, "v6-gas")
		b.ReportMetric(nsPerOp/newGas, "ns/v6-gas")
	}

	b.Run(fmt.Sprintf("UserKDA/%dB", len(userBlob)), func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			decoded := &kapps.UserKDA{}
			if err := marshalizer.Unmarshal(decoded, userBlob); err != nil {
				b.Fatal(err)
			}
			benchmarkKDADecodeSink += len(decoded.Metadata)
		}
		report(b, len(userBlob))
	})
	b.Run(fmt.Sprintf("UserKDABuckets/%dB/%dbuckets", len(bucketsBlob), len(bucketsUser.Buckets)), func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			decoded := &kapps.UserKDA{}
			if err := marshalizer.Unmarshal(decoded, bucketsBlob); err != nil {
				b.Fatal(err)
			}
			benchmarkKDADecodeSink += len(decoded.Buckets)
		}
		report(b, len(bucketsBlob))
	})
	b.Run(fmt.Sprintf("KDAData/%dB", len(kdaBlob)), func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			decoded := &kapps.KDAData{}
			if err := marshalizer.Unmarshal(decoded, kdaBlob); err != nil {
				b.Fatal(err)
			}
			benchmarkKDADecodeSink += len(decoded.Name)
		}
		report(b, len(kdaBlob))
	})
	b.Run(fmt.Sprintf("Both/%dB", len(userBlob)+len(kdaBlob)), func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			decodedUser := &kapps.UserKDA{}
			decodedKDA := &kapps.KDAData{}
			if err := marshalizer.Unmarshal(decodedUser, userBlob); err != nil {
				b.Fatal(err)
			}
			if err := marshalizer.Unmarshal(decodedKDA, kdaBlob); err != nil {
				b.Fatal(err)
			}
			// the charge sizes what was decoded, so that is part of the read's cost too
			benchmarkKDADecodeSink += int(vmhooks.KDATokenDecodedLen(decodedKDA, decodedUser))
		}
		report(b, len(userBlob)+len(kdaBlob))
	})
}
