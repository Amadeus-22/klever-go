package vmhooks

import (
	"bytes"
	"testing"

	commonMock "github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/kvm/config"
	"github.com/klever-io/klever-go/kvm/executor"
	contextmock "github.com/klever-io/klever-go/kvm/mock/context"
	"github.com/klever-io/klever-go/kvm/vmhost"
	hostmock "github.com/klever-io/klever-go/kvm/vmhost/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gasForOversizedInput is plenty for the fixed hook cost, so a rejection can only come from the
// length check and not from gas
const gasForOversizedInput = uint64(1_000_000)

func TestLoadMemBytes(t *testing.T) {
	t.Parallel()

	payload := []byte("hello")
	const gasProvided = uint64(1000)
	const costPerByte = uint64(2)
	charge := costPerByte * uint64(len(payload))
	// out of memory bounds, so reaching MemLoad would fail with a memory error rather than on gas
	const unloadable = executor.MemPtr(1 << 20)

	tests := []struct {
		name              string
		fixAuditChangesV6 bool
		gas               uint64
		offset            executor.MemPtr
		length            int32
		preForkCost       uint64
		postForkCost      uint64
		wantData          []byte
		wantErr           error
		wantLegacyCharges []uint64
		wantGasLeft       uint64
	}{
		{
			name:        "before V6 the pre-fork cost is charged unbounded",
			gas:         gasProvided,
			length:      int32(len(payload)),
			preForkCost: costPerByte, postForkCost: costPerByte,
			wantData:          payload,
			wantLegacyCharges: []uint64{charge},
			wantGasLeft:       gasProvided,
		},
		{
			name:        "before V6 a zero pre-fork cost charges nothing",
			gas:         gasProvided,
			length:      int32(len(payload)),
			preForkCost: 0, postForkCost: costPerByte,
			wantData:    payload,
			wantGasLeft: gasProvided,
		},
		{
			name:              "after V6 the post-fork cost is charged bounded",
			fixAuditChangesV6: true,
			gas:               gasProvided,
			length:            int32(len(payload)),
			preForkCost:       costPerByte, postForkCost: costPerByte,
			wantData:    payload,
			wantGasLeft: gasProvided - charge,
		},
		{
			name:              "after V6 a zero post-fork cost charges nothing",
			fixAuditChangesV6: true,
			gas:               gasProvided,
			length:            int32(len(payload)),
			preForkCost:       costPerByte, postForkCost: 0,
			wantData:    payload,
			wantGasLeft: gasProvided,
		},
		{
			name:              "after V6 one gas short fails before the load",
			fixAuditChangesV6: true,
			gas:               charge - 1,
			offset:            unloadable,
			length:            int32(len(payload)),
			preForkCost:       costPerByte, postForkCost: costPerByte,
			wantErr:     vmhost.ErrNotEnoughGas,
			wantGasLeft: charge - 1,
		},
		{
			name:              "after V6 a negative length is rejected by MemLoad, not by gas",
			fixAuditChangesV6: true,
			gas:               gasProvided,
			length:            -1,
			preForkCost:       costPerByte, postForkCost: costPerByte,
			wantErr:     executor.ErrMemoryNegativeLength,
			wantGasLeft: gasProvided,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := newInternalMockVMHostWithGas(true, tt.gas)
			host.ForkControllerContext.(*commonMock.ForkControllerStub).FixAuditChangesV6Value = tt.fixAuditChangesV6
			metering := host.MeteringContext.(*contextmock.MeteringContextMock)
			// the mock's UseAndTraceGas deducts nothing, so the pre-fork charges are recorded instead
			var legacyCharges []uint64
			metering.UseAndTraceGasCalled = func(gas uint64) { legacyCharges = append(legacyCharges, gas) }
			require.NoError(t, host.Runtime().GetInstance().MemStore(executor.MemPtr(0), payload))

			data, err := loadMemBytes(NewVMHooksImpl(host), tt.offset, tt.length, tt.preForkCost, tt.postForkCost)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantData, data)
			assert.Equal(t, tt.wantLegacyCharges, legacyCharges)
			assert.Equal(t, tt.wantGasLeft, metering.GasLeft())
		})
	}
}

func TestLoadMBufferBytes(t *testing.T) {
	t.Parallel()

	const handle = int32(7)
	const missingHandle = int32(8)
	buffer := []byte("0123456789")
	const gasProvided = uint64(1000)
	const costPerByte = uint64(2)
	charge := costPerByte * uint64(len(buffer))

	tests := []struct {
		name              string
		fixAuditChangesV6 bool
		gas               uint64
		handle            int32
		preForkCost       uint64
		postForkCost      uint64
		wantData          []byte
		wantErr           error
		wantCopies        []string
		wantLegacyCharges []uint64
		wantGasLeft       uint64
	}{
		{
			name:        "before V6 the pre-fork cost is charged unbounded after the copy",
			gas:         gasProvided,
			handle:      handle,
			preForkCost: costPerByte, postForkCost: costPerByte,
			wantData:          buffer,
			wantCopies:        []string{"GetBytes"},
			wantLegacyCharges: []uint64{charge},
			wantGasLeft:       gasProvided,
		},
		{
			name:        "before V6 a zero pre-fork cost charges nothing",
			gas:         gasProvided,
			handle:      handle,
			preForkCost: 0, postForkCost: costPerByte,
			wantData:    buffer,
			wantCopies:  []string{"GetBytes"},
			wantGasLeft: gasProvided,
		},
		{
			name:        "before V6 a missing buffer charges nothing",
			gas:         gasProvided,
			handle:      missingHandle,
			preForkCost: costPerByte, postForkCost: costPerByte,
			wantErr:     vmhost.ErrNoManagedBufferUnderThisHandle,
			wantCopies:  []string{"GetBytes"},
			wantGasLeft: gasProvided,
		},
		{
			name:              "after V6 the post-fork cost is charged bounded",
			fixAuditChangesV6: true,
			gas:               gasProvided,
			handle:            handle,
			preForkCost:       costPerByte, postForkCost: costPerByte,
			wantData:    buffer,
			wantCopies:  []string{"GetBytes"},
			wantGasLeft: gasProvided - charge,
		},
		{
			name:              "after V6 one gas short fails before the copy",
			fixAuditChangesV6: true,
			gas:               charge - 1,
			handle:            handle,
			preForkCost:       costPerByte, postForkCost: costPerByte,
			wantErr:     vmhost.ErrNotEnoughGas,
			wantGasLeft: charge - 1,
		},
		{
			name:              "after V6 a zero post-fork cost charges nothing",
			fixAuditChangesV6: true,
			gas:               gasProvided,
			handle:            handle,
			preForkCost:       costPerByte, postForkCost: 0,
			wantData:    buffer,
			wantCopies:  []string{"GetBytes"},
			wantGasLeft: gasProvided,
		},
		{
			name:              "after V6 a missing buffer fails before the charge",
			fixAuditChangesV6: true,
			gas:               gasProvided,
			handle:            missingHandle,
			preForkCost:       costPerByte, postForkCost: costPerByte,
			wantErr:     vmhost.ErrNoManagedBufferUnderThisHandle,
			wantGasLeft: gasProvided,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stored := bytes.Clone(buffer)
			var copies []string
			managedTypes := &hostmock.ManagedTypesContextMock{
				GetLengthCalled: func(h int32) int32 {
					if h != handle {
						return -1
					}
					return int32(len(stored))
				},
				GetBytesCalled: func(h int32) ([]byte, error) {
					copies = append(copies, "GetBytes")
					if h != handle {
						return nil, vmhost.ErrNoManagedBufferUnderThisHandle
					}
					return bytes.Clone(stored), nil
				},
			}
			// the mock's UseAndTraceGas deducts nothing, so the pre-fork charges are recorded instead
			var legacyCharges []uint64
			metering := &contextmock.MeteringContextMock{
				GasLeftMock:          tt.gas,
				GasProvidedMock:      tt.gas,
				UseAndTraceGasCalled: func(gas uint64) { legacyCharges = append(legacyCharges, gas) },
			}
			host := &contextmock.VMHostMock{
				ManagedTypesContext:   managedTypes,
				MeteringContext:       metering,
				ForkControllerContext: &commonMock.ForkControllerStub{FixAuditChangesV6Value: tt.fixAuditChangesV6},
			}

			data, err := loadMBufferBytes(host, tt.handle, tt.preForkCost, tt.postForkCost)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantData, data)
			assert.Equal(t, tt.wantCopies, copies)
			assert.Equal(t, tt.wantLegacyCharges, legacyCharges)
			assert.Equal(t, tt.wantGasLeft, metering.GasLeft())
		})
	}
}

func TestCheckMBufferMaxLength(t *testing.T) {
	t.Parallel()

	const handle = int32(7)
	const missingHandle = int32(8)
	const maxLength = int32(4)

	tests := []struct {
		name              string
		fixAuditChangesV6 bool
		handle            int32
		length            int32
		wantErr           error
	}{
		{name: "before V6 a longer buffer passes", handle: handle, length: maxLength + 1},
		{name: "after V6 a buffer at the maximum passes", fixAuditChangesV6: true, handle: handle, length: maxLength},
		{name: "after V6 a longer buffer is rejected", fixAuditChangesV6: true, handle: handle, length: maxLength + 1, wantErr: vmhost.ErrBadUpperBounds},
		{name: "after V6 a missing buffer passes, for loadMBufferBytes to report", fixAuditChangesV6: true, handle: missingHandle},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := &contextmock.VMHostMock{
				ManagedTypesContext: &hostmock.ManagedTypesContextMock{
					GetLengthCalled: func(h int32) int32 {
						if h != handle {
							return -1
						}
						return tt.length
					},
				},
				ForkControllerContext: &commonMock.ForkControllerStub{FixAuditChangesV6Value: tt.fixAuditChangesV6},
			}

			err := checkMBufferMaxLength(host, tt.handle, vmhost.ErrBadUpperBounds, maxLength)

			assert.Equal(t, tt.wantErr, err)
		})
	}
}

// TestManagedHooksRejectOversizedInputBeforeCopy pins the size-before-copy guarantee of the two managed
// hooks whose loads charge nothing: for them the length check is the only thing standing between an
// oversized handle and a free copy of it. Asserting the return code alone does not pin it, because the
// checks that run after the copy produce the same code, so the check could be deleted and stay green
func TestManagedHooksRejectOversizedInputBeforeCopy(t *testing.T) {
	t.Parallel()

	const (
		rHandle    = int32(7)
		sHandle    = int32(8)
		nameHandle = int32(9)
	)

	tests := []struct {
		name    string
		lengths map[int32]int32
		wantRet int32
		wantErr error
		call    func(host vmhost.VMHost) int32
	}{
		{
			name: "DER r over 32 bytes",
			lengths: map[int32]int32{
				rHandle: secp256k1SignatureComponentMaxLength + 1,
				sHandle: secp256k1SignatureComponentMaxLength,
			},
			wantRet: 1,
			wantErr: vmhost.ErrRAndSExceedsMax32BytesSecp256k1,
			call: func(host vmhost.VMHost) int32 {
				return ManagedEncodeSecp256k1DerSignatureWithHost(host, rHandle, sHandle, 0)
			},
		},
		{
			name: "DER s over 32 bytes",
			lengths: map[int32]int32{
				rHandle: secp256k1SignatureComponentMaxLength,
				sHandle: secp256k1SignatureComponentMaxLength + 1,
			},
			wantRet: 1,
			wantErr: vmhost.ErrRAndSExceedsMax32BytesSecp256k1,
			call: func(host vmhost.VMHost) int32 {
				return ManagedEncodeSecp256k1DerSignatureWithHost(host, rHandle, sHandle, 0)
			},
		},
		{
			name:    "curve name of the wrong length",
			lengths: map[int32]int32{nameHandle: curveNameLength + 1},
			wantRet: -1,
			wantErr: vmhost.ErrBadBounds,
			call: func(host vmhost.VMHost) int32 {
				return ManagedCreateECWithHost(host, nameHandle)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var copies []string
			managedTypes := &hostmock.ManagedTypesContextMock{
				GetLengthCalled: func(handle int32) int32 { return tt.lengths[handle] },
				GetBytesCalled: func(handle int32) ([]byte, error) {
					copies = append(copies, "GetBytes")
					return make([]byte, tt.lengths[handle]), nil
				},
			}
			metering := &contextmock.MeteringContextMock{
				GasLeftMock:     gasForOversizedInput,
				GasProvidedMock: gasForOversizedInput,
			}
			metering.SetGasSchedule(config.MakeGasMapForTests())
			runtime := &contextmock.RuntimeContextMock{FailCryptoAPI: true}
			host := &contextmock.VMHostMock{
				ManagedTypesContext:   managedTypes,
				MeteringContext:       metering,
				RuntimeContext:        runtime,
				ForkControllerContext: &commonMock.ForkControllerStub{FixAuditChangesV6Value: true},
			}

			assert.Equal(t, tt.wantRet, tt.call(host))
			assert.Empty(t, copies, "post-fork an oversized input must be rejected before any copy")
			assert.ErrorIs(t, runtime.FailExecutionErr, tt.wantErr)
		})
	}
}
