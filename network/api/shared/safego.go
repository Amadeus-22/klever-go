package shared

import (
	"io"

	logger "github.com/klever-io/klever-go-logger"
	"github.com/klever-io/klever-go/tools/panicguard"
)

// SafeRun runs fn on the calling goroutine under a recover. A goroutine detached from the
// request is outside gin.Recovery(), so an unrecovered panic in it takes the node down instead
// of the one connection; SafeRun logs it and closes conn so the connection's own teardown still
// runs. Defers inside fn — a limiter release, say — run during the unwind, before the recover.
// It returns only once that recovery has finished, so a caller that needs to know the goroutine
// is entirely done — a join before releasing a connection slot — wraps SafeRun rather than
// deferring inside fn, where the accounting would run before the recovery.
//
// The log line draws on budget: these routes are peer-driven (an unauthenticated /log among
// them), so a panic a peer can reproduce would otherwise buy a stack trace per reconnect. The
// close is not budgeted; every panicked connection is torn down. A nil budget logs every panic.
func SafeRun(log logger.Logger, name string, conn io.Closer, budget panicguard.Budget, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			// Close ends the connection first, so the teardown does not depend on the log.
			// panicguard quotes the value, which can echo peer input and forge a log line
			// (KLC-2596), and logs this goroutine's stack.
			_ = conn.Close()
			panicguard.Log(log, "panic in detached websocket goroutine", name, r, budget)
		}
	}()

	fn()
}

// SafeGo runs fn on its own goroutine under SafeRun.
func SafeGo(log logger.Logger, name string, conn io.Closer, budget panicguard.Budget, fn func()) {
	go SafeRun(log, name, conn, budget, fn)
}
