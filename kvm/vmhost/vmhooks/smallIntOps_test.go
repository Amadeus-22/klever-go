package vmhooks_test

import (
	"testing"

	contextmock "github.com/klever-io/klever-go/kvm/mock/context"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/stretchr/testify/assert"
)

// smallIntArgumentGetterGasCase is the SmallInt getters' row for runArgumentGetterGasCases: from
// FixAuditChangesV6 they pay CopyPerByteForTooBig for an argument past 32 bytes before parsing it.
// Their only destination is the value they return, so an unpaid parse shows as the failure 0
func smallIntArgumentGetterGasCase(fixedCost uint64, call func(hooks *vmhooks.VMHooksImpl, id int32) int64) argumentGetterGasCase {
	gasMap := argumentGetterGasMap()
	return argumentGetterGasCase{
		fixedCost: fixedCost,
		perByteFromV6: func(argLen int) uint64 {
			if argLen <= 32 {
				return 0
			}
			return uint64(argLen) * gasMap["BigIntAPICost"]["CopyPerByteForTooBig"]
		},
		badIndexErr: vmhost.ErrArgIndexOutOfRange,
		failed:      0,
		call:        call,
		// leading zeros do not change the value, so both arguments parse to the same int64
		assertCopied: func(t *testing.T, _ *contextmock.VMHostMock, result int64, _ []byte) {
			assert.Equal(t, int64(0x010203), result)
		},
		assertUntouched: func(_ *testing.T, _ *contextmock.VMHostMock) {},
	}
}

func TestVMHooksImpl_SmallIntGetUnsignedArgument_GasPerByteFromV6(t *testing.T) {
	t.Parallel()

	runArgumentGetterGasCases(t, smallIntArgumentGetterGasCase(
		argumentGetterGasMap()["BaseOpsAPICost"]["Int64GetArgument"],
		func(hooks *vmhooks.VMHooksImpl, id int32) int64 {
			return hooks.SmallIntGetUnsignedArgument(id)
		},
	))
}

func TestVMHooksImpl_SmallIntGetSignedArgument_GasPerByteFromV6(t *testing.T) {
	t.Parallel()

	runArgumentGetterGasCases(t, smallIntArgumentGetterGasCase(
		argumentGetterGasMap()["BaseOpsAPICost"]["Int64GetArgument"],
		func(hooks *vmhooks.VMHooksImpl, id int32) int64 {
			return hooks.SmallIntGetSignedArgument(id)
		},
	))
}
