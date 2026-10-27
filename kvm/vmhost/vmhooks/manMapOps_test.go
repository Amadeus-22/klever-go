package vmhooks_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ManagedMapContains cloned the key, whatever its size, for a flat cost. From FixAuditChangesV6 the
// flat cost is bounded and the key is paid for, DataCopyPerByte a byte and bounded, before it is
// cloned. An unknown map still faults first, and a missing key next, as they did: neither of them
// is charged for the key. Not parallel: it counts allocations
func TestVMHooksImpl_ManagedMapContains_KeyChargedFromV6(t *testing.T) {
	gasMap := flatCloneGasMap()
	fixedCost := gasMap["ManagedMapAPICost"]["ManagedMapContains"]
	perByte := gasMap["BaseOperationCost"]["DataCopyPerByte"]
	smallKey := []byte("key")
	oversizedKey := bytes.Repeat([]byte{0xAB}, flatCloneOversizedLen)
	keyCost := func(key []byte) uint64 { return uint64(len(key)) * perByte }

	type scenario struct {
		name       string
		key        []byte
		stored     bool
		unknownMap bool
		missingKey bool
		budget     uint64
		failErr    error
		result     int32
		gasUsed    uint64
		cloned     bool
	}

	for _, fixV6 := range []bool{false, true} {
		charged := func(key []byte) uint64 {
			if !fixV6 {
				return fixedCost
			}
			return fixedCost + keyCost(key)
		}
		scenarios := []scenario{
			{name: "small", key: smallKey, stored: true, result: 1, gasUsed: charged(smallKey)},
			{name: "oversized", key: oversizedKey, stored: true, result: 1, gasUsed: charged(oversizedKey), cloned: true},
			{name: "absent", key: oversizedKey, result: 0, gasUsed: charged(oversizedKey), cloned: true},
			// the budget covers the flat cost only: had the key been charged first, it would be
			// ErrNotEnoughGas instead
			{name: "unknownMap", key: oversizedKey, unknownMap: true, budget: fixedCost,
				failErr: vmhost.ErrNoManagedMapUnderThisHandle, result: 2},
			{name: "missingKey", missingKey: true, budget: fixedCost,
				failErr: vmhost.ErrNoManagedBufferUnderThisHandle, result: 2},
		}
		if fixV6 {
			scenarios = append(scenarios,
				scenario{name: "fixedShortfall", key: oversizedKey, stored: true, budget: fixedCost - 1,
					failErr: vmhost.ErrNotEnoughGas, result: 2},
				scenario{name: "keyShortfall", key: oversizedKey, stored: true, budget: fixedCost + keyCost(oversizedKey) - 1,
					failErr: vmhost.ErrNotEnoughGas, result: 2},
			)
		} else {
			// pre-fork the flat cost overdraws, the key is free, and the hook carries on
			scenarios = append(scenarios,
				scenario{name: "fixedShortfall", key: oversizedKey, stored: true, budget: fixedCost - 1,
					result: 1, gasUsed: fixedCost, cloned: true},
			)
		}

		for _, sc := range scenarios {
			t.Run(fmt.Sprintf("FixAuditChangesV6=%v/%s", fixV6, sc.name), func(t *testing.T) {
				host, runtime := newFlatCloneHost(t, fixV6)
				managedTypes := host.ManagedTypes()

				mapHandle := managedTypes.NewManagedMap()
				keyHandle := int32(1234)
				if !sc.missingKey {
					keyHandle = managedTypes.NewManagedBufferFromBytes(sc.key)
				}
				if sc.stored {
					valueHandle := managedTypes.NewManagedBufferFromBytes([]byte("value"))
					require.NoError(t, managedTypes.ManagedMapPut(mapHandle, keyHandle, valueHandle))
				}
				if sc.unknownMap {
					mapHandle = 4321
				}
				budget := sc.budget
				if budget == 0 {
					budget = fixedCost + keyCost(oversizedKey) + 1_000_000
				}

				var result int32
				gasUsed, allocated := runFlatCloneHook(host, budget, func() {
					result = vmhooks.NewVMHooksImpl(host).ManagedMapContains(mapHandle, keyHandle)
				})

				assert.Equal(t, sc.result, result)
				if sc.failErr != nil {
					require.ErrorIs(t, runtime.FailExecutionErr, sc.failErr)
				} else {
					require.Nil(t, runtime.FailExecutionErr)
					assert.Equal(t, sc.gasUsed, gasUsed)
				}
				if len(sc.key) == flatCloneOversizedLen {
					if sc.cloned {
						assert.GreaterOrEqual(t, allocated, uint64(flatCloneOversizedLen))
					} else {
						assert.Less(t, allocated, uint64(flatCloneNoCloneBelow), "the key must not be cloned")
					}
				}
			})
		}
	}
}

type perByteMapHook struct {
	name      string
	fixedCost uint64
	isPut     bool
	isRemove  bool
	call      func(hooks *vmhooks.VMHooksImpl, mapHandle int32, keyHandle int32, valueHandle int32) int32
	// gas is what a successful call uses
	gas func(fixV6 bool, key []byte, value []byte, found bool) uint64
}

func perByteMapHooks() []perByteMapHook {
	gasMap := perByteHookGasMap()
	perByte := gasMap["BaseOperationCost"]["DataCopyPerByte"]
	bytesCost := func(data []byte) uint64 { return uint64(len(data)) * perByte }
	foundCost := func(value []byte, found bool) uint64 {
		if !found {
			return 0
		}
		return bytesCost(value)
	}

	putCost := gasMap["ManagedMapAPICost"]["ManagedMapPut"]
	getCost := gasMap["ManagedMapAPICost"]["ManagedMapGet"]
	removeCost := gasMap["ManagedMapAPICost"]["ManagedMapRemove"]
	return []perByteMapHook{
		{
			name: "ManagedMapPut", fixedCost: putCost, isPut: true,
			call: func(hooks *vmhooks.VMHooksImpl, m int32, k int32, v int32) int32 { return hooks.ManagedMapPut(m, k, v) },
			gas: func(_ bool, key []byte, value []byte, _ bool) uint64 {
				return putCost + bytesCost(key) + bytesCost(value)
			},
		},
		{
			// KLC-2728: from FixAuditChangesV6 the key is paid for too, as Contains and Remove pay it
			name: "ManagedMapGet", fixedCost: getCost,
			call: func(hooks *vmhooks.VMHooksImpl, m int32, k int32, v int32) int32 { return hooks.ManagedMapGet(m, k, v) },
			gas: func(fixV6 bool, key []byte, value []byte, found bool) uint64 {
				if !fixV6 {
					return getCost + foundCost(value, found)
				}
				return getCost + bytesCost(key) + foundCost(value, found)
			},
		},
		{
			name: "ManagedMapRemove", fixedCost: removeCost, isRemove: true,
			call: func(hooks *vmhooks.VMHooksImpl, m int32, k int32, v int32) int32 {
				return hooks.ManagedMapRemove(m, k, v)
			},
			gas: func(_ bool, key []byte, value []byte, found bool) uint64 {
				return removeCost + bytesCost(key) + foundCost(value, found)
			},
		},
	}
}

// ManagedMapPut, ManagedMapGet and ManagedMapRemove charged DataCopyPerByte for the key and the
// value after cloning them (Get did not charge the key at all), unbounded. From FixAuditChangesV6
// every charge is bounded and comes before the copy it pays for: an underfunded call fails with
// ErrNotEnoughGas without cloning, without writing the output handle and without changing the map.
// The totals stay as they were, except that Get pays for its key (KLC-2728). An unknown map still
// faults first and a missing handle next, without paying for the bytes. Not parallel: it counts
// allocations
func TestVMHooksImpl_ManagedMapPerByte_ChargedBeforeTheCopyFromV6(t *testing.T) {
	perByte := perByteHookGasMap()["BaseOperationCost"]["DataCopyPerByte"]
	smallKey := []byte("key")
	smallValue := []byte("value")
	oversized := bytes.Repeat([]byte{0xAB}, flatCloneOversizedLen)
	untouched := []byte("untouched")
	oldValue := []byte("old")

	type scenario struct {
		name         string
		key          []byte
		value        []byte
		absent       bool
		unknownMap   bool
		missingKey   bool
		missingValue bool
		budget       uint64
		failErr      error
		cloned       bool
		notCloned    bool
	}

	for _, hook := range perByteMapHooks() {
		for _, fixV6 := range []bool{false, true} {
			success := func(key []byte, value []byte) uint64 { return hook.gas(fixV6, key, value, true) }
			scenarios := []scenario{
				{name: "small", key: smallKey, value: smallValue},
				{name: "oversizedValue", key: smallKey, value: oversized, cloned: true},
				{name: "oversizedKey", key: oversized, value: smallValue, cloned: true},
				// the budget covers the flat cost only: had the bytes been charged first, it would be
				// ErrNotEnoughGas instead
				{name: "unknownMap", key: oversized, value: oversized, unknownMap: true, budget: hook.fixedCost,
					failErr: vmhost.ErrNoManagedMapUnderThisHandle},
				{name: "missingKey", value: oversized, missingKey: true, budget: hook.fixedCost,
					failErr: vmhost.ErrNoManagedBufferUnderThisHandle},
			}
			if hook.isPut {
				// from FixAuditChangesV6 the missing value is reported before the key is cloned
				scenarios = append(scenarios, scenario{name: "missingValue", key: oversized, missingValue: true,
					budget: hook.fixedCost, failErr: vmhost.ErrNoManagedBufferUnderThisHandle, notCloned: fixV6})
			} else {
				scenarios = append(scenarios, scenario{name: "absent", key: oversized, value: smallValue, absent: true})
			}
			if fixV6 {
				scenarios = append(scenarios,
					scenario{name: "fixedShortfall", key: smallKey, value: oversized, budget: hook.fixedCost - 1,
						failErr: vmhost.ErrNotEnoughGas, notCloned: true},
					scenario{name: "keyShortfall", key: oversized, value: smallValue,
						budget: hook.fixedCost + uint64(len(oversized))*perByte - 1, failErr: vmhost.ErrNotEnoughGas, notCloned: true},
					scenario{name: "valueShortfall", key: smallKey, value: oversized,
						budget: success(smallKey, oversized) - 1, failErr: vmhost.ErrNotEnoughGas, notCloned: true},
				)
			} else {
				// pre-fork every charge overdraws and the hook carries on
				scenarios = append(scenarios,
					scenario{name: "fixedShortfall", key: smallKey, value: oversized, budget: hook.fixedCost - 1, cloned: true},
					scenario{name: "valueShortfall", key: smallKey, value: oversized,
						budget: success(smallKey, oversized) - 1, cloned: true},
				)
			}

			for _, sc := range scenarios {
				t.Run(fmt.Sprintf("%s/FixAuditChangesV6=%v/%s", hook.name, fixV6, sc.name), func(t *testing.T) {
					host, runtime, _ := newPerByteHost(t, fixV6)
					managedTypes := host.ManagedTypes()

					mapHandle := managedTypes.NewManagedMap()
					keyHandle := int32(1234)
					if !sc.missingKey {
						keyHandle = managedTypes.NewManagedBufferFromBytes(sc.key)
					}
					valueHandle := int32(4321)
					if hook.isPut {
						if !sc.missingValue {
							valueHandle = managedTypes.NewManagedBufferFromBytes(sc.value)
						}
						if !sc.missingKey {
							oldHandle := managedTypes.NewManagedBufferFromBytes(oldValue)
							require.NoError(t, managedTypes.ManagedMapPut(mapHandle, keyHandle, oldHandle))
						}
					} else {
						if !sc.missingKey && !sc.absent {
							storedHandle := managedTypes.NewManagedBufferFromBytes(sc.value)
							require.NoError(t, managedTypes.ManagedMapPut(mapHandle, keyHandle, storedHandle))
						}
						valueHandle = managedTypes.NewManagedBufferFromBytes(untouched)
					}
					if sc.unknownMap {
						mapHandle = 9999
					}
					budget := sc.budget
					if budget == 0 {
						budget = success(oversized, oversized) + 1_000_000
					}

					var result int32
					gasUsed, allocated := runFlatCloneHook(host, budget, func() {
						result = hook.call(vmhooks.NewVMHooksImpl(host), mapHandle, keyHandle, valueHandle)
					})

					applied := sc.failErr == nil
					if applied {
						require.Nil(t, runtime.FailExecutionErr)
						assert.Equal(t, int32(0), result)
						assert.Equal(t, hook.gas(fixV6, sc.key, sc.value, !sc.absent), gasUsed)
					} else {
						require.ErrorIs(t, runtime.FailExecutionErr, sc.failErr)
						assert.Equal(t, int32(1), result)
					}
					if sc.cloned {
						assert.GreaterOrEqual(t, allocated, uint64(flatCloneOversizedLen))
					}
					if sc.notCloned {
						assert.Less(t, allocated, uint64(flatCloneNoCloneBelow), "nothing may be cloned")
					}
					if sc.unknownMap || sc.missingKey {
						return
					}

					// read the state back with the gas refilled: the failure burned all of it
					runtime.SetPointsUsed(0)
					stored := managedTypes.NewManagedBuffer()
					require.NoError(t, managedTypes.ManagedMapGet(mapHandle, keyHandle, stored))
					storedBytes, err := managedTypes.GetBytes(stored)
					require.NoError(t, err)
					switch {
					case hook.isPut:
						if applied {
							assert.Equal(t, sc.value, storedBytes)
						} else {
							assert.Equal(t, oldValue, storedBytes, "the map must not change")
						}
						return
					case hook.isRemove && applied:
						assert.Empty(t, storedBytes, "the key must be removed")
					case !sc.absent:
						assert.Equal(t, sc.value, storedBytes, "the map must not change")
					}
					outBytes, err := managedTypes.GetBytes(valueHandle)
					require.NoError(t, err)
					switch {
					case !applied:
						assert.Equal(t, untouched, outBytes, "the output handle must not be written")
					case sc.absent:
						assert.Empty(t, outBytes)
					default:
						assert.Equal(t, sc.value, outBytes)
					}
				})
			}
		}
	}
}
