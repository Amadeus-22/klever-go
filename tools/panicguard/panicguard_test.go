package panicguard_test

import (
	"strings"
	"testing"

	logger "github.com/klever-io/klever-go-logger"
	"github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/tools/panicguard"
	clientSocket "github.com/klever-io/klever-go/websocket"
	"github.com/stretchr/testify/require"
)

// panickingStringer panics when fmt formats it
type panickingStringer struct{}

func (panickingStringer) String() string { panic("String panicked") }

// nestedPanicker panics with itself when formatted, so fmt's own recover re-panics
type nestedPanicker struct{}

func (nestedPanicker) String() string { panic(nestedPanicker{}) }

type budgetStub struct {
	fired []bool
	calls int
}

func (b *budgetStub) Fire() (int64, bool) {
	ok := b.fired[b.calls]
	b.calls++
	if !ok {
		return 0, false
	}
	return int64(b.calls), true
}

func collect(logged *[][]any) *mock.LoggerStub {
	return &mock.LoggerStub{LogCalled: func(level logger.LogLevel, _ string, args ...any) {
		if level == logger.LogError {
			*logged = append(*logged, args)
		}
	}}
}

func argValue(t *testing.T, args []any, key string) any {
	t.Helper()
	for i := 0; i+1 < len(args); i += 2 {
		if args[i] == key {
			return args[i+1]
		}
	}
	t.Fatalf("log line is missing %q", key)
	return nil
}

func TestValue_QuotesAndCaps(t *testing.T) {
	t.Parallel()

	require.Equal(t, `"line\nforged"`, panicguard.Value("line\nforged"))
	require.Len(t, panicguard.Value(strings.Repeat("a", 1000)), 256+2)
}

func TestValue_PanickingStringIsContained(t *testing.T) {
	t.Parallel()

	require.NotPanics(t, func() {
		require.Contains(t, panicguard.Value(panickingStringer{}), "String panicked")
	})
}

func TestValue_NestedPanicFallsBackToType(t *testing.T) {
	t.Parallel()

	var rendered string
	require.NotPanics(t, func() {
		rendered = panicguard.Value(nestedPanicker{})
	})
	require.Equal(t, `"panicguard_test.nestedPanicker"`, rendered)
}

func TestLog_WithoutBudgetLogsEveryOccurrence(t *testing.T) {
	t.Parallel()

	var logged [][]any
	log := collect(&logged)

	panicguard.Log(log, "msg", "worker", "boom", nil, "droppedTxs", 3)
	panicguard.Log(log, "msg", "worker", "boom", nil)

	require.Len(t, logged, 2)
	require.Equal(t, "worker", argValue(t, logged[0], "goroutine"))
	require.Equal(t, `"boom"`, argValue(t, logged[0], "recover"))
	require.Equal(t, 3, argValue(t, logged[0], "droppedTxs"))
	require.Contains(t, argValue(t, logged[0], "stack"), "panicguard.Log")
}

func TestLog_BudgetFoldsOccurrences(t *testing.T) {
	t.Parallel()

	var logged [][]any
	log := collect(&logged)
	budget := &budgetStub{fired: []bool{true, false, false, true}}

	for range 4 {
		panicguard.Log(log, "msg", "worker", nestedPanicker{}, budget)
	}

	require.Len(t, logged, 2)
	require.EqualValues(t, 4, argValue(t, logged[1], "similarSinceLastLog"))
}

func TestLog_TypedNilDropWarnerLogsEveryOccurrence(t *testing.T) {
	t.Parallel()

	var logged [][]any
	log := collect(&logged)
	var warner *clientSocket.DropWarner

	require.NotPanics(t, func() {
		panicguard.Log(log, "msg", "worker", "boom", warner)
		panicguard.Log(log, "msg", "worker", "boom", warner)
	})

	require.Len(t, logged, 2)
	require.EqualValues(t, 1, argValue(t, logged[1], "similarSinceLastLog"))
}
