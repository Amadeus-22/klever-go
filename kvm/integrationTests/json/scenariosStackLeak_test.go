package vmjsonintegrationtest

import (
	"testing"
)

// TestStackLeakScenarios runs loops that leaked native stack in the unfixed
// singlepass compiler (KLC-2785, KLC-2786). Every endpoint must return 7; a
// library without the fixes fails elseLeak (and, on arm64, callSpilledArgs and
// brIfSpilled) with VMExecutionFailed.
func TestStackLeakScenarios(t *testing.T) {
	ScenariosTest(t).
		Folder("stack-leak/scenarios").
		File("stack_leak.scen.json").
		Run().
		CheckNoError()
}
