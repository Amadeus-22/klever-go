package logs

import (
	"time"

	clientSocket "github.com/klever-io/klever-go/websocket"
)

const MsgQueueSize = msgQueueSize

type LogSender struct {
	*logSender
}

func (ls *LogSender) Set(l *logSender) {
	ls.logSender = l
}

func (ls *logSender) Writer() *logWriter {
	return ls.writer
}

func (ls *logSender) SetWriter(lw *logWriter) {
	ls.writer = lw
}

// SetKeepalive shortens this sender's keepalive timings so a test can observe a reclaim in
// milliseconds. Per-sender, so it mutates no shared state and races with nothing; call it
// before StartSendingBlocking.
func (ls *logSender) SetKeepalive(ping, pong, write time.Duration) {
	ls.pingPeriod, ls.pongWait, ls.writeWait = ping, pong, write
}

// ResetProfileRefs clears the package-global profile refcount so profile tests do not
// inherit a count left behind by an earlier one.
func ResetProfileRefs() {
	mutProfile.Lock()
	defer mutProfile.Unlock()

	profileRefs = 0
}

// ProfileRefs exposes the current refcount.
func ProfileRefs() int {
	mutProfile.Lock()
	defer mutProfile.Unlock()

	return profileRefs
}

// SetHandshakeFailBudget gives this sender its own failed-handshake budget, so a test is not
// racing every other sender in the process for the shared one.
func (ls *logSender) SetHandshakeFailBudget(w *clientSocket.DropWarner) {
	ls.handshakeFailWarn = w
}

// SetLoopPanicBudget gives this sender its own budget for its loops' recovered panics, so a test
// that expects the panic line is not racing every other sender for the shared one.
func (ls *logSender) SetLoopPanicBudget(w *clientSocket.DropWarner) {
	ls.loopPanicWarn = w
}

// LoopPanicBudget exposes the budget this sender's loops will draw on.
func (ls *logSender) LoopPanicBudget() *clientSocket.DropWarner {
	return ls.loopPanicWarn
}

// DefaultLoopPanicBudget exposes the process-wide budget every production sender's loops share.
func DefaultLoopPanicBudget() *clientSocket.DropWarner {
	return defaultLoopPanicWarn
}

// IsTeardownError exposes the write-failure classification.
var IsTeardownError = isTeardownError

// HandshakeFailBudget exposes the budget this sender will draw on.
func (ls *logSender) HandshakeFailBudget() *clientSocket.DropWarner {
	return ls.handshakeFailWarn
}

// DefaultHandshakeFailBudget exposes the process-wide budget every production sender shares.
func DefaultHandshakeFailBudget() *clientSocket.DropWarner {
	return defaultHandshakeFailWarn
}
