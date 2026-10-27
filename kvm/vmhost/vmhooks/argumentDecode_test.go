package vmhooks_test

import (
	"fmt"
	"math/big"
	"testing"

	contextmock "github.com/klever-io/klever-go/kvm/mock/context"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/klever-io/klever-go/vmcommon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The argument getters' gas tests copy a positive argument whose top bit is clear, which the signed
// and unsigned decodings read as the same number. This one has its top bit set, so each getter must
// decode it its own way, on both sides of the fork: 0xFF01 is 65281 unsigned and -255 in two's
// complement
func TestVMHooksImpl_ArgumentGetters_DecodeSignedAndUnsigned(t *testing.T) {
	t.Parallel()

	const destination = int32(42)
	arg := []byte{0xFF, 0x01}
	unsigned, signed := big.NewInt(65281), big.NewInt(-255)

	getters := []struct {
		name     string
		expected *big.Int
		call     func(t *testing.T, hooks *vmhooks.VMHooksImpl) *big.Int
	}{
		{"BigIntGetUnsignedArgument", unsigned, func(t *testing.T, hooks *vmhooks.VMHooksImpl) *big.Int {
			hooks.BigIntGetUnsignedArgument(0, destination)
			value, err := hooks.GetManagedTypesContext().GetBigInt(destination)
			require.NoError(t, err)
			return value
		}},
		{"BigIntGetSignedArgument", signed, func(t *testing.T, hooks *vmhooks.VMHooksImpl) *big.Int {
			hooks.BigIntGetSignedArgument(0, destination)
			value, err := hooks.GetManagedTypesContext().GetBigInt(destination)
			require.NoError(t, err)
			return value
		}},
		{"SmallIntGetUnsignedArgument", unsigned, func(t *testing.T, hooks *vmhooks.VMHooksImpl) *big.Int {
			return big.NewInt(hooks.SmallIntGetUnsignedArgument(0))
		}},
		{"SmallIntGetSignedArgument", signed, func(t *testing.T, hooks *vmhooks.VMHooksImpl) *big.Int {
			return big.NewInt(hooks.SmallIntGetSignedArgument(0))
		}},
	}

	for _, getter := range getters {
		for _, fixAuditChangesV6 := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/FixAuditChangesV6=%v", getter.name, fixAuditChangesV6), func(t *testing.T) {
				t.Parallel()

				host, _ := newMockVMHostWithRealMetering(t, fixAuditChangesV6, argumentGetterGasMap(), 100_000_000)
				runtime := host.RuntimeContext.(*contextmock.RuntimeContextMock)
				runtime.VMInput = &vmcommon.ContractCallInput{VMInput: vmcommon.VMInput{Arguments: [][]byte{arg}}}

				value := getter.call(t, vmhooks.NewVMHooksImpl(host))

				require.Nil(t, runtime.FailExecutionErr)
				assert.Equal(t, getter.expected, value)
			})
		}
	}
}
