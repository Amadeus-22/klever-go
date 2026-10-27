// Package panicguard renders and logs a panic recovered by a barrier around a goroutine that runs
// outside any recover of its own (KLC-2596, KLC-2700). Keeping it in one place means each hardening
// of the log line (quoting, nested-panic safety, rate limiting) is made once for every barrier.
package panicguard

import (
	"fmt"
	"runtime/debug"

	logger "github.com/klever-io/klever-go-logger"
)

// Budget folds repeated occurrences into one log line per window: Fire reports whether this one may
// be logged and, if so, how many were folded into it. websocket.DropWarner satisfies it.
type Budget interface {
	Fire() (int64, bool)
}

// Value renders a recovered panic value for a log line. The value can echo peer input, so it is
// quoted, which escapes the newlines that would forge a log entry, and capped at 256 runes. It
// cannot panic in turn: fmt contains a panicking String or Error method, but re-panics when the
// value that method panicked with panics again while being formatted. That case falls back to the
// value's type, which fmt renders without calling any of its methods.
func Value(r any) (rendered string) {
	defer func() {
		if recover() != nil {
			rendered = fmt.Sprintf("%.256q", fmt.Sprintf("%T", r))
		}
	}()

	return fmt.Sprintf("%.256q", fmt.Sprintf("%v", r))
}

// Log logs a recovered panic at Error with the recovering goroutine's stack, plus any extra
// key/value pairs. With a budget, only the occurrence that opens a window is logged, carrying the
// number folded into it; with a nil budget every occurrence is logged.
func Log(log logger.Logger, message string, goroutine string, r any, budget Budget, extra ...any) {
	var folded []any
	if budget != nil {
		count, ok := budget.Fire()
		if !ok {
			return
		}
		folded = []any{"similarSinceLastLog", count}
	}

	args := []any{"goroutine", goroutine, "recover", Value(r)}
	args = append(args, folded...)
	args = append(args, extra...)
	args = append(args, "stack", string(debug.Stack()))

	log.Error(message, args...)
}
