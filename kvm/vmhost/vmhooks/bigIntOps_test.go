package vmhooks_test

import (
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"strconv"
	"testing"

	commonMock "github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/core/process/kda/kdautils"
	"github.com/klever-io/klever-go/kvm/executor"
	twos "github.com/klever-io/klever-go/kvm/math/twos-complement"
	mock "github.com/klever-io/klever-go/kvm/mock/context"
	worldmock "github.com/klever-io/klever-go/kvm/mock/world"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/kvm/vmhost/hostCore"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/klever-io/klever-go/vmcommon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBigIntNew(t *testing.T) {
	tests := []struct {
		name     string
		input    int64
		expected *big.Int
	}{
		{
			name:     "positive number",
			input:    42,
			expected: big.NewInt(42),
		},
		{
			name:     "negative number",
			input:    -42,
			expected: big.NewInt(-42),
		},
		{
			name:     "zero",
			input:    0,
			expected: big.NewInt(0),
		},
		{
			name:     "max int64",
			input:    9223372036854775807,
			expected: big.NewInt(9223372036854775807),
		},
		{
			name:     "min int64",
			input:    -9223372036854775808,
			expected: big.NewInt(-9223372036854775808),
		},
	}

	mockWorld := worldmock.NewMockWorld()
	vmHost, _ := hostCore.NewVMHost(mockWorld, makeHostParameters())
	hooks := vmhooks.NewVMHooksImpl(vmHost)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handle := hooks.BigIntNew(tt.input)
			result, _ := vmHost.ManagedTypes().GetBigInt(handle)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBigIntArithmetic(t *testing.T) {
	tests := []struct {
		name      string
		op1       int64
		op2       int64
		expected  *big.Int
		operation func(*vmhooks.VMHooksImpl, int32, int32, int32)
	}{
		{
			name:     "add positive numbers",
			op1:      100,
			op2:      200,
			expected: big.NewInt(300),
			operation: func(h *vmhooks.VMHooksImpl, dest, a, b int32) {
				h.BigIntAdd(dest, a, b)
			},
		},
		{
			name:     "add negative and positive",
			op1:      -100,
			op2:      200,
			expected: big.NewInt(100),
			operation: func(h *vmhooks.VMHooksImpl, dest, a, b int32) {
				h.BigIntAdd(dest, a, b)
			},
		},
		{
			name:     "subtract positive numbers",
			op1:      200,
			op2:      100,
			expected: big.NewInt(100),
			operation: func(h *vmhooks.VMHooksImpl, dest, a, b int32) {
				h.BigIntSub(dest, a, b)
			},
		},
		{
			name:     "subtract to negative",
			op1:      100,
			op2:      200,
			expected: big.NewInt(-100),
			operation: func(h *vmhooks.VMHooksImpl, dest, a, b int32) {
				h.BigIntSub(dest, a, b)
			},
		},
		{
			name:     "multiply positive numbers",
			op1:      100,
			op2:      200,
			expected: big.NewInt(20000),
			operation: func(h *vmhooks.VMHooksImpl, dest, a, b int32) {
				h.BigIntMul(dest, a, b)
			},
		},
		{
			name:     "multiply by zero",
			op1:      100,
			op2:      0,
			expected: big.NewInt(0),
			operation: func(h *vmhooks.VMHooksImpl, dest, a, b int32) {
				h.BigIntMul(dest, a, b)
			},
		},
	}

	mockWorld := worldmock.NewMockWorld()
	vmHost, _ := hostCore.NewVMHost(mockWorld, makeHostParameters())
	hooks := vmhooks.NewVMHooksImpl(vmHost)
	// the hook's fixed cost is bounded from FixAuditChangesV6, so it needs a budget to charge against
	vmHost.Metering().InitStateFromContractCallInput(&vmcommon.VMInput{GasProvided: 10_000})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handle1 := hooks.BigIntNew(tt.op1)
			handle2 := hooks.BigIntNew(tt.op2)
			destHandle := hooks.BigIntNew(0)
			tt.operation(hooks, destHandle, handle1, handle2)
			result, _ := vmHost.ManagedTypes().GetBigInt(destHandle)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBigIntDivision(t *testing.T) {
	tests := []struct {
		name        string
		op1         int64
		op2         int64
		expected    *big.Int
		expectError bool
	}{
		{
			name:        "normal division",
			op1:         100,
			op2:         20,
			expected:    big.NewInt(5),
			expectError: false,
		},
		{
			name:        "division with negative dividend",
			op1:         -100,
			op2:         20,
			expected:    big.NewInt(-5),
			expectError: false,
		},
		{
			name:        "division by one",
			op1:         100,
			op2:         1,
			expected:    big.NewInt(100),
			expectError: false,
		},
		{
			name:        "zero dividend",
			op1:         0,
			op2:         20,
			expected:    big.NewInt(0),
			expectError: false,
		},
		{
			name:        "division by zero",
			op1:         100,
			op2:         0,
			expected:    nil,
			expectError: true,
		},
	}

	mockWorld := worldmock.NewMockWorld()
	vmHost, _ := hostCore.NewVMHost(mockWorld, makeHostParameters())
	hooks := vmhooks.NewVMHooksImpl(vmHost)
	// the hook's fixed cost is bounded from FixAuditChangesV6, so it needs a budget to charge against
	vmHost.Metering().InitStateFromContractCallInput(&vmcommon.VMInput{GasProvided: 10_000})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handle1 := hooks.BigIntNew(tt.op1)
			handle2 := hooks.BigIntNew(tt.op2)
			destHandle := hooks.BigIntNew(0)

			hooks.BigIntTDiv(destHandle, handle1, handle2)

			if tt.expectError {
				assert.Equal(t, vmcommon.VMExecutionFailed, vmHost.Output().ReturnCode())
				assert.Equal(t, "division by 0", vmHost.Output().ReturnMessage())
				return
			}

			result, _ := vmHost.ManagedTypes().GetBigInt(destHandle)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBigIntBitwise(t *testing.T) {
	tests := []struct {
		name        string
		input       int64
		bits        int32
		expected    *big.Int
		expectError bool
	}{
		{
			name:        "shift right by 2",
			input:       100,
			bits:        2,
			expected:    big.NewInt(25),
			expectError: false,
		},
		{
			name:        "shift right by zero",
			input:       100,
			bits:        0,
			expected:    big.NewInt(100),
			expectError: false,
		},
		{
			name:        "shift right negative number",
			input:       -100,
			bits:        2,
			expected:    nil,
			expectError: true,
		},
		{
			name:        "shift right by negative bits",
			input:       100,
			bits:        -2,
			expected:    nil,
			expectError: true,
		},
	}

	mockWorld := worldmock.NewMockWorld()
	vmHost, _ := hostCore.NewVMHost(mockWorld, makeHostParameters())
	hooks := vmhooks.NewVMHooksImpl(vmHost)
	// the hook's fixed cost is bounded from FixAuditChangesV6, so it needs a budget to charge against
	vmHost.Metering().InitStateFromContractCallInput(&vmcommon.VMInput{GasProvided: 10_000})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handle := hooks.BigIntNew(tt.input)
			destHandle := hooks.BigIntNew(0)

			hooks.BigIntShr(destHandle, handle, tt.bits)

			if tt.expectError {
				assert.Equal(t, vmcommon.VMExecutionFailed, vmHost.Output().ReturnCode())
				assert.Equal(t, "bitwise shift operations only allowed on positive integers and by a positive amount", vmHost.Output().ReturnMessage())
				return
			}

			result, _ := vmHost.ManagedTypes().GetBigInt(destHandle)
			assert.Equal(t, tt.expected, result)
		})
	}
}

type InstanceTracker interface {
	ReplaceInstance(instance executor.Instance)
}

func TestBigIntSetUnsignedBytes(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected *big.Int
	}{
		{
			name:     "normal bytes",
			input:    []byte{0x01, 0x02, 0x03},
			expected: new(big.Int).SetBytes([]byte{0x01, 0x02, 0x03}),
		},
		{
			name:     "empty bytes",
			input:    []byte{},
			expected: big.NewInt(0),
		},
		{
			name:     "bytes with leading zeros",
			input:    []byte{0x00, 0x00, 0x01},
			expected: big.NewInt(1),
		},
		{
			name:     "single zero byte",
			input:    []byte{0x00},
			expected: big.NewInt(0),
		},
	}

	mockWorld := worldmock.NewMockWorld()
	vmHost, _ := hostCore.NewVMHost(mockWorld, makeHostParameters())
	hooks := vmhooks.NewVMHooksImpl(vmHost)
	// the hook's fixed cost is bounded from FixAuditChangesV6, so it needs a budget to charge against
	vmHost.Metering().InitStateFromContractCallInput(&vmcommon.VMInput{GasProvided: 10_000})

	// set new instance
	it, ok := vmHost.Runtime().GetInstanceTracker().(InstanceTracker)
	require.True(t, ok)
	it.ReplaceInstance(mock.NewInstanceMock(nil))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			offset := 0
			destHandle := hooks.BigIntNew(0)
			err := hooks.MemStore(executor.MemPtr(offset), tt.input)
			require.NoError(t, err)
			hooks.BigIntSetUnsignedBytes(destHandle, executor.MemPtr(offset), executor.MemLength(len(tt.input)))
			result, _ := vmHost.ManagedTypes().GetBigInt(destHandle)
			assert.Equal(t, 0, result.Cmp(tt.expected))
		})
	}
}

func TestBigIntGetUnsignedBytes(t *testing.T) {
	tests := []struct {
		name          string
		input         int64
		expectedBytes []byte
	}{
		{
			name:          "positive number",
			input:         257,
			expectedBytes: []byte{0x01, 0x01},
		},
		{
			name:          "zero",
			input:         0,
			expectedBytes: []byte{},
		},
		{
			name:          "negative number",
			input:         -257,
			expectedBytes: []byte{0x01, 0x01}, // should be treated as positive
		},
		{
			name:          "large number",
			input:         1<<32 - 1,
			expectedBytes: []byte{0xff, 0xff, 0xff, 0xff},
		},
	}

	mockWorld := worldmock.NewMockWorld()
	vmHost, _ := hostCore.NewVMHost(mockWorld, makeHostParameters())
	hooks := vmhooks.NewVMHooksImpl(vmHost)
	// the hook's fixed cost is bounded from FixAuditChangesV6, so it needs a budget to charge against
	vmHost.Metering().InitStateFromContractCallInput(&vmcommon.VMInput{GasProvided: 10_000})

	// set new instance
	it, ok := vmHost.Runtime().GetInstanceTracker().(InstanceTracker)
	require.True(t, ok)
	it.ReplaceInstance(mock.NewInstanceMock(nil))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handle := hooks.BigIntNew(tt.input)

			length := hooks.BigIntGetUnsignedBytes(handle, executor.MemPtr(0))

			result, err := hooks.MemLoad(executor.MemPtr(0), length)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedBytes, result)
		})
	}
}

func TestBigIntGetSignedBytes(t *testing.T) {
	tests := []struct {
		name          string
		input         int64
		expectedBytes []byte
	}{
		{
			name:          "positive small number",
			input:         127,
			expectedBytes: []byte{0x7f},
		},
		{
			name:          "negative small number",
			input:         -128,
			expectedBytes: []byte{0x80},
		},
		{
			name:          "zero",
			input:         0,
			expectedBytes: []byte{},
		},
		{
			name:          "positive number requiring two bytes",
			input:         128,
			expectedBytes: []byte{0x00, 0x80},
		},
		{
			name:          "negative number requiring two bytes",
			input:         -129,
			expectedBytes: []byte{0xff, 0x7f},
		},
	}

	mockWorld := worldmock.NewMockWorld()
	vmHost, _ := hostCore.NewVMHost(mockWorld, makeHostParameters())
	hooks := vmhooks.NewVMHooksImpl(vmHost)
	// the hook's fixed cost is bounded from FixAuditChangesV6, so it needs a budget to charge against
	vmHost.Metering().InitStateFromContractCallInput(&vmcommon.VMInput{GasProvided: 10_000})

	// set new instance
	it, ok := vmHost.Runtime().GetInstanceTracker().(InstanceTracker)
	require.True(t, ok)
	it.ReplaceInstance(mock.NewInstanceMock(nil))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handle := hooks.BigIntNew(tt.input)
			length := hooks.BigIntGetSignedBytes(handle, executor.MemPtr(0))

			result, err := hooks.MemLoad(executor.MemPtr(0), length)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedBytes, result)
		})
	}
}

func TestBigIntGetCallValue(t *testing.T) {
	cases := []struct {
		title        string
		kdaTransfers []*vmcommon.KDATransfer
	}{
		{
			title:        "With 10 transfers",
			kdaTransfers: generateTransfersSlice(9),
		},
		{
			title:        "With 20 transfers",
			kdaTransfers: generateTransfersSlice(19),
		},
		{
			title:        "With 30 transfers",
			kdaTransfers: generateTransfersSlice(29),
		},
		{
			title:        "With 40 transfers",
			kdaTransfers: generateTransfersSlice(39),
		},
		{
			title:        "With 50 transfers",
			kdaTransfers: generateTransfersSlice(49),
		},
	}

	t.Run("Sucessful", func(t *testing.T) {
		for _, tt := range cases {
			t.Run(tt.title, func(t *testing.T) {
				mockWorld := worldmock.NewMockWorld()
				vmHost, _ := hostCore.NewVMHost(mockWorld, makeHostParameters())
				hooks := vmhooks.NewVMHooksImpl(vmHost)

				// Set mock instance
				it, ok := vmHost.Runtime().GetInstanceTracker().(InstanceTracker)
				require.True(t, ok)
				it.ReplaceInstance(mock.NewInstanceMock(nil))

				destHandle := int32(1)

				// As it does not exist, it will be created
				initialValue := hooks.GetManagedTypesContext().GetBigIntOrCreate(destHandle)
				assert.Equal(t, initialValue, big.NewInt(0))

				expected := big.NewInt(100)
				tt.kdaTransfers = append(tt.kdaTransfers, &vmcommon.KDATransfer{
					KDATokenName: kdautils.KLVIdentifier, // BigIntGetCallValue hook only retrieves KLV
					KDAValue:     expected,
				})

				shuffleTransferSlice(tt.kdaTransfers)
				hooks.GetRuntimeContext().SetVMInput(&vmcommon.ContractCallInput{
					VMInput: vmcommon.VMInput{
						KDATransfers: tt.kdaTransfers,
					},
				})

				// Invoking test target hook who will set *big.Int value on destHandle
				hooks.BigIntGetCallValue(destHandle)

				// As it now exists the value retrieved must be non-zero
				result := hooks.GetManagedTypesContext().GetBigIntOrCreate(destHandle)
				assert.Equal(t, result, expected)

				// Calculate gas used
				loopGas := hooks.GetMeteringContext().GasSchedule().WASMOpcodeCost.Loop
				callValueGas := hooks.GetMeteringContext().
					GasSchedule().
					BigIntAPICost.BigIntGetCallValue
				totalGas := (uint64(loopGas) * uint64(len(tt.kdaTransfers))) + callValueGas

				assert.Equal(t, hooks.GetRuntimeContext().GetPointsUsed(), totalGas)
			})
		}

		t.Run("With only one KLV transfer", func(t *testing.T) {
			mockWorld := worldmock.NewMockWorld()
			vmHost, _ := hostCore.NewVMHost(mockWorld, makeHostParameters())
			hooks := vmhooks.NewVMHooksImpl(vmHost)

			// Set mock instance
			it, ok := vmHost.Runtime().GetInstanceTracker().(InstanceTracker)
			require.True(t, ok)
			it.ReplaceInstance(mock.NewInstanceMock(nil))

			destHandle := int32(1)

			// As it does not exist, it will be created
			initialValue := hooks.GetManagedTypesContext().GetBigIntOrCreate(destHandle)
			assert.Equal(t, initialValue, big.NewInt(0))

			expected := big.NewInt(100)
			kdaTransfers := []*vmcommon.KDATransfer{
				{
					KDATokenName: kdautils.KLVIdentifier, // BigIntGetCallValue hook only retrieves KLV
					KDAValue:     expected,
				},
			}
			hooks.GetRuntimeContext().SetVMInput(&vmcommon.ContractCallInput{
				VMInput: vmcommon.VMInput{
					KDATransfers: kdaTransfers,
				},
			})

			// Invoking test target hook who will set *big.Int value on destHandle
			hooks.BigIntGetCallValue(destHandle)

			// As it now exists the value retrieved must be non-zero
			result := hooks.GetManagedTypesContext().GetBigIntOrCreate(destHandle)
			assert.Equal(t, result, expected)

			// Calculate gas used
			loopGas := hooks.GetMeteringContext().GasSchedule().WASMOpcodeCost.Loop
			callValueGas := hooks.GetMeteringContext().
				GasSchedule().
				BigIntAPICost.BigIntGetCallValue
			totalGas := (uint64(loopGas) * uint64(len(kdaTransfers))) + callValueGas

			assert.Equal(t, hooks.GetRuntimeContext().GetPointsUsed(), totalGas)
		})
	})

	casesNoKLV := []struct {
		title        string
		kdaTransfers []*vmcommon.KDATransfer
	}{
		{
			title:        "With 10 transfers",
			kdaTransfers: generateTransfersSlice(10),
		},
		{
			title:        "With 20 transfers",
			kdaTransfers: generateTransfersSlice(20),
		},
		{
			title:        "With 30 transfers",
			kdaTransfers: generateTransfersSlice(30),
		},
		{
			title:        "With 40 transfers",
			kdaTransfers: generateTransfersSlice(40),
		},
		{
			title:        "With 50 transfers",
			kdaTransfers: generateTransfersSlice(50),
		},
	}

	t.Run("With no KLV call value", func(t *testing.T) {
		for _, tt := range casesNoKLV {
			t.Run(tt.title, func(t *testing.T) {
				mockWorld := worldmock.NewMockWorld()
				vmHost, _ := hostCore.NewVMHost(mockWorld, makeHostParameters())
				hooks := vmhooks.NewVMHooksImpl(vmHost)

				// Set mock instance
				it, ok := vmHost.Runtime().GetInstanceTracker().(InstanceTracker)
				require.True(t, ok)
				it.ReplaceInstance(mock.NewInstanceMock(nil))

				destHandle := int32(1)
				// As it does not exist, it will be created
				initialValue := hooks.GetManagedTypesContext().GetBigIntOrCreate(destHandle)
				assert.Equal(t, initialValue, big.NewInt(0))

				shuffleTransferSlice(tt.kdaTransfers)
				hooks.GetRuntimeContext().SetVMInput(&vmcommon.ContractCallInput{
					VMInput: vmcommon.VMInput{
						KDATransfers: tt.kdaTransfers,
					},
				})

				// Invoking test target hook who will set *big.Int value on destHandle
				hooks.BigIntGetCallValue(destHandle)

				// Invoking test target hook with empty transfers slice, so no value will be set
				result := hooks.GetManagedTypesContext().GetBigIntOrCreate(destHandle)
				assert.Equal(t, result, big.NewInt(0))

				// Calculate gas used
				loopGas := hooks.GetMeteringContext().GasSchedule().WASMOpcodeCost.Loop
				callValueGas := hooks.GetMeteringContext().
					GasSchedule().
					BigIntAPICost.BigIntGetCallValue
				totalGas := (uint64(loopGas) * uint64(len(tt.kdaTransfers))) + callValueGas

				assert.Equal(t, hooks.GetRuntimeContext().GetPointsUsed(), totalGas)
			})
		}

		t.Run("only one asset transfer", func(t *testing.T) {
			mockWorld := worldmock.NewMockWorld()
			vmHost, _ := hostCore.NewVMHost(mockWorld, makeHostParameters())
			hooks := vmhooks.NewVMHooksImpl(vmHost)

			// Set mock instance
			it, ok := vmHost.Runtime().GetInstanceTracker().(InstanceTracker)
			require.True(t, ok)
			it.ReplaceInstance(mock.NewInstanceMock(nil))

			destHandle := int32(1)

			// As it does not exist, it will be created
			initialValue := hooks.GetManagedTypesContext().GetBigIntOrCreate(destHandle)
			assert.Equal(t, initialValue, big.NewInt(0))

			kdaTransfers := []*vmcommon.KDATransfer{
				{
					KDATokenName: []byte("TEST-H7K7"),
					KDAValue:     big.NewInt(100),
				},
			}
			hooks.GetRuntimeContext().SetVMInput(&vmcommon.ContractCallInput{
				VMInput: vmcommon.VMInput{
					KDATransfers: kdaTransfers,
				},
			})

			// Invoking test target hook who will set *big.Int value on destHandle
			hooks.BigIntGetCallValue(destHandle)

			// As there is no transfer of klv the value must be zero
			result := hooks.GetManagedTypesContext().GetBigIntOrCreate(destHandle)
			assert.Equal(t, result, big.NewInt(0))

			// Calculate gas used
			loopGas := hooks.GetMeteringContext().GasSchedule().WASMOpcodeCost.Loop
			callValueGas := hooks.GetMeteringContext().
				GasSchedule().
				BigIntAPICost.BigIntGetCallValue
			totalGas := (uint64(loopGas) * uint64(len(kdaTransfers))) + callValueGas

			assert.Equal(t, hooks.GetRuntimeContext().GetPointsUsed(), totalGas)
		})
	})

	t.Run("with empty call values", func(t *testing.T) {
		mockWorld := worldmock.NewMockWorld()
		vmHost, _ := hostCore.NewVMHost(mockWorld, makeHostParameters())
		hooks := vmhooks.NewVMHooksImpl(vmHost)

		// Set mock instance
		it, ok := vmHost.Runtime().GetInstanceTracker().(InstanceTracker)
		require.True(t, ok)
		it.ReplaceInstance(mock.NewInstanceMock(nil))

		destHandle := int32(1)

		// As it does not exist, it will be created
		initialValue := hooks.GetManagedTypesContext().GetBigIntOrCreate(destHandle)
		assert.Equal(t, initialValue, big.NewInt(0))

		hooks.GetRuntimeContext().SetVMInput(&vmcommon.ContractCallInput{
			VMInput: vmcommon.VMInput{
				KDATransfers: []*vmcommon.KDATransfer{},
			},
		})

		hooks.BigIntGetCallValue(destHandle)

		// As there is no transfer of klv the value must be zero
		result := hooks.GetManagedTypesContext().GetBigIntOrCreate(destHandle)
		assert.Equal(t, result, big.NewInt(0))

		// Calculate gas used
		loopGas := hooks.GetMeteringContext().GasSchedule().WASMOpcodeCost.Loop
		callValueGas := hooks.GetMeteringContext().
			GasSchedule().
			BigIntAPICost.BigIntGetCallValue
		totalGas := (uint64(loopGas) * uint64(len(
			hooks.GetRuntimeContext().GetVMInput().KDATransfers,
		))) + callValueGas

		assert.Equal(t, hooks.GetRuntimeContext().GetPointsUsed(), totalGas)
	})
}

func TestBigIntPow(t *testing.T) {
	t.Run("positive base and exponent", func(t *testing.T) {
		mockWorld := worldmock.NewMockWorld()
		vmHost, err := hostCore.NewVMHost(mockWorld, makeHostParameters())
		require.NoError(t, err)
		hooks := vmhooks.NewVMHooksImpl(vmHost)
		provideGas(hooks, 1_000_000)

		baseHandle := hooks.BigIntNew(2)
		expHandle := hooks.BigIntNew(10)
		destHandle := hooks.BigIntNew(0)

		hooks.BigIntPow(destHandle, baseHandle, expHandle)

		result, err := vmHost.ManagedTypes().GetBigInt(destHandle)
		require.NoError(t, err)
		require.Equal(t, 0, big.NewInt(1024).Cmp(result), "2^10 should be 1024")
	})

	t.Run("zero base short-circuits identically across fork", func(t *testing.T) {
		cases := []struct {
			exponent int64
			expected int64
		}{
			{exponent: 0, expected: 1},
			{exponent: 1, expected: 0},
			{exponent: 64, expected: 0},
		}
		for _, fixAuditV3 := range []bool{false, true} {
			fixAuditV3 := fixAuditV3
			t.Run(fmt.Sprintf("FixAuditChangesV3=%t", fixAuditV3), func(t *testing.T) {
				for _, tc := range cases {
					tc := tc
					t.Run(strconv.FormatInt(tc.exponent, 10), func(t *testing.T) {
						mockWorld := worldmock.NewMockWorld()
						vmHost, err := hostCore.NewVMHost(mockWorld, makeHostParametersWithFork(newForkStub(fixAuditV3)))
						require.NoError(t, err)
						hooks := vmhooks.NewVMHooksImpl(vmHost)
						// only the fixed cost, bounded from FixAuditChangesV6: proves no result-size charge is made
						provideGas(hooks, vmHost.Metering().GasSchedule().BigIntAPICost.BigIntPow)

						baseHandle := hooks.BigIntNew(0)
						expHandle := hooks.BigIntNew(tc.exponent)
						destHandle := hooks.BigIntNew(7) // sentinel; overwritten with the result

						hooks.BigIntPow(destHandle, baseHandle, expHandle)

						result, err := vmHost.ManagedTypes().GetBigInt(destHandle)
						require.NoError(t, err, "zero base must not fault")
						require.Equal(t, 0, big.NewInt(tc.expected).Cmp(result), "0^%d should be %d", tc.exponent, tc.expected)
					})
				}
			})
		}
	})
	t.Run("non-zero base result unchanged across fork", func(t *testing.T) {
		for _, fixAuditV3 := range []bool{false, true} {
			fixAuditV3 := fixAuditV3
			t.Run(fmt.Sprintf("FixAuditChangesV3=%t", fixAuditV3), func(t *testing.T) {
				mockWorld := worldmock.NewMockWorld()
				vmHost, err := hostCore.NewVMHost(mockWorld, makeHostParametersWithFork(newForkStub(fixAuditV3)))
				require.NoError(t, err)
				hooks := vmhooks.NewVMHooksImpl(vmHost)
				provideGas(hooks, 1_000_000)

				baseHandle := hooks.BigIntNew(2)
				expHandle := hooks.BigIntNew(10)
				destHandle := hooks.BigIntNew(0)

				hooks.BigIntPow(destHandle, baseHandle, expHandle)

				result, err := vmHost.ManagedTypes().GetBigInt(destHandle)
				require.NoError(t, err)
				require.Equal(t, 0, big.NewInt(1024).Cmp(result), "2^10 should be 1024 regardless of fork")
			})
		}
	})

	t.Run("non-zero base charges positive gas", func(t *testing.T) {
		// base 2 (byteLen == 2 bits) and exponent 64 give a result-size charge of
		// 64*2/8 == 16 gas, a whole, strictly-positive number of gas units.
		const base = 2
		const exponent = 64
		const expectedGasCharge = uint64(exponent) * 2 / 8 // 16 gas

		t.Run("faults when gas is one unit short", func(t *testing.T) {
			mockWorld := worldmock.NewMockWorld()
			vmHost, err := hostCore.NewVMHost(mockWorld, makeHostParameters())
			require.NoError(t, err)
			hooks := vmhooks.NewVMHooksImpl(vmHost)
			// One unit short of the positive result-size charge.
			provideGas(hooks, expectedGasCharge-1)

			baseHandle := hooks.BigIntNew(base)
			expHandle := hooks.BigIntNew(exponent)
			destHandle := hooks.BigIntNew(7) // sentinel; must stay unchanged on fault

			hooks.BigIntPow(destHandle, baseHandle, expHandle)

			result, err := vmHost.ManagedTypes().GetBigInt(destHandle)
			require.NoError(t, err)
			require.Equal(t, 0, big.NewInt(7).Cmp(result), "a positive gas charge must exhaust the one-unit-short budget and leave the destination untouched")
		})

		t.Run("runs to completion with sufficient gas", func(t *testing.T) {
			mockWorld := worldmock.NewMockWorld()
			vmHost, err := hostCore.NewVMHost(mockWorld, makeHostParameters())
			require.NoError(t, err)
			hooks := vmhooks.NewVMHooksImpl(vmHost)
			provideGas(hooks, 1_000_000)

			baseHandle := hooks.BigIntNew(base)
			expHandle := hooks.BigIntNew(exponent)
			destHandle := hooks.BigIntNew(7) // sentinel; overwritten with the result

			hooks.BigIntPow(destHandle, baseHandle, expHandle)

			result, err := vmHost.ManagedTypes().GetBigInt(destHandle)
			require.NoError(t, err)
			expected := new(big.Int).Exp(big.NewInt(base), big.NewInt(exponent), nil)
			require.Equal(t, 0, expected.Cmp(result), "2^64 should be %s", expected.String())
		})
	})

	// Regression: the zero-base short-circuit must charge the exponent copy gas
	// just like the general path. For an exponent whose byte length exceeds the
	// "too big" threshold (32 bytes / 256 bits) that charge is strictly positive,
	// so skipping it (as an un-gated short-circuit did) would silently lower
	// gas-used for 0^n and diverge from historical replay.
	t.Run("zero base charges exponent copy gas for a large exponent", func(t *testing.T) {
		powGasUsed := func(exp *big.Int) uint64 {
			mockWorld := worldmock.NewMockWorld()
			vmHost, err := hostCore.NewVMHost(mockWorld, makeHostParameters())
			require.NoError(t, err)
			hooks := vmhooks.NewVMHooksImpl(vmHost)

			// a mock instance is required for GetPointsUsed to track consumed gas
			it, ok := vmHost.Runtime().GetInstanceTracker().(InstanceTracker)
			require.True(t, ok)
			it.ReplaceInstance(mock.NewInstanceMock(nil))

			provideGas(hooks, 100_000_000)

			baseHandle := hooks.BigIntNew(0)
			expHandle := hooks.GetManagedTypesContext().NewBigInt(exp)
			destHandle := hooks.BigIntNew(7)

			hooks.BigIntPow(destHandle, baseHandle, expHandle)

			result, err := vmHost.ManagedTypes().GetBigInt(destHandle)
			require.NoError(t, err)
			require.Equal(t, 0, big.NewInt(0).Cmp(result), "0^n should be 0")

			return hooks.GetRuntimeContext().GetPointsUsed()
		}

		schedHost, err := hostCore.NewVMHost(worldmock.NewMockWorld(), makeHostParameters())
		require.NoError(t, err)
		copyPerByte := schedHost.Metering().GasSchedule().BigIntAPICost.CopyPerByteForTooBig
		require.NotZero(t, copyPerByte, "test gas schedule must charge for oversized copies")

		hugeExp := new(big.Int).Lsh(big.NewInt(1), 300) // 2^300 -> 38 bytes, over the 32-byte threshold
		smallExp := big.NewInt(64)                      // 1 byte, under the threshold (no copy charge)

		byteLen := uint64((hugeExp.BitLen() + 7) / 8) // 38
		expectedDelta := byteLen * copyPerByte

		require.Equal(t, expectedDelta, powGasUsed(hugeExp)-powGasUsed(smallExp),
			"zero base must charge exponent copy gas for a large exponent")
	})

	// The fault error becomes the on-chain returnMessage (receipts). Pre-fork a
	// negative exponent first flows through the result-size charge, so a large
	// |exponent| faults with "not enough gas"; post-fork the bounds check runs
	// before any gas math and always faults with ErrBadLowerBounds.
	t.Run("negative exponent fault message is fork-gated", func(t *testing.T) {
		returnMessageFor := func(fixAuditV3 bool, exponent int64) string {
			mockWorld := worldmock.NewMockWorld()
			vmHost, err := hostCore.NewVMHost(mockWorld, makeHostParametersWithFork(newForkStub(fixAuditV3)))
			require.NoError(t, err)
			hooks := vmhooks.NewVMHooksImpl(vmHost)
			provideGas(hooks, 1_000_000)

			baseHandle := hooks.BigIntNew(2)
			expHandle := hooks.BigIntNew(exponent)
			destHandle := hooks.BigIntNew(7)

			hooks.BigIntPow(destHandle, baseHandle, expHandle)

			return vmHost.Output().ReturnMessage()
		}

		const smallNegative = int64(-1)
		const hugeNegative = int64(-1) << 40 // result-size charge far above the gas budget

		t.Run("pre-fork keeps the legacy gas-first order", func(t *testing.T) {
			require.Equal(t, vmhost.ErrBadLowerBounds.Error(), returnMessageFor(false, smallNegative))
			require.Equal(t, vmhost.ErrNotEnoughGas.Error(), returnMessageFor(false, hugeNegative))
		})

		t.Run("post-fork always reports bad lower bounds", func(t *testing.T) {
			require.Equal(t, vmhost.ErrBadLowerBounds.Error(), returnMessageFor(true, smallNegative))
			require.Equal(t, vmhost.ErrBadLowerBounds.Error(), returnMessageFor(true, hugeNegative))
		})

		// from FixAuditChangesV6 the operand copy is bounded and can fault, so it must stay after the
		// bounds check: a negative exponent over 32 bytes with only the fixed cost left still reports
		// bad lower bounds, not not-enough-gas
		t.Run("post-fork bounds check precedes the bounded copy charge", func(t *testing.T) {
			forkController := newForkStub(true)
			forkController.FixAuditChangesV6Value = true
			vmHost, err := hostCore.NewVMHost(worldmock.NewMockWorld(), makeHostParametersWithFork(forkController))
			require.NoError(t, err)
			hooks := vmhooks.NewVMHooksImpl(vmHost)
			provideGas(hooks, vmHost.Metering().GasSchedule().BigIntAPICost.BigIntPow)

			negative := new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 320)) // 41 bytes
			baseHandle := hooks.BigIntNew(2)
			expHandle := hooks.GetManagedTypesContext().NewBigInt(negative)
			destHandle := hooks.BigIntNew(7)

			hooks.BigIntPow(destHandle, baseHandle, expHandle)

			require.Equal(t, vmhost.ErrBadLowerBounds.Error(), vmHost.Output().ReturnMessage())
		})
	})
}

func TestBigIntShl(t *testing.T) {
	newHooks := func(t *testing.T, fixAuditV6 bool, gas uint64) (vmhost.VMHost, *vmhooks.VMHooksImpl) {
		forkController := commonMock.NewForkControllerStub()
		forkController.FixAuditChangesV6Value = fixAuditV6
		vmHost, err := hostCore.NewVMHost(worldmock.NewMockWorld(), makeHostParametersWithFork(forkController))
		require.NoError(t, err)
		hooks := vmhooks.NewVMHooksImpl(vmHost)

		it, ok := vmHost.Runtime().GetInstanceTracker().(InstanceTracker)
		require.True(t, ok)
		it.ReplaceInstance(mock.NewInstanceMock(nil))

		provideGas(hooks, gas)
		return vmHost, hooks
	}

	runShl := func(t *testing.T, fixAuditV6 bool, gas uint64, value *big.Int, bits int32) (vmhost.VMHost, *vmhooks.VMHooksImpl, int32) {
		vmHost, hooks := newHooks(t, fixAuditV6, gas)

		sourceHandle := hooks.GetManagedTypesContext().NewBigInt(value)
		destHandle := hooks.BigIntNew(7)

		hooks.BigIntShl(destHandle, sourceHandle, bits)

		return vmHost, hooks, destHandle
	}

	requireDestination := func(t *testing.T, vmHost vmhost.VMHost, destHandle int32, expected *big.Int) {
		result, err := vmHost.ManagedTypes().GetBigInt(destHandle)
		require.NoError(t, err)
		require.Equal(t, 0, expected.Cmp(result), "expected %s, got %s", expected, result)
	}

	shlGasUsed := func(t *testing.T, fixAuditV6 bool, value *big.Int, bits int32) uint64 {
		vmHost, hooks, destHandle := runShl(t, fixAuditV6, 100_000_000, value, bits)

		requireDestination(t, vmHost, destHandle, new(big.Int).Lsh(value, uint(bits)))

		return hooks.GetRuntimeContext().GetPointsUsed()
	}

	t.Run("result size gas is unchanged across fork", func(t *testing.T) {
		cases := []struct {
			value *big.Int
			bits  int32
		}{
			{value: big.NewInt(5), bits: 0},
			{value: big.NewInt(3), bits: 4},
			{value: big.NewInt(1), bits: 255},
			{value: big.NewInt(1), bits: 256},
			{value: big.NewInt(0xFF), bits: 249},
			{value: big.NewInt(1), bits: 1000},
			{value: big.NewInt(0), bits: 1000},
			{value: new(big.Int).Lsh(big.NewInt(1), 400), bits: 8},
		}
		for _, tc := range cases {
			require.Equal(t, shlGasUsed(t, false, tc.value, tc.bits), shlGasUsed(t, true, tc.value, tc.bits),
				"%s << %d", tc.value, tc.bits)
		}
	})

	t.Run("charges the result size above the normal cost threshold", func(t *testing.T) {
		vmHost, _ := newHooks(t, true, 0)
		copyPerByte := vmHost.Metering().GasSchedule().BigIntAPICost.CopyPerByteForTooBig
		require.NotZero(t, copyPerByte)

		resultByteLen := uint64((1 + 1000 + 7) / 8)
		require.Equal(t, resultByteLen*copyPerByte, shlGasUsed(t, true, big.NewInt(1), 1000)-shlGasUsed(t, true, big.NewInt(1), 4))
	})

	t.Run("zero value charges no result size gas", func(t *testing.T) {
		require.Equal(t, shlGasUsed(t, true, big.NewInt(0), 0), shlGasUsed(t, true, big.NewInt(0), math.MaxInt32))
	})

	t.Run("affordability boundary", func(t *testing.T) {
		value := big.NewInt(0xFF)
		const bits = int32(249)
		exactGas := shlGasUsed(t, true, value, bits)

		t.Run("succeeds with exactly the gas it needs", func(t *testing.T) {
			vmHost, hooks, destHandle := runShl(t, true, exactGas, value, bits)

			requireDestination(t, vmHost, destHandle, new(big.Int).Lsh(value, uint(bits)))
			require.Equal(t, vmcommon.Ok, vmHost.Output().ReturnCode())
			require.Zero(t, hooks.GetMeteringContext().GasLeft())
		})

		t.Run("rejects one gas unit short without shifting", func(t *testing.T) {
			vmHost, _, destHandle := runShl(t, true, exactGas-1, value, bits)

			requireDestination(t, vmHost, destHandle, big.NewInt(7))
			require.Equal(t, vmhost.ErrNotEnoughGas.Error(), vmHost.Output().ReturnMessage())
		})
	})

	t.Run("rejects an unaffordable result before shifting", func(t *testing.T) {
		const gasProvided = uint64(1_000_000)
		vmHost, hooks, destHandle := runShl(t, true, gasProvided, big.NewInt(1), math.MaxInt32)

		requireDestination(t, vmHost, destHandle, big.NewInt(7))
		require.Equal(t, gasProvided, hooks.GetRuntimeContext().GetPointsUsed())
		require.Equal(t, vmcommon.VMExecutionFailed, vmHost.Output().ReturnCode())
		require.Equal(t, vmhost.ErrNotEnoughGas.Error(), vmHost.Output().ReturnMessage())
	})

	t.Run("negative operands are rejected regardless of fork", func(t *testing.T) {
		cases := []struct {
			name  string
			value int64
			bits  int32
		}{
			{name: "negative source", value: -1, bits: 8},
			{name: "negative shift", value: 1, bits: -1},
		}
		for _, fixAuditV6 := range []bool{false, true} {
			t.Run(fmt.Sprintf("FixAuditChangesV6=%t", fixAuditV6), func(t *testing.T) {
				for _, tc := range cases {
					t.Run(tc.name, func(t *testing.T) {
						vmHost, _, destHandle := runShl(t, fixAuditV6, 1_000_000, big.NewInt(tc.value), tc.bits)

						requireDestination(t, vmHost, destHandle, big.NewInt(7))
						require.Equal(t, vmhost.ErrShiftNegative.Error(), vmHost.Output().ReturnMessage())
					})
				}
			})
		}
	})
}

// bigIntArgumentGetterGasCase is the BigInt getters' row for runArgumentGetterGasCases: from
// FixAuditChangesV6 they pay CopyPerByteForTooBig for an argument past 32 bytes before copying it
// into the destination, and the destination handle is only created once that is paid. A bad index
// still returns silently, without a fault
func bigIntArgumentGetterGasCase(fixedCost uint64, call func(hooks *vmhooks.VMHooksImpl, id int32, destination int32)) argumentGetterGasCase {
	const destination = int32(42)
	gasMap := argumentGetterGasMap()
	return argumentGetterGasCase{
		fixedCost: fixedCost,
		perByteFromV6: func(argLen int) uint64 {
			if argLen <= 32 {
				return 0
			}
			return uint64(argLen) * gasMap["BigIntAPICost"]["CopyPerByteForTooBig"]
		},
		badIndexErr: nil,
		failed:      0,
		call: func(hooks *vmhooks.VMHooksImpl, id int32) int64 {
			call(hooks, id, destination)
			return 0
		},
		// leading zeros do not change the value, so both arguments copy in as the same number
		assertCopied: func(t *testing.T, host *mock.VMHostMock, _ int64, _ []byte) {
			value, err := host.ManagedTypes().GetBigInt(destination)
			require.NoError(t, err)
			assert.Equal(t, big.NewInt(0x010203), value)
		},
		assertUntouched: func(t *testing.T, host *mock.VMHostMock) {
			_, err := host.ManagedTypes().GetBigInt(destination)
			assert.ErrorIs(t, err, vmhost.ErrNoBigIntUnderThisHandle, "the destination must not be created for an unpaid copy")
		},
	}
}

func TestBigIntGetUnsignedArgument_GasPerByteFromV6(t *testing.T) {
	t.Parallel()

	runArgumentGetterGasCases(t, bigIntArgumentGetterGasCase(
		argumentGetterGasMap()["BigIntAPICost"]["BigIntGetUnsignedArgument"],
		func(hooks *vmhooks.VMHooksImpl, id int32, destination int32) {
			hooks.BigIntGetUnsignedArgument(id, destination)
		},
	))
}

func TestBigIntGetSignedArgument_GasPerByteFromV6(t *testing.T) {
	t.Parallel()

	runArgumentGetterGasCases(t, bigIntArgumentGetterGasCase(
		argumentGetterGasMap()["BigIntAPICost"]["BigIntGetSignedArgument"],
		func(hooks *vmhooks.VMHooksImpl, id int32, destination int32) {
			hooks.BigIntGetSignedArgument(id, destination)
		},
	))
}

// bigIntByteLengthHook is one of the two big int byte length hooks, with the length it must answer
type bigIntByteLengthHook struct {
	name      string
	fixedCost uint64
	call      func(hooks *vmhooks.VMHooksImpl, handle int32) int32
	want      func(value *big.Int) int32
}

func bigIntByteLengthHooks() []bigIntByteLengthHook {
	gasMap := flatCloneGasMap()
	return []bigIntByteLengthHook{
		{
			name:      "Unsigned",
			fixedCost: gasMap["BigIntAPICost"]["BigIntUnsignedByteLength"],
			call: func(hooks *vmhooks.VMHooksImpl, handle int32) int32 {
				return hooks.BigIntUnsignedByteLength(handle)
			},
			want: func(value *big.Int) int32 { return int32(len(value.Bytes())) },
		},
		{
			name:      "Signed",
			fixedCost: gasMap["BigIntAPICost"]["BigIntSignedByteLength"],
			call: func(hooks *vmhooks.VMHooksImpl, handle int32) int32 {
				return hooks.BigIntSignedByteLength(handle)
			},
			want: func(value *big.Int) int32 { return int32(len(twos.ToBytes(value))) },
		},
	}
}

// The byte length hooks encoded the whole value just to measure it, for a flat cost. From
// FixAuditChangesV6 they measure it without the encoding, and their flat cost is bounded. The
// length and the gas must stay what they were. Not parallel: it counts allocations
func TestBigIntByteLength_NoEncodingFromV6(t *testing.T) {
	// the top bit set: the signed encoding takes one more byte than the unsigned one
	large := new(big.Int).Lsh(big.NewInt(1), 8*flatCloneOversizedLen-1)
	values := map[string]*big.Int{
		"small":         big.NewInt(-129),
		"large":         large,
		"largeNegative": new(big.Int).Neg(large),
	}

	for _, hook := range bigIntByteLengthHooks() {
		for _, fixV6 := range []bool{false, true} {
			for _, name := range []string{"small", "large", "largeNegative"} {
				value := values[name]
				t.Run(fmt.Sprintf("%s/FixAuditChangesV6=%v/%s", hook.name, fixV6, name), func(t *testing.T) {
					host, runtime := newFlatCloneHost(t, fixV6)
					handle := host.ManagedTypes().NewBigInt(value)

					var result int32
					gasUsed, allocated := runFlatCloneHook(host, 1_000_000, func() {
						result = hook.call(vmhooks.NewVMHooksImpl(host), handle)
					})

					require.Nil(t, runtime.FailExecutionErr)
					assert.Equal(t, hook.want(value), result)
					assert.Equal(t, hook.fixedCost, gasUsed)
					if name == "small" {
						return
					}
					if fixV6 {
						assert.Less(t, allocated, uint64(flatCloneNoCloneBelow), "the value must not be encoded")
					} else {
						assert.GreaterOrEqual(t, allocated, uint64(flatCloneOversizedLen))
					}
				})
			}

			t.Run(fmt.Sprintf("%s/FixAuditChangesV6=%v/missing", hook.name, fixV6), func(t *testing.T) {
				host, runtime := newFlatCloneHost(t, fixV6)

				var result int32
				runFlatCloneHook(host, 1_000_000, func() {
					result = hook.call(vmhooks.NewVMHooksImpl(host), 1234)
				})

				require.ErrorIs(t, runtime.FailExecutionErr, vmhost.ErrNoBigIntUnderThisHandle)
				assert.Equal(t, int32(-1), result)
			})

			t.Run(fmt.Sprintf("%s/FixAuditChangesV6=%v/fixedShortfall", hook.name, fixV6), func(t *testing.T) {
				host, runtime := newFlatCloneHost(t, fixV6)
				handle := host.ManagedTypes().NewBigInt(large)

				var result int32
				gasUsed, _ := runFlatCloneHook(host, hook.fixedCost-1, func() {
					result = hook.call(vmhooks.NewVMHooksImpl(host), handle)
				})

				if fixV6 {
					require.ErrorIs(t, runtime.FailExecutionErr, vmhost.ErrNotEnoughGas)
					assert.Equal(t, int32(-1), result)
					return
				}
				// pre-fork the flat cost overdraws and the hook carries on
				require.Nil(t, runtime.FailExecutionErr)
				assert.Equal(t, hook.want(large), result)
				assert.Equal(t, hook.fixedCost, gasUsed)
			})
		}
	}
}

// bigIntByteLengthValues is every value around a power of two up to 2^520, positive and negative,
// so every byte boundary of both encodings, and -2^(8k-1) and -1 among them; plus random values
func bigIntByteLengthValues() []*big.Int {
	values := []*big.Int{big.NewInt(0)}
	for bits := uint(0); bits <= 520; bits++ {
		power := new(big.Int).Lsh(big.NewInt(1), bits)
		for delta := int64(-2); delta <= 2; delta++ {
			value := new(big.Int).Add(power, big.NewInt(delta))
			values = append(values, value, new(big.Int).Neg(value))
		}
	}
	random := rand.New(rand.NewSource(2755))
	for i := 0; i < 5000; i++ {
		buf := make([]byte, random.Intn(300))
		random.Read(buf)
		value := new(big.Int).SetBytes(buf)
		if random.Intn(2) == 0 {
			value.Neg(value)
		}
		values = append(values, value)
	}
	return values
}

// From FixAuditChangesV6 the byte length hooks compute the length instead of encoding the value:
// it must be the length of the encoding, len(value.Bytes()) and len(twos.ToBytes(value)), for
// every value, as it is before the fork
func TestBigIntByteLength_MatchesTheEncoding(t *testing.T) {
	t.Parallel()

	values := bigIntByteLengthValues()
	for _, hook := range bigIntByteLengthHooks() {
		for _, fixV6 := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/FixAuditChangesV6=%v", hook.name, fixV6), func(t *testing.T) {
				t.Parallel()

				host, runtime := newFlatCloneHost(t, fixV6)
				hooks := vmhooks.NewVMHooksImpl(host)
				for _, value := range values {
					handle := host.ManagedTypes().NewBigInt(value)
					require.Equal(t, hook.want(value), hook.call(hooks, handle), "value %s", value)
				}
				require.Nil(t, runtime.FailExecutionErr)
			})
		}
	}
}
