package vmhooks

const (
	managedMapNewName      = "managedMapNew"
	managedMapPutName      = "managedMapPut"
	managedMapGetName      = "managedMapGet"
	managedMapRemoveName   = "managedMapRemove"
	managedMapContainsName = "managedMapContains"
)

// ManagedMapNew VMHooks implementation.
// @autogenerate(VMHooks)
func (context *VMHooksImpl) ManagedMapNew() int32 {
	managedType := context.GetManagedTypesContext()
	metering := context.GetMeteringContext()

	gasToUse := metering.GasSchedule().ManagedMapAPICost.ManagedMapNew
	metering.UseGasAndAddTracedGas(managedMapNewName, gasToUse)

	return managedType.NewManagedMap()
}

// ManagedMapPut VMHooks implementation.
// @autogenerate(VMHooks)
func (context *VMHooksImpl) ManagedMapPut(mMapHandle int32, keyHandle int32, valueHandle int32) int32 {
	managedType := context.GetManagedTypesContext()
	metering := context.GetMeteringContext()
	runtime := context.GetRuntimeContext()

	gasToUse := metering.GasSchedule().ManagedMapAPICost.ManagedMapPut
	// from FixAuditChangesV6 the flat gas is bounded, and the bytes are paid for before they are
	// copied, in managedType.ManagedMapPut
	err := startTracingAndUseGas(context.host, managedMapPutName, gasToUse)
	if context.WithFault(err, runtime.ManagedMapAPIErrorShouldFailExecution()) {
		return 1
	}

	err = managedType.ManagedMapPut(mMapHandle, keyHandle, valueHandle)
	if context.WithFault(err, runtime.ManagedMapAPIErrorShouldFailExecution()) {
		return 1
	}

	return 0
}

// ManagedMapGet VMHooks implementation.
// @autogenerate(VMHooks)
func (context *VMHooksImpl) ManagedMapGet(mMapHandle int32, keyHandle int32, outValueHandle int32) int32 {
	managedType := context.GetManagedTypesContext()
	metering := context.GetMeteringContext()
	runtime := context.GetRuntimeContext()

	gasToUse := metering.GasSchedule().ManagedMapAPICost.ManagedMapGet
	// from FixAuditChangesV6 the flat gas is bounded, and the bytes are paid for before they are
	// copied, in managedType.ManagedMapGet
	err := startTracingAndUseGas(context.host, managedMapGetName, gasToUse)
	if context.WithFault(err, runtime.ManagedMapAPIErrorShouldFailExecution()) {
		return 1
	}

	err = managedType.ManagedMapGet(mMapHandle, keyHandle, outValueHandle)
	if context.WithFault(err, runtime.ManagedMapAPIErrorShouldFailExecution()) {
		return 1
	}

	return 0
}

// ManagedMapRemove VMHooks implementation.
// @autogenerate(VMHooks)
func (context *VMHooksImpl) ManagedMapRemove(mMapHandle int32, keyHandle int32, outValueHandle int32) int32 {
	managedType := context.GetManagedTypesContext()
	metering := context.GetMeteringContext()
	runtime := context.GetRuntimeContext()

	gasToUse := metering.GasSchedule().ManagedMapAPICost.ManagedMapRemove
	// from FixAuditChangesV6 the flat gas is bounded, and the bytes are paid for before they are
	// copied, in managedType.ManagedMapRemove
	err := startTracingAndUseGas(context.host, managedMapRemoveName, gasToUse)
	if context.WithFault(err, runtime.ManagedMapAPIErrorShouldFailExecution()) {
		return 1
	}

	err = managedType.ManagedMapRemove(mMapHandle, keyHandle, outValueHandle)
	if context.WithFault(err, runtime.ManagedMapAPIErrorShouldFailExecution()) {
		return 1
	}

	return 0
}

// ManagedMapContains VMHooks implementation.
// @autogenerate(VMHooks)
func (context *VMHooksImpl) ManagedMapContains(mMapHandle int32, keyHandle int32) int32 {
	managedType := context.GetManagedTypesContext()
	metering := context.GetMeteringContext()
	runtime := context.GetRuntimeContext()

	gasToUse := metering.GasSchedule().ManagedMapAPICost.ManagedMapContains
	// from FixAuditChangesV6 the flat gas is bounded, and the key is paid for before it is cloned,
	// in managedType.ManagedMapContains
	err := startTracingAndUseGas(context.host, managedMapContainsName, gasToUse)
	if context.WithFault(err, runtime.ManagedMapAPIErrorShouldFailExecution()) {
		return 2
	}

	foundValue, err := managedType.ManagedMapContains(mMapHandle, keyHandle)
	if context.WithFault(err, runtime.ManagedMapAPIErrorShouldFailExecution()) {
		return 2
	}

	if foundValue {
		return 1
	}

	return 0
}
