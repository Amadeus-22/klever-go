package node

import (
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	logger "github.com/klever-io/klever-go-logger"
	commonMock "github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/data/batch"
	"github.com/klever-io/klever-go/data/transaction"
	"github.com/klever-io/klever-go/tools/marshal"
	"github.com/klever-io/klever-go/tools/panicguard"
	clientSocket "github.com/klever-io/klever-go/websocket"
	"github.com/stretchr/testify/require"
)

// accumulatorStub hands the node a channel the test feeds directly
type accumulatorStub struct {
	ch chan []any
}

func (a *accumulatorStub) AddData(data any)            { a.ch <- []any{data} }
func (a *accumulatorStub) OutputChannel() <-chan []any { return a.ch }
func (a *accumulatorStub) IsInterfaceNil() bool        { return a == nil }

// panickingStringer panics when fmt formats it, as a hostile panic value could
type panickingStringer struct{}

func (panickingStringer) String() string { panic("String panicked") }

// nestedPanicker panics with itself when formatted, which fmt re-panics on
type nestedPanicker struct{}

func (nestedPanicker) String() string { panic(nestedPanicker{}) }

// marshalPanics panics when asked to marshal an object match accepts, only the first time if once
// is set, and delegates every other call. It panics with value, or a plain string when unset.
type marshalPanics struct {
	marshal.Marshalizer
	match func(obj any) bool
	once  bool
	value any
	fired atomic.Bool
}

func (m *marshalPanics) Marshal(obj any) ([]byte, error) {
	if m.match(obj) && (!m.once || m.fired.CompareAndSwap(false, true)) {
		if m.value != nil {
			panic(m.value)
		}
		panic("marshal panicked")
	}
	return m.Marshalizer.Marshal(obj)
}

// marshalFails returns an error when asked to marshal an object match accepts, and delegates every
// other call
type marshalFails struct {
	marshal.Marshalizer
	match func(obj any) bool
}

var errMarshalFailed = errors.New("marshal failed")

func (m *marshalFails) Marshal(obj any) ([]byte, error) {
	if m.match(obj) {
		return nil, errMarshalFailed
	}
	return m.Marshalizer.Marshal(obj)
}

// isPacket matches the packer's marshal of a whole packet, which runs after the per-tx marshals
func isPacket(obj any) bool {
	_, ok := obj.(*batch.Batch)
	return ok
}

func isTxWithNonce(nonce uint64) func(obj any) bool {
	return func(obj any) bool {
		tx, ok := obj.(*transaction.Transaction)
		return ok && tx.GetNonce() == nonce
	}
}

func txWithNonce(nonce uint64) *transaction.Transaction {
	return &transaction.Transaction{RawData: &transaction.Transaction_Raw{Nonce: nonce}}
}

const broadcastTestTimeout = 5 * time.Second

// sendBatch hands one batch to the accumulator loop, failing instead of hanging if the loop is gone
func sendBatch(t *testing.T, acc *accumulatorStub, data any) {
	t.Helper()
	select {
	case acc.ch <- []any{data}:
	case <-time.After(broadcastTestTimeout):
		require.Fail(t, "accumulator loop stopped draining")
	}
}

func waitFor(t *testing.T, ch <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(broadcastTestTimeout):
		require.Fail(t, msg)
	}
}

// waitForGoroutinesGone waits until no goroutine has frame in its stack. A barrier's deferred
// recover runs after the panicking callback's own defers, so this is the signal that it finished.
func waitForGoroutinesGone(t *testing.T, frame string) {
	t.Helper()
	buf := make([]byte, 1<<20)
	require.Eventually(t, func() bool {
		return !strings.Contains(string(buf[:runtime.Stack(buf, true)]), frame)
	}, broadcastTestTimeout, time.Millisecond, "goroutines running %s never finished", frame)
}

// lineRecorder is a log observer that keeps the arguments of every line with one message. The
// observer is process-wide, so the tests using it stay off t.Parallel().
type lineRecorder struct {
	message string
	mut     sync.Mutex
	lines   [][]string
}

func (r *lineRecorder) Write(p []byte) (int, error) { return len(p), nil }

func (r *lineRecorder) Output(line logger.LogLineHandler) []byte {
	if line.GetMessage() != r.message {
		return nil
	}

	r.mut.Lock()
	r.lines = append(r.lines, line.GetArgs())
	r.mut.Unlock()

	return nil
}

func (r *lineRecorder) IsInterfaceNil() bool { return r == nil }

func (r *lineRecorder) snapshot() [][]string {
	r.mut.Lock()
	defer r.mut.Unlock()

	return append([][]string(nil), r.lines...)
}

// waitForLines waits until exactly want lines were logged, failing on more or on a timeout
func (r *lineRecorder) waitForLines(t *testing.T, want int) [][]string {
	t.Helper()
	require.Eventually(t, func() bool { return len(r.snapshot()) >= want }, broadcastTestTimeout, time.Millisecond)
	lines := r.snapshot()
	require.Len(t, lines, want)

	return lines
}

// observeLog records the node logger's lines with message at debug level for the test's duration
func observeLog(t *testing.T, message string) *lineRecorder {
	t.Helper()

	previous := log.GetLevel()
	log.SetLevel(logger.LogDebug)
	recorder := &lineRecorder{message: message}
	require.NoError(t, logger.AddLogObserver(recorder, recorder))
	t.Cleanup(func() {
		_ = logger.RemoveLogObserver(recorder)
		log.SetLevel(previous)
	})

	return recorder
}

// argOf returns the value logged under key
func argOf(t *testing.T, args []string, key string) string {
	t.Helper()
	for i := 0; i+1 < len(args); i += 2 {
		if args[i] == key {
			return args[i+1]
		}
	}
	require.Failf(t, "log line is missing a key", "key %q in %v", key, args)
	return ""
}

// newBudgetedNode is a broadcast test node with fresh budgets, so what it logs does not depend on
// the process-wide budgets other tests draw on
func newBudgetedNode(broadcast func(channel string, topic string, buff []byte) error) *Node {
	n := newBroadcastTestNode(broadcast)
	n.broadcastBudgets = &broadcastPanicBudgets{
		accumulator: clientSocket.NewDropWarner(time.Hour),
		marshal:     clientSocket.NewDropWarner(time.Hour),
		packet:      clientSocket.NewDropWarner(time.Hour),
		counter:     clientSocket.NewDropWarner(time.Hour),
	}

	return n
}

func newBroadcastTestNode(broadcast func(channel string, topic string, buff []byte) error) *Node {
	return &Node{
		internalMarshalizer: &marshal.ProtoMarshalizer{},
		messenger:           &commonMock.MessengerStub{BroadcastOnChannelBlockingCalled: broadcast},
	}
}

// startAccumulatorLoop runs sendFromTxAccumulator over a fresh stub and returns the stub and a
// channel closed once the loop returns
func startAccumulatorLoop(n *Node) (*accumulatorStub, <-chan struct{}) {
	acc := &accumulatorStub{ch: make(chan []any)}
	n.txAccumulator = acc

	done := make(chan struct{})
	go func() {
		n.sendFromTxAccumulator()
		close(done)
	}()

	return acc, done
}

func TestSendBulkTransactionsPack_RecoversPanicInPacketGoroutine(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	panicked := make(chan struct{})
	delivered := make(chan struct{})
	n := newBroadcastTestNode(func(string, string, []byte) error {
		if calls.Add(1) == 1 {
			defer close(panicked)
			panic("broadcast panicked")
		}
		close(delivered)
		return nil
	})

	require.NoError(t, n.sendBulkTransactionsPack([][]byte{[]byte("tx")}))
	waitFor(t, panicked, "first packet never reached the messenger")

	// Unrecovered, the first packet's panic kills the test binary while this one is in flight.
	require.NoError(t, n.sendBulkTransactionsPack([][]byte{[]byte("tx")}))
	waitFor(t, delivered, "second packet was never broadcast")
}

func TestSendFromTxAccumulator_KeepsDrainingAfterPanicInBatch(t *testing.T) {
	t.Parallel()

	delivered := make(chan struct{})
	n := newBroadcastTestNode(func(string, string, []byte) error {
		close(delivered)
		return nil
	})
	// The first packet marshal panics on the accumulator goroutine itself, past the per-tx
	// barrier, so only the batch barrier inside the loop keeps the second batch flowing.
	n.internalMarshalizer = &marshalPanics{Marshalizer: n.internalMarshalizer, match: isPacket, once: true}
	acc, done := startAccumulatorLoop(n)

	sendBatch(t, acc, txWithNonce(1))
	// The channel is unbuffered, so this second send only completes once the loop has recovered
	// from the first batch and come back for more.
	sendBatch(t, acc, txWithNonce(2))
	waitFor(t, delivered, "second batch was never broadcast")

	close(acc.ch)
	waitFor(t, done, "sendFromTxAccumulator did not return after the channel closed")
}

func TestSendFromTxAccumulator_KeepsDrainingAfterPanicInPacket(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	panicked := make(chan struct{})
	delivered := make(chan struct{})
	n := newBroadcastTestNode(func(string, string, []byte) error {
		if calls.Add(1) == 1 {
			defer close(panicked)
			panic("first broadcast panicked")
		}
		close(delivered)
		return nil
	})
	acc, done := startAccumulatorLoop(n)

	// Batches go one at a time: the second is sent only after the first packet goroutine has
	// panicked, so the loop is still draining after a recovered panic.
	sendBatch(t, acc, txWithNonce(1))
	waitFor(t, panicked, "first batch never reached the messenger")

	sendBatch(t, acc, txWithNonce(2))
	waitFor(t, delivered, "second batch was never broadcast")

	close(acc.ch)
	waitFor(t, done, "sendFromTxAccumulator did not return after the channel closed")
}

func TestSendBulkTransactions_PoisonTxIsDroppedAlone(t *testing.T) {
	t.Parallel()

	n := newBroadcastTestNode(func(string, string, []byte) error { return nil })
	n.internalMarshalizer = &marshalPanics{Marshalizer: n.internalMarshalizer, match: isTxWithNonce(1)}

	var sent int
	require.NotPanics(t, func() {
		sent = n.sendBulkTransactions([]*transaction.Transaction{txWithNonce(1), txWithNonce(2), txWithNonce(3)})
	})
	require.Equal(t, 2, sent, "only the poison tx may be lost, not its batch")
}

func TestSendAccumulatedTransactions_CountsOnlySentTxs(t *testing.T) {
	t.Parallel()

	n := newBroadcastTestNode(func(string, string, []byte) error { return nil })
	n.internalMarshalizer = &marshalPanics{Marshalizer: n.internalMarshalizer, match: isPacket, once: true}

	lost := []any{txWithNonce(1), txWithNonce(2)}
	require.NotPanics(t, func() { n.sendAccumulatedTransactions(lost) })
	require.Zero(t, atomic.LoadUint32(&n.txSentCounter), "a batch lost to a recovered panic is not sent")

	n.sendAccumulatedTransactions([]any{txWithNonce(3), txWithNonce(4)})
	require.EqualValues(t, 2, atomic.LoadUint32(&n.txSentCounter))
}

func TestRecordTxSentSecond_RecoversPanic(t *testing.T) {
	t.Parallel()

	n := &Node{}

	require.NotPanics(t, func() { n.recordTxSentSecond(nil) })
}

func TestRecoverBroadcastPanic_ContainsPanickingValue(t *testing.T) {
	t.Parallel()

	require.NotPanics(t, func() {
		defer recoverBroadcastPanic("test", nil)
		panic(panickingStringer{})
	})
}

func TestRecoverBroadcastPanic_ContainsNestedPanickingValue(t *testing.T) {
	t.Parallel()

	require.NotPanics(t, func() {
		defer recoverBroadcastPanic("test", nil)
		panic(nestedPanicker{})
	})
}

func TestRecoverBroadcastPanic_NoPanicIsNoop(t *testing.T) {
	t.Parallel()

	require.NotPanics(t, func() {
		defer recoverBroadcastPanic("test", nil)
	})
}

func TestSendBulkTransactions_MarshalErrorDropsTxAlone(t *testing.T) {
	t.Parallel()

	n := newBroadcastTestNode(func(string, string, []byte) error { return nil })
	n.internalMarshalizer = &marshalFails{Marshalizer: n.internalMarshalizer, match: isTxWithNonce(1)}

	sent := n.sendBulkTransactions([]*transaction.Transaction{txWithNonce(1), txWithNonce(2)})
	require.Equal(t, 1, sent)
}

func TestSendBulkTransactions_PackErrorSendsNothing(t *testing.T) {
	t.Parallel()

	n := newBroadcastTestNode(func(string, string, []byte) error { return nil })
	n.internalMarshalizer = &marshalFails{Marshalizer: n.internalMarshalizer, match: isPacket}

	sent := n.sendBulkTransactions([]*transaction.Transaction{txWithNonce(1), txWithNonce(2)})
	require.Zero(t, sent)
}

// TestSendBulkTransactionsPack_BroadcastErrorIsLogged reads the logger, not the stub: each of the
// two packets must log its own broadcast error. Two packets also run two goroutines, which a
// shared error variable would race on under -race.
func TestSendBulkTransactionsPack_BroadcastErrorIsLogged(t *testing.T) {
	lines := observeLog(t, "node.BroadcastOnChannelBlocking")

	var calls atomic.Int32
	returned := make(chan struct{})
	n := newBroadcastTestNode(func(string, string, []byte) error {
		if calls.Add(1) == 2 {
			defer close(returned)
		}
		return errors.New("broadcast failed")
	})

	// Two buffers over half the limit each cannot share a packet.
	half := make([]byte, core.MaxBulkTransactionSize/2+1)
	require.NoError(t, n.sendBulkTransactionsPack([][]byte{half, half}))
	waitFor(t, returned, "both packets never reached the messenger")

	logged := lines.waitForLines(t, 2)
	for _, args := range logged {
		require.Equal(t, "broadcast failed", argOf(t, args, "error"))
	}
}

func TestSendAccumulatedTransactions_SkipsEmptyBatchAndNonTxObjects(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	n := newBroadcastTestNode(func(string, string, []byte) error {
		calls.Add(1)
		return nil
	})

	n.sendAccumulatedTransactions(nil)
	require.Zero(t, atomic.LoadUint32(&n.txSentCounter))

	n.sendAccumulatedTransactions([]any{"not a tx", txWithNonce(1)})
	require.EqualValues(t, 1, atomic.LoadUint32(&n.txSentCounter), "only the transaction is counted")
}

func TestRecordTxSentSecond_FoldsSecondsAndResetsPeakAfterPrint(t *testing.T) {
	t.Parallel()

	n := &Node{}
	stats := &txSentStats{}

	atomic.StoreUint32(&n.txSentCounter, 5)
	n.recordTxSentSecond(stats)
	atomic.StoreUint32(&n.txSentCounter, 3)
	n.recordTxSentSecond(stats)

	require.Zero(t, atomic.LoadUint32(&n.txSentCounter), "each second swaps the counter out")
	require.EqualValues(t, 5, stats.maxTxCounter, "the peak is kept, not the last second")
	require.EqualValues(t, 8, stats.totalTxCounter)
	require.Equal(t, 2, stats.counterSeconds)

	// Run to the print, which resets the window and its peak but keeps the running total.
	for stats.counterSeconds != 0 {
		n.recordTxSentSecond(stats)
	}
	require.Zero(t, stats.maxTxCounter)
	require.EqualValues(t, 8, stats.totalTxCounter)

	// A window with nothing sent prints nothing and still resets.
	for i := 0; i <= numSecondsBetweenPrints; i++ {
		n.recordTxSentSecond(stats)
	}
	require.Zero(t, stats.counterSeconds)
	require.EqualValues(t, 8, stats.totalTxCounter)
}

// TestBroadcastBarriers_PanicLogsAreBudgeted panics twice at each barrier and reads the logger:
// one line per barrier, carrying the fold count, so a budget dropped to nil logs twice and fails.
// Each barrier draws on its own budget, so the first panic at every site is logged.
func TestBroadcastBarriers_PanicLogsAreBudgeted(t *testing.T) {
	lines := observeLog(t, broadcastPanicMessage)

	// The poison tx panics in marshalForBroadcast; its batch then panics packing.
	marshalNode := newBudgetedNode(func(string, string, []byte) error { return nil })
	poisonTx := isTxWithNonce(1)
	marshalNode.internalMarshalizer = &marshalPanics{
		Marshalizer: marshalNode.internalMarshalizer,
		match:       func(obj any) bool { return poisonTx(obj) || isPacket(obj) },
	}

	var packetPanics atomic.Int32
	packetsDone := make(chan struct{})
	packetNode := newBudgetedNode(func(string, string, []byte) error {
		if packetPanics.Add(1) == 2 {
			defer close(packetsDone)
		}
		panic("broadcast panicked")
	})

	for i := 0; i < 2; i++ {
		marshalNode.sendAccumulatedTransactions([]any{txWithNonce(1), txWithNonce(2), txWithNonce(3)})
		packetNode.recordTxSentSecond(nil)
		require.NoError(t, packetNode.sendBulkTransactionsPack([][]byte{[]byte("tx")}))
	}
	waitFor(t, packetsDone, "both packets never reached the messenger")
	// packetsDone closes before the packet barriers recover, so wait for both to finish logging.
	waitForGoroutinesGone(t, "(*Node).sendBulkTransactionsPack.func")

	byGoroutine := make(map[string][]string)
	for _, args := range lines.waitForLines(t, 4) {
		goroutine := argOf(t, args, "goroutine")
		require.NotContains(t, byGoroutine, goroutine, "a second panic inside the window must not log")
		byGoroutine[goroutine] = args
		require.Equal(t, "1", argOf(t, args, "similarSinceLastLog"))
	}

	require.ElementsMatch(t,
		[]string{"sendFromTxAccumulator", "marshalForBroadcast", "sendBulkTransactionsPack", "printTxSentCounter"},
		mapKeys(byGoroutine))
	// The batch barrier reports the whole batch it lost.
	require.Equal(t, "3", argOf(t, byGoroutine["sendFromTxAccumulator"], "droppedTxs"))
}

// TestDefaultBroadcastPanicBudgets_OnePerBarrier pins what a node outside the tests draws on: a
// budget at every barrier, none shared, so a flood at one site cannot silence another.
func TestDefaultBroadcastPanicBudgets_OnePerBarrier(t *testing.T) {
	t.Parallel()

	require.Same(t, defaultBroadcastPanicBudgets, (&Node{}).panicBudgets())

	budgets := []panicguard.Budget{
		defaultBroadcastPanicBudgets.accumulator,
		defaultBroadcastPanicBudgets.marshal,
		defaultBroadcastPanicBudgets.packet,
		defaultBroadcastPanicBudgets.counter,
	}
	for i, budget := range budgets {
		require.NotNil(t, budget)
		for _, other := range budgets[i+1:] {
			require.NotSame(t, budget, other)
		}
	}
}

func mapKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

// TestBroadcastBarriers_HostilePanicValues drives each KLC-2700 site with the panic values that
// could hurt the barrier itself: one carrying a newline, which logged raw would forge a second log
// entry, and two that panic again when formatted, which would take the node down from inside the
// recover. Every site must survive, keep working, and log the value quoted on one line.
func TestBroadcastBarriers_HostilePanicValues(t *testing.T) {
	values := []struct {
		name     string
		value    any
		rendered func(t *testing.T, logged string)
	}{
		{
			name:  "newline cannot forge a log entry",
			value: "bad key\nERROR forged entry",
			rendered: func(t *testing.T, logged string) {
				require.Equal(t, `"bad key\nERROR forged entry"`, logged, "the newline must survive as an escape")
			},
		},
		{
			name:  "panicking String is contained",
			value: panickingStringer{},
			rendered: func(t *testing.T, logged string) {
				require.Contains(t, logged, "String panicked")
			},
		},
		{
			name:  "nested panic falls back to the type",
			value: nestedPanicker{},
			rendered: func(t *testing.T, logged string) {
				require.Equal(t, `"node.nestedPanicker"`, logged)
			},
		},
	}

	sites := []struct {
		goroutine string
		// run panics the site once with value, fails the test unless the site keeps working
		// afterwards, and returns once the recovered panic has been logged
		run func(t *testing.T, value any)
	}{
		{
			// The boot loop (options.go) must still drain the next batch after the panic.
			goroutine: "sendFromTxAccumulator",
			run: func(t *testing.T, value any) {
				delivered := make(chan struct{})
				n := newBudgetedNode(func(string, string, []byte) error {
					close(delivered)
					return nil
				})
				n.internalMarshalizer = &marshalPanics{Marshalizer: n.internalMarshalizer, match: isPacket, once: true, value: value}
				acc, done := startAccumulatorLoop(n)

				sendBatch(t, acc, txWithNonce(1))
				sendBatch(t, acc, txWithNonce(2))
				waitFor(t, delivered, "the loop stopped broadcasting after the panic")

				close(acc.ch)
				waitFor(t, done, "sendFromTxAccumulator did not return after the channel closed")
			},
		},
		{
			// One goroutine per packet: the next packet must still go out.
			goroutine: "sendBulkTransactionsPack",
			run: func(t *testing.T, value any) {
				var calls atomic.Int32
				panicked := make(chan struct{})
				delivered := make(chan struct{})
				n := newBudgetedNode(func(string, string, []byte) error {
					if calls.Add(1) == 1 {
						defer close(panicked)
						panic(value)
					}
					close(delivered)
					return nil
				})

				require.NoError(t, n.sendBulkTransactionsPack([][]byte{[]byte("tx")}))
				waitFor(t, panicked, "first packet never reached the messenger")
				require.NoError(t, n.sendBulkTransactionsPack([][]byte{[]byte("tx")}))
				waitFor(t, delivered, "second packet was never broadcast")
			},
		},
		{
			// The per-tx barrier: the poison tx is dropped, its neighbour is still sent.
			goroutine: "marshalForBroadcast",
			run: func(t *testing.T, value any) {
				n := newBudgetedNode(func(string, string, []byte) error { return nil })
				n.internalMarshalizer = &marshalPanics{Marshalizer: n.internalMarshalizer, match: isTxWithNonce(1), value: value}

				require.Equal(t, 1, n.sendBulkTransactions([]*transaction.Transaction{txWithNonce(1), txWithNonce(2)}))
			},
		},
	}

	for _, site := range sites {
		for _, v := range values {
			t.Run(site.goroutine+"/"+v.name, func(t *testing.T) {
				lines := observeLog(t, broadcastPanicMessage)

				site.run(t, v.value)

				args := lines.waitForLines(t, 1)[0]
				require.Equal(t, site.goroutine, argOf(t, args, "goroutine"))
				logged := argOf(t, args, "recover")
				require.NotContains(t, logged, "\n", "a raw newline would split the entry")
				v.rendered(t, logged)
				require.NotEmpty(t, argOf(t, args, "stack"), "a recovered panic without its stack is not actionable")
			})
		}
	}
}
