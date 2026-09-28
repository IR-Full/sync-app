package schedule

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/message"
	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/internal/store/memory"
	"github.com/SyncApp-chat/SyncApp/pkg/id"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// ------------------------------------------------------------------- Run

// countingSender records dispatches and expiry sweeps, and can fail either.
type countingSender struct {
	mu        sync.Mutex
	sent      []message.SendInput
	expiries  atomic.Int32
	expired   int
	expireErr error
	sendErr   error
}

func (c *countingSender) Send(_ context.Context, in message.SendInput) (*model.Message, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sendErr != nil {
		return nil, false, c.sendErr
	}
	c.sent = append(c.sent, in)
	return &model.Message{ID: "m", ChatID: in.ChatID}, false, nil
}

func (c *countingSender) ExpireDue(context.Context, int64, int) (int, error) {
	c.expiries.Add(1)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.expired, c.expireErr
}

func (c *countingSender) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sent)
}

func runFixture(t *testing.T, allow bool) (*Service, *countingSender) {
	t.Helper()
	ids, _ := id.NewGenerator(11)
	sender := &countingSender{}
	svc := New(memory.New().Stores().Schedule, &allowChats{allow: allow}, sender, ids, quietLog())
	return svc, sender
}

// runFor starts the loop and stops it once `check` is satisfied or the deadline
// passes, so a test never hangs on a loop that stopped ticking.
func runFor(t *testing.T, svc *Service, tick time.Duration, check func() bool) bool {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.Run(ctx, tick)
		close(done)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Run did not return after its context was cancelled")
		}
	}()

	deadline := time.After(2 * time.Second)
	for {
		if check() {
			return true
		}
		select {
		case <-deadline:
			return false
		case <-time.After(2 * time.Millisecond):
		}
	}
}

func TestRunDispatchesDueMessages(t *testing.T) {
	svc, sender := runFixture(t, true)
	now := time.Now().UnixMilli()
	if _, err := svc.Schedule(context.Background(), Input{
		ChatID: "c1", SenderID: "u1", Text: "now", SendAt: now + 1,
	}, now); err != nil {
		t.Fatal(err)
	}

	if !runFor(t, svc, 5*time.Millisecond, func() bool { return sender.count() > 0 }) {
		t.Fatal("the loop never dispatched a due message")
	}
}

func TestRunSweepsSelfDestructedMessages(t *testing.T) {
	// The reaper shares the dispatcher's tick. If it were only wired into some
	// other loop, expired messages would linger until something else ran.
	svc, sender := runFixture(t, true)

	if !runFor(t, svc, 5*time.Millisecond, func() bool { return sender.expiries.Load() > 0 }) {
		t.Fatal("the loop never ran the self-destruct sweep")
	}
}

func TestRunStopsWhenItsContextIsCancelled(t *testing.T) {
	// A loop that ignores cancellation keeps hitting the database after shutdown
	// has begun, which is how a "clean" shutdown ends in a connection error.
	svc, _ := runFixture(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.Run(ctx, 5*time.Millisecond)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run ignored its cancelled context")
	}
}

func TestRunReturnsImmediatelyForAnAlreadyCancelledContext(t *testing.T) {
	svc, _ := runFixture(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		svc.Run(ctx, time.Hour)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run blocked on a context that was cancelled before it started")
	}
}

func TestRunSubstitutesADefaultTick(t *testing.T) {
	// A zero or negative tick would panic time.NewTicker and take the process
	// down at startup — the worst possible place for a misconfiguration.
	svc, _ := runFixture(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.Run(ctx, 0)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not survive a zero tick")
	}
}

func TestRunKeepsGoingWhenTheSweepFails(t *testing.T) {
	// A transient database error must not end the loop: the next tick should try
	// again rather than leave the node permanently not dispatching.
	svc, sender := runFixture(t, true)
	sender.mu.Lock()
	sender.expireErr = errors.New("database is down")
	sender.mu.Unlock()

	now := time.Now().UnixMilli()
	if _, err := svc.Schedule(context.Background(), Input{
		ChatID: "c1", SenderID: "u1", Text: "now", SendAt: now + 1,
	}, now); err != nil {
		t.Fatal(err)
	}

	if !runFor(t, svc, 5*time.Millisecond, func() bool { return sender.expiries.Load() > 2 }) {
		t.Fatal("the loop stopped after the sweep returned an error")
	}
}

// -------------------------------------------------------------- dispatchDue

func TestDispatchCarriesTheWholePayload(t *testing.T) {
	/*
	 * A scheduled send goes out through the ordinary write path, so anything
	 * dropped here is dropped *only* for scheduled messages — an attachment that
	 * silently vanishes an hour after the user queued it, with no error anywhere.
	 */
	svc, sender := runFixture(t, true)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	if _, err := svc.Schedule(ctx, Input{
		ChatID: "c1", SenderID: "u1", Text: "hello",
		MediaRef:   "media-1",
		Attachment: &model.Attachment{Kind: model.AttachImage, MediaRef: "media-1", Width: 10},
		ReplyTo:    "m7",
		TTLSeconds: 60,
		SendAt:     now + 1,
	}, now); err != nil {
		t.Fatal(err)
	}

	time.Sleep(5 * time.Millisecond)
	svc.dispatchDue(ctx)

	if sender.count() != 1 {
		t.Fatalf("dispatched %d messages, want 1", sender.count())
	}
	sent := sender.sent[0]
	if sent.Text != "hello" || sent.MediaRef != "media-1" || sent.ReplyTo != "m7" || sent.TTLSeconds != 60 {
		t.Errorf("payload lost fields: %+v", sent)
	}
	if sent.Attachment == nil || sent.Attachment.MediaRef != "media-1" {
		t.Errorf("attachment = %+v, want the scheduled one", sent.Attachment)
	}
}

func TestDispatchDedupKeyIsDerivedFromTheScheduledID(t *testing.T) {
	// Stable across retries, which is what makes a redelivered claim safe: the
	// message store resolves the repeat to the existing row instead of posting a
	// second copy.
	svc, sender := runFixture(t, true)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	m, err := svc.Schedule(ctx, Input{ChatID: "c1", SenderID: "u1", Text: "x", SendAt: now + 1}, now)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(5 * time.Millisecond)
	svc.dispatchDue(ctx)

	if want := "sched:" + m.ID; sender.sent[0].DedupKey != want {
		t.Errorf("dedup key = %q, want %q", sender.sent[0].DedupKey, want)
	}
}

func TestDispatchKeepsGoingAfterOneSendFails(t *testing.T) {
	// One bad message must not strand the rest of the batch: they were all
	// claimed together, so a bail-out would leave them marked sent and never
	// delivered.
	svc, sender := runFixture(t, true)
	sender.mu.Lock()
	sender.sendErr = errors.New("chat is gone")
	sender.mu.Unlock()

	ctx := context.Background()
	now := time.Now().UnixMilli()
	for i := 0; i < 3; i++ {
		if _, err := svc.Schedule(ctx, Input{ChatID: "c1", SenderID: "u1", Text: "x", SendAt: now + 1}, now); err != nil {
			t.Fatal(err)
		}
	}

	time.Sleep(5 * time.Millisecond)
	// The assertion is that this returns at all rather than panicking or aborting
	// the loop; the sender records nothing because every send failed.
	svc.dispatchDue(ctx)
}

func TestDispatchSurvivesAStoreFailure(t *testing.T) {
	// The claim runs on every tick; a failure has to be logged and retried, not
	// propagated into a loop that has no error path.
	ids, _ := id.NewGenerator(11)
	svc := New(failingSchedule{claimErr: errors.New("db down")}, &allowChats{allow: true},
		&countingSender{}, ids, quietLog())

	svc.dispatchDue(context.Background())
}

func TestDispatchOfAnEmptyQueueSendsNothing(t *testing.T) {
	svc, sender := runFixture(t, true)

	svc.dispatchDue(context.Background())

	if sender.count() != 0 {
		t.Errorf("dispatched %d messages from an empty queue", sender.count())
	}
}

// --------------------------------------------------------------- purgeSent

// countingPurge records how the purge loop paged through its work.
type countingPurge struct {
	store.ScheduleStore
	mu      sync.Mutex
	batches []int
	remain  int
	err     error
}

func (p *countingPurge) PurgeSentScheduled(_ context.Context, _ int64, limit int) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return 0, p.err
	}
	n := p.remain
	if n > limit {
		n = limit
	}
	p.remain -= n
	p.batches = append(p.batches, n)
	return n, nil
}

func (p *countingPurge) calls() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]int(nil), p.batches...)
}

func newPurgeService(t *testing.T, st store.ScheduleStore) *Service {
	t.Helper()
	ids, _ := id.NewGenerator(11)
	return New(st, &allowChats{allow: true}, &countingSender{}, ids, quietLog())
}

func TestPurgeStopsOnAShortBatch(t *testing.T) {
	// A batch smaller than the limit means the table is drained. Continuing would
	// spin on an empty table for as long as the process lives.
	purge := &countingPurge{remain: 10}
	newPurgeService(t, purge).purgeSent(context.Background())

	if got := purge.calls(); len(got) != 1 || got[0] != 10 {
		t.Errorf("purge calls = %v, want a single batch of 10", got)
	}
}

func TestPurgePagesUntilTheTableIsDrained(t *testing.T) {
	// A single bounded delete would leave the rest behind forever on a busy
	// instance, and the table only ever grows.
	purge := &countingPurge{remain: purgeBatch*2 + 3}
	newPurgeService(t, purge).purgeSent(context.Background())

	got := purge.calls()
	if len(got) != 3 {
		t.Fatalf("purge made %d calls (%v), want 3", len(got), got)
	}
	if got[0] != purgeBatch || got[1] != purgeBatch || got[2] != 3 {
		t.Errorf("batches = %v, want [%d %d 3]", got, purgeBatch, purgeBatch)
	}
}

func TestPurgeStopsOnAnError(t *testing.T) {
	// Retrying inside the loop against a database that is down would spin at full
	// speed; the next purge tick is the right place to try again.
	purge := &countingPurge{err: errors.New("db down")}
	newPurgeService(t, purge).purgeSent(context.Background())

	if got := purge.calls(); len(got) != 0 {
		t.Errorf("purge kept going after an error: %v", got)
	}
}

func TestPurgeHandlesAnAlreadyEmptyTable(t *testing.T) {
	purge := &countingPurge{remain: 0}
	newPurgeService(t, purge).purgeSent(context.Background())

	if got := purge.calls(); len(got) != 1 || got[0] != 0 {
		t.Errorf("purge calls = %v, want a single empty batch", got)
	}
}

// -------------------------------------------------------------------- List

func TestListReturnsOnlyTheCallersOwnPendingSends(t *testing.T) {
	// The pending table holds future traffic for every user; scoping it to the
	// caller is the only thing keeping one user's drafts out of another's list.
	svc, _ := runFixture(t, true)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	if _, err := svc.Schedule(ctx, Input{ChatID: "c1", SenderID: "u1", Text: "mine", SendAt: now + 60_000}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Schedule(ctx, Input{ChatID: "c1", SenderID: "u2", Text: "theirs", SendAt: now + 60_000}, now); err != nil {
		t.Fatal(err)
	}

	list, err := svc.List(ctx, "u1", "c1")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range list {
		if m.SenderID != "u1" {
			t.Errorf("list leaked %s's pending send to u1", m.SenderID)
		}
	}
	if len(list) != 1 {
		t.Errorf("want 1 pending send, got %d", len(list))
	}
}

func TestListIsScopedToTheChat(t *testing.T) {
	svc, _ := runFixture(t, true)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	if _, err := svc.Schedule(ctx, Input{ChatID: "c1", SenderID: "u1", Text: "here", SendAt: now + 60_000}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Schedule(ctx, Input{ChatID: "c2", SenderID: "u1", Text: "elsewhere", SendAt: now + 60_000}, now); err != nil {
		t.Fatal(err)
	}

	list, err := svc.List(ctx, "u1", "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Text != "here" {
		t.Errorf("list = %+v, want only the c1 message", list)
	}
}

// ---------------------------------------------------------------- Schedule

func TestScheduleAcceptsTheHorizonExactly(t *testing.T) {
	// The boundary is where an off-by-one lives, and rejecting a legitimate
	// "same day next year" would be a silent usability bug.
	svc, _ := runFixture(t, true)
	now := time.Now().UnixMilli()

	if _, err := svc.Schedule(context.Background(), Input{
		ChatID: "c1", SenderID: "u1", Text: "x", SendAt: now + MaxHorizon.Milliseconds(),
	}, now); err != nil {
		t.Errorf("a send exactly at the horizon was rejected: %v", err)
	}
}

func TestScheduleRejectsTheCurrentInstant(t *testing.T) {
	// "Now" is not the future: a row that is already due at creation would be
	// dispatched by the very next tick, which is a plain send with extra steps.
	svc, _ := runFixture(t, true)
	now := time.Now().UnixMilli()

	if _, err := svc.Schedule(context.Background(), Input{
		ChatID: "c1", SenderID: "u1", Text: "x", SendAt: now,
	}, now); !errors.Is(err, ErrPastTime) {
		t.Errorf("got %v, want ErrPastTime", err)
	}
}

func TestScheduleChecksPermissionBeforeWriting(t *testing.T) {
	// Otherwise a user with no posting rights could fill the pending table.
	svc := New(failingSchedule{createErr: errors.New("must not be reached")},
		&allowChats{allow: false}, &countingSender{}, mustGenerator(t), quietLog())
	now := time.Now().UnixMilli()

	if _, err := svc.Schedule(context.Background(), Input{
		ChatID: "c1", SenderID: "u1", Text: "x", SendAt: now + 60_000,
	}, now); !errors.Is(err, ErrForbidden) {
		t.Errorf("got %v, want ErrForbidden", err)
	}
}

func TestScheduleValidatesTimeBeforeCheckingPermission(t *testing.T) {
	// A bad time is the caller's mistake and costs nothing to detect; reaching
	// for the chat service first would spend a lookup on every typo.
	svc := New(memory.New().Stores().Schedule, refusingChats{}, &countingSender{},
		mustGenerator(t), quietLog())
	now := time.Now().UnixMilli()

	if _, err := svc.Schedule(context.Background(), Input{
		ChatID: "c1", SenderID: "u1", Text: "x", SendAt: now - 1,
	}, now); !errors.Is(err, ErrPastTime) {
		t.Errorf("got %v, want ErrPastTime before any permission check", err)
	}
}

func TestSchedulePropagatesAStoreFailure(t *testing.T) {
	svc := New(failingSchedule{createErr: errors.New("db down")}, &allowChats{allow: true},
		&countingSender{}, mustGenerator(t), quietLog())
	now := time.Now().UnixMilli()

	if _, err := svc.Schedule(context.Background(), Input{
		ChatID: "c1", SenderID: "u1", Text: "x", SendAt: now + 60_000,
	}, now); err == nil {
		t.Error("a failed write reported success")
	}
}

// ------------------------------------------------------------------ doubles

// refusingChats fails the permission lookup, so a test can tell "was it even
// consulted?" apart from "did it say no?".
type refusingChats struct{}

func (refusingChats) CanPost(context.Context, string, string) (bool, error) {
	return false, errors.New("CanPost should not have been consulted")
}

// failingSchedule is a ScheduleStore whose operations fail on demand. The
// embedded interface is nil, so any method a test does not configure panics
// rather than silently returning a zero value.
type failingSchedule struct {
	store.ScheduleStore
	createErr error
	claimErr  error
}

func (f failingSchedule) CreateScheduled(context.Context, *model.ScheduledMessage) error {
	return f.createErr
}

func (f failingSchedule) ClaimDueScheduled(context.Context, int64, int) ([]*model.ScheduledMessage, error) {
	return nil, f.claimErr
}

func mustGenerator(t *testing.T) *id.Generator {
	t.Helper()
	ids, err := id.NewGenerator(11)
	if err != nil {
		t.Fatal(err)
	}
	return ids
}
