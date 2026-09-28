package outbox

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// drainStore serves staged rows in batches and records what was marked sent.
//
// The relay is the piece that turns "committed but not published" into
// at-least-once delivery, so what matters is the *ordering* of its two effects:
// a row marked sent before its event was accepted by the bus is a message that
// silently never arrives, and no later retry will find it.
type drainStore struct {
	mu sync.Mutex
	// batches are served in order, one per Poll call.
	batches [][]store.OutboxRecord
	polls   int
	sent    []string
	pollErr error
	markErr error
}

func (d *drainStore) Poll(context.Context, int) ([]store.OutboxRecord, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pollErr != nil {
		return nil, d.pollErr
	}
	d.polls++
	if len(d.batches) == 0 {
		return nil, nil
	}
	batch := d.batches[0]
	d.batches = d.batches[1:]
	return batch, nil
}

func (d *drainStore) MarkSent(_ context.Context, ids []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.markErr != nil {
		return d.markErr
	}
	d.sent = append(d.sent, ids...)
	return nil
}

func (d *drainStore) PurgeSent(context.Context, int64, int) (int, error) { return 0, nil }

func (d *drainStore) marked() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.sent...)
}

func (d *drainStore) pollCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.polls
}

// recordingBus captures published events and can fail on a chosen subject.
type recordingBus struct {
	mu        sync.Mutex
	published []eventbus.Event
	failOn    string
}

func (b *recordingBus) Publish(_ context.Context, e eventbus.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failOn != "" && e.Subject == b.failOn {
		return errors.New("bus is down")
	}
	b.published = append(b.published, e)
	return nil
}

func (b *recordingBus) Subscribe(string, string, eventbus.Handler) error { return nil }
func (b *recordingBus) Close() error                                     { return nil }

func (b *recordingBus) events() []eventbus.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]eventbus.Event(nil), b.published...)
}

func record(id, subject string) store.OutboxRecord {
	return store.OutboxRecord{
		ID: id, Subject: subject, Key: "chat-1", Data: []byte(`{"id":"` + id + `"}`),
		Trace: map[string]string{"traceparent": "00-abc-def-01"},
	}
}

func newRelay(st store.OutboxStore, bus eventbus.Bus) *Relay {
	return New(st, bus, discardLog())
}

func TestDrainPublishesEveryStagedRow(t *testing.T) {
	st := &drainStore{batches: [][]store.OutboxRecord{{
		record("1", "message.created"),
		record("2", "message.created"),
	}}}
	bus := &recordingBus{}

	newRelay(st, bus).drain(context.Background())

	if got := len(bus.events()); got != 2 {
		t.Fatalf("published %d events, want 2", got)
	}
}

func TestDrainMarksPublishedRowsSent(t *testing.T) {
	// Without this the same rows are republished on every tick — at-least-once
	// turns into at-least-once-per-20ms.
	st := &drainStore{batches: [][]store.OutboxRecord{{record("1", "message.created")}}}

	newRelay(st, &recordingBus{}).drain(context.Background())

	if got := st.marked(); len(got) != 1 || got[0] != "1" {
		t.Errorf("marked %v, want [1]", got)
	}
}

func TestDrainCarriesTheWholeRecord(t *testing.T) {
	/*
	 * The subject routes the event, the key decides its partition (and therefore
	 * its ordering relative to the rest of that chat's traffic), and the trace
	 * headers are what connect the async consumer back to the request that
	 * staged the row. A dropped key is the worst of the three: it reorders a
	 * chat's messages, which looks like a client bug.
	 */
	st := &drainStore{batches: [][]store.OutboxRecord{{record("1", "message.created")}}}
	bus := &recordingBus{}

	newRelay(st, bus).drain(context.Background())

	published := bus.events()[0]
	if published.Subject != "message.created" {
		t.Errorf("subject = %q", published.Subject)
	}
	if published.Key != "chat-1" {
		t.Errorf("key = %q, want the chat id", published.Key)
	}
	if published.Headers["traceparent"] == "" {
		t.Error("trace context lost — the consumer's span would be orphaned")
	}
	if string(published.Data) != `{"id":"1"}` {
		t.Errorf("data = %q", published.Data)
	}
}

func TestDrainKeepsPollingWhileBatchesAreFull(t *testing.T) {
	// A full batch means there is probably more staged behind it; stopping would
	// leave a backlog to trickle out one tick at a time.
	full := make([]store.OutboxRecord, 200)
	for i := range full {
		full[i] = record(string(rune('a'+i%26))+string(rune('0'+i/26)), "message.created")
	}
	st := &drainStore{batches: [][]store.OutboxRecord{full, {record("last", "message.created")}}}
	bus := &recordingBus{}

	newRelay(st, bus).drain(context.Background())

	if got := len(bus.events()); got != 201 {
		t.Errorf("published %d events, want 201 — the relay stopped early", got)
	}
}

func TestDrainStopsOnAShortBatch(t *testing.T) {
	// Fewer rows than asked for means the table is drained; another Poll would
	// be a wasted round trip on every tick.
	st := &drainStore{batches: [][]store.OutboxRecord{{record("1", "message.created")}}}

	newRelay(st, &recordingBus{}).drain(context.Background())

	if got := st.pollCount(); got != 1 {
		t.Errorf("polled %d times, want 1", got)
	}
}

func TestDrainOfAnEmptyOutboxPublishesNothing(t *testing.T) {
	st := &drainStore{}
	bus := &recordingBus{}

	newRelay(st, bus).drain(context.Background())

	if got := len(bus.events()); got != 0 {
		t.Errorf("published %d events from an empty outbox", got)
	}
	if got := st.marked(); len(got) != 0 {
		t.Errorf("marked %v sent from an empty outbox", got)
	}
}

func TestDrainSurvivesAPollFailure(t *testing.T) {
	// The relay runs on a ticker with no error path; a propagated failure would
	// have to be handled by the caller, which has nothing useful to do with it.
	st := &drainStore{pollErr: errors.New("database is down")}

	newRelay(st, &recordingBus{}).drain(context.Background())
}

/*
 * The publish/mark ordering is the whole point of a transactional outbox. These
 * three tests pin it from both sides: nothing is marked before the bus accepted
 * it, and everything that WAS accepted is marked even when a later row fails.
 */

func TestDrainMarksOnlyWhatThePublishAccepted(t *testing.T) {
	st := &drainStore{batches: [][]store.OutboxRecord{{
		record("1", "message.created"),
		record("2", "message.deleted"), // the bus rejects this subject
		record("3", "message.created"),
	}}}
	bus := &recordingBus{failOn: "message.deleted"}

	newRelay(st, bus).drain(context.Background())

	marked := st.marked()
	if len(marked) != 1 || marked[0] != "1" {
		t.Errorf("marked %v, want only the row that was actually published", marked)
	}
}

func TestDrainLeavesAFailedRowForTheNextTick(t *testing.T) {
	// It must not be marked sent: a row marked sent but never published is a
	// message that vanishes with no retry and no error anywhere.
	st := &drainStore{batches: [][]store.OutboxRecord{{record("1", "message.deleted")}}}
	bus := &recordingBus{failOn: "message.deleted"}

	newRelay(st, bus).drain(context.Background())

	if got := st.marked(); len(got) != 0 {
		t.Errorf("marked %v sent despite the publish failing", got)
	}
}

func TestDrainStopsAtTheFirstFailure(t *testing.T) {
	// Continuing past a failed publish would deliver later rows before earlier
	// ones, breaking the per-chat ordering the key exists to preserve.
	st := &drainStore{batches: [][]store.OutboxRecord{{
		record("1", "message.deleted"),
		record("2", "message.created"),
	}}}
	bus := &recordingBus{failOn: "message.deleted"}

	newRelay(st, bus).drain(context.Background())

	if got := len(bus.events()); got != 0 {
		t.Errorf("published %d events past a failure", got)
	}
}

func TestDrainStopsWhenMarkSentFails(t *testing.T) {
	// Polling again would re-serve the same rows and publish them a second time;
	// consumers dedup, but the loop would spin until the store recovers.
	st := &drainStore{
		batches: [][]store.OutboxRecord{
			{record("1", "message.created")},
			{record("2", "message.created")},
		},
		markErr: errors.New("database is down"),
	}
	bus := &recordingBus{}

	newRelay(st, bus).drain(context.Background())

	if got := len(bus.events()); got != 1 {
		t.Errorf("published %d events, want 1 — the relay did not stop", got)
	}
}

// --------------------------------------------------------------------- Run

func TestRunDrainsWhatIsAlreadyStagedAtStartup(t *testing.T) {
	// Rows committed while the process was down have no notification coming;
	// waiting for the first tick would add startup latency for no reason.
	st := &drainStore{batches: [][]store.OutboxRecord{{record("1", "message.created")}}}
	bus := &recordingBus{}
	relay := newRelay(st, bus)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		relay.Run(ctx)
		close(done)
	}()

	waitFor(t, func() bool { return len(bus.events()) == 1 })
	cancel()
	awaitClose(t, done)
}

func TestRunStopsWhenItsContextIsCancelled(t *testing.T) {
	relay := newRelay(&drainStore{}, &recordingBus{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		relay.Run(ctx)
		close(done)
	}()

	cancel()
	awaitClose(t, done)
}

func TestRunKeepsDrainingOnItsTicker(t *testing.T) {
	// Rows staged after startup have to go out without anything else prompting
	// the relay.
	st := &drainStore{}
	bus := &recordingBus{}
	relay := newRelay(st, bus)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		relay.Run(ctx)
		close(done)
	}()
	defer func() { cancel(); awaitClose(t, done) }()

	waitFor(t, func() bool { return st.pollCount() > 0 })

	st.mu.Lock()
	st.batches = append(st.batches, []store.OutboxRecord{record("late", "message.created")})
	st.mu.Unlock()

	waitFor(t, func() bool { return len(bus.events()) == 1 })
}

// notifyStore signals new rows the way a Postgres LISTEN/NOTIFY store would.
type notifyStore struct {
	drainStore
	ch        chan struct{}
	listenErr error
}

func (n *notifyStore) Listen(context.Context) (<-chan struct{}, error) {
	if n.listenErr != nil {
		return nil, n.listenErr
	}
	return n.ch, nil
}

func TestRunDrainsOnANotification(t *testing.T) {
	// The whole point of the optional Listener: a staged row goes out on the
	// notification rather than waiting out a poll interval.
	st := &notifyStore{ch: make(chan struct{}, 1)}
	bus := &recordingBus{}
	relay := newRelay(st, bus)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		relay.Run(ctx)
		close(done)
	}()
	defer func() { cancel(); awaitClose(t, done) }()

	waitFor(t, func() bool { return st.pollCount() > 0 })

	st.mu.Lock()
	st.batches = append(st.batches, []store.OutboxRecord{record("notified", "message.created")})
	st.mu.Unlock()
	st.ch <- struct{}{}

	waitFor(t, func() bool { return len(bus.events()) == 1 })
}

func TestRunFallsBackToPollingWhenListenFails(t *testing.T) {
	// A store that advertises the capability but cannot subscribe must not leave
	// the relay waiting on a channel that never fires.
	st := &notifyStore{ch: make(chan struct{}), listenErr: errors.New("no LISTEN here")}
	st.batches = [][]store.OutboxRecord{{record("1", "message.created")}}
	bus := &recordingBus{}
	relay := newRelay(st, bus)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		relay.Run(ctx)
		close(done)
	}()
	defer func() { cancel(); awaitClose(t, done) }()

	waitFor(t, func() bool { return len(bus.events()) == 1 })
}

// ------------------------------------------------------------- WithRetention

func TestWithRetentionOverridesBothWindows(t *testing.T) {
	relay := newRelay(&drainStore{}, &recordingBus{}).WithRetention(time.Hour, 5*time.Minute)

	if relay.retain != time.Hour {
		t.Errorf("retain = %v, want 1h", relay.retain)
	}
	if relay.purgeEvery != 5*time.Minute {
		t.Errorf("purgeEvery = %v, want 5m", relay.purgeEvery)
	}
}

func TestWithRetentionIgnoresNonPositiveValues(t *testing.T) {
	// A zero would mean "collect everything immediately" and a negative one would
	// panic time.NewTicker at startup; keeping the default is the safe reading of
	// an unset option.
	relay := newRelay(&drainStore{}, &recordingBus{})
	before := relay.retain
	beforeEvery := relay.purgeEvery

	relay.WithRetention(0, 0)

	if relay.retain != before || relay.purgeEvery != beforeEvery {
		t.Errorf("zero values overrode the defaults: %v / %v", relay.retain, relay.purgeEvery)
	}
}

func TestWithRetentionReturnsTheRelayForChaining(t *testing.T) {
	relay := newRelay(&drainStore{}, &recordingBus{})
	if got := relay.WithRetention(time.Hour, time.Minute); got != relay {
		t.Error("WithRetention must return the same relay so it can be chained at construction")
	}
}

// ------------------------------------------------------------------ helpers

// waitFor polls a condition rather than sleeping a fixed time, so the test is
// neither flaky on a slow machine nor slow on a fast one.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was never met")
}

func awaitClose(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Error("Run did not return after its context was cancelled")
	}
}
