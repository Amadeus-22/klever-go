package vmhooks_test

import (
	"encoding/binary"
	"math/big"
	"testing"

	test "github.com/klever-io/klever-go/kvm/testcommon"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/stretchr/testify/require"
)

// Decoding the KDA-transfer vector of managedMultiTransferKDANFTExecute (GHSA-3xw3-gjh2-5j94).
//
// readKDATransfers clones the token identifier buffer of every 16-byte record. Pre-fork each clone
// is charged afterwards through a charge that cannot fail, so a vector naming one large buffer many
// times clones it once per record whatever the gas left. Post-fork (FixAuditChangesV6) each clone is
// paid, bounded, before it is made, so the decode stops at the gas limit.

const (
	kdaTransferRecordLen    = 16
	kdaTransfersFollowUpGas = 100_000
)

// kdaTransferVector encodes numRecords records that all name tokenHandle and valueHandle.
func kdaTransferVector(numRecords int, tokenHandle int32, valueHandle int32) []byte {
	vec := make([]byte, numRecords*kdaTransferRecordLen)
	for i := range numRecords {
		record := vec[i*kdaTransferRecordLen : (i+1)*kdaTransferRecordLen]
		binary.BigEndian.PutUint32(record[0:4], uint32(tokenHandle))   // #nosec G115
		binary.BigEndian.PutUint32(record[12:16], uint32(valueHandle)) // #nosec G115
	}
	return vec
}

// multiTransferWithTransfers calls managedMultiTransferKDANFTExecute to the child with the
// transfers vector buildVector returns and no arguments, and records the hook's return code.
func multiTransferWithTransfers(
	buildVector func(managedType vmhost.ManagedTypesContext) []byte,
	returnCode *int32,
) func(vmhost.VMHost) func() {
	return func(host vmhost.VMHost) func() {
		managedType := host.ManagedTypes()
		dstHandle := managedType.NewManagedBufferFromBytes(test.ChildAddress)
		functionHandle := managedType.NewManagedBufferFromBytes([]byte(managedHookChildFunction))
		argumentsHandle := managedType.NewManagedBufferFromBytes(nil)
		vecHandle := managedType.NewManagedBufferFromBytes(buildVector(managedType))
		return func() {
			*returnCode = vmhooks.NewVMHooksImpl(host).ManagedMultiTransferKDANFTExecute(
				dstHandle, vecHandle, kdaTransfersFollowUpGas, functionHandle, argumentsHandle)
		}
	}
}

// repeatedTokenVector stores one tokenLen-byte token identifier and names it numRecords times.
func repeatedTokenVector(numRecords int, tokenLen int) func(vmhost.ManagedTypesContext) []byte {
	return func(managedType vmhost.ManagedTypesContext) []byte {
		tokenHandle := managedType.NewManagedBufferFromBytes(make([]byte, tokenLen))
		valueHandle := managedType.NewBigInt(big.NewInt(1))
		return kdaTransferVector(numRecords, tokenHandle, valueHandle)
	}
}

func TestManagedMultiTransfer_RepeatedTokenHandle_PreForkVersusPostFork(t *testing.T) {
	const gasLeft = uint64(10_000_000)

	cases := []struct {
		name       string
		numRecords int
		tokenLen   int
	}{
		{name: "256 records x 64 KiB", numRecords: 256, tokenLen: 64 * 1024},
		{name: "1024 records x 16 KiB", numRecords: 1024, tokenLen: 16 * 1024},
	}

	for _, tc := range cases {
		cloneTraffic := uint64(tc.numRecords) * uint64(tc.tokenLen)

		var preRet, postRet int32
		pre := runManagedHook(t, preForkV6EnableEpochs(), gasLeft, multiTransferWithTransfers(repeatedTokenVector(tc.numRecords, tc.tokenLen), &preRet))
		post := runManagedHook(t, postForkV6EnableEpochs(), gasLeft, multiTransferWithTransfers(repeatedTokenVector(tc.numRecords, tc.tokenLen), &postRet))

		t.Logf("%s, %d gas left, clone traffic %d B", tc.name, gasLeft, cloneTraffic)
		t.Logf("  pre-fork:  ret=%d gasUsed=%d allocated=%d B", preRet, pre.gasUsed, pre.allocated)
		t.Logf("  post-fork: ret=%d gasUsed=%d allocated=%d B", postRet, post.gasUsed, post.allocated)

		// the legacy path still clones every record, pinned for block replay
		require.GreaterOrEqual(t, pre.allocated, cloneTraffic, tc.name)
		require.Greater(t, pre.gasUsed, gasLeft, tc.name)

		// post-fork the hook stops at the gas it had: every clone is paid at DataCopyPerByte before
		// it is made, so it can clone at most gasLeft/DataCopyPerByte bytes (200 KB here)
		require.Equal(t, int32(-1), postRet, tc.name)
		require.LessOrEqual(t, post.gasUsed, gasLeft, tc.name)
		// TotalAlloc is process-wide, so this is a loose bound with room for unrelated allocations;
		// it still sits an order of magnitude below the clone traffic the legacy path allocates
		require.Less(t, post.allocated, cloneTraffic/8, tc.name)
	}
}

func TestManagedMultiTransfer_DistinctTokens_SameGasBothSides(t *testing.T) {
	const gasLeft = uint64(10_000_000)

	// three payments, each record with its own small token identifier buffer: the honest shape
	const numPayments = 3
	distinctTokens := func(managedType vmhost.ManagedTypesContext) []byte {
		vec := make([]byte, 0, numPayments*kdaTransferRecordLen)
		for range numPayments {
			tokenHandle := managedType.NewManagedBufferFromBytes(test.KDATestTokenName)
			valueHandle := managedType.NewBigInt(big.NewInt(1))
			vec = append(vec, kdaTransferVector(1, tokenHandle, valueHandle)...)
		}
		return vec
	}

	var preRet, postRet int32
	pre := runManagedHook(t, preForkV6EnableEpochs(), gasLeft, multiTransferWithTransfers(distinctTokens, &preRet))
	post := runManagedHook(t, postForkV6EnableEpochs(), gasLeft, multiTransferWithTransfers(distinctTokens, &postRet))

	t.Logf("pre-fork:  ret=%d gasUsed=%d", preRet, pre.gasUsed)
	t.Logf("post-fork: ret=%d gasUsed=%d", postRet, post.gasUsed)

	require.Equal(t, int32(0), preRet)
	require.Equal(t, preRet, postRet)
	require.Equal(t, pre.gasUsed, post.gasUsed)
}

func TestManagedMultiTransfer_MoreRecordsThanTransferValueCovers_PreForkVersusPostFork(t *testing.T) {
	const gasLeft = uint64(10_000_000)
	// the vector and the tokens fit the gas (5000 x (16+3) x DataCopyPerByte), but TransferValue
	// for 5000 transfers does not: an honest charge stops at about 100 of them
	const numRecords, tokenLen = 5000, 3

	// on the mainnet TransferValue (config/node/gasScheduleV1.yaml), not the test schedule's
	onMainnetTransferValue := func(returnCode *int32) func(vmhost.VMHost) func() {
		prepare := multiTransferWithTransfers(repeatedTokenVector(numRecords, tokenLen), returnCode)
		return func(host vmhost.VMHost) func() {
			host.Metering().GasSchedule().BaseOpsAPICost.TransferValue = 100_000
			return prepare(host)
		}
	}

	var preRet, postRet int32
	pre := runManagedHook(t, preForkV6EnableEpochs(), gasLeft, onMainnetTransferValue(&preRet))
	post := runManagedHook(t, postForkV6EnableEpochs(), gasLeft, onMainnetTransferValue(&postRet))

	t.Logf("pre-fork:  ret=%d gasUsed=%d allocated=%d B", preRet, pre.gasUsed, pre.allocated)
	t.Logf("post-fork: ret=%d gasUsed=%d allocated=%d B", postRet, post.gasUsed, post.allocated)

	// the legacy path builds every record before TransferValue is charged, pinned for block replay
	require.Greater(t, pre.gasUsed, gasLeft)

	// post-fork the decode fails before building a record
	require.Equal(t, int32(-1), postRet)
	require.LessOrEqual(t, post.gasUsed, gasLeft)
	// TotalAlloc is process-wide, so this is a loose bound
	require.Less(t, post.allocated, pre.allocated/2)
}
