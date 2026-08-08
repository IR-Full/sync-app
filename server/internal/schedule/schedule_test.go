package schedule

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/synapse-chat/synapse/internal/message"
	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store/memory"
	"github.com/synapse-chat/synapse/pkg/id"
)

// allowChats grants or denies posting; the flag can flip mid-test to simulate a
// sender losing access between scheduling and firing.
type allowChats struct {
	mu    sync.Mutex
	allow bool
}

func (a *allowChats) CanPost(context.Context, string, string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.allow, nil
}
func (a *allowChats) set(v bool) { a.mu.Lock(); a.allow = v; a.mu.Unlock() }

// recordSender captures what the dispatcher sent.
type recordSender struct {
	mu   sync.Mutex
	sent []message.SendInput
}

func (r *recordSender) Send(_ context.Context, in message.SendInput) (*model.Message, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, in)
	return &model.Message{ID: "m", ChatID: in.ChatID}, false, nil
}
func (r *recordSender) ExpireDue(context.Context, int64, int) (int, error) { return 0, nil }
func (r *recordSender) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sent)
}

func newSvc(allow bool) (*Service, *allowChats, *recordSender) {
	chats := &allowChats{allow: allow}
	sender := &recordSender{}
	ids, _ := id.NewGenerator(11)
	return New(memory.New().Stores().Schedule, chats, sender, ids, slog.Default()), chats, sender
}

func TestScheduleAndDispatch(t *testing.T) {
	s, _, sender := newSvc(true)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	m, err := s.Schedule(ctx, Input{ChatID: "c1", SenderID: "u1", Text: "later", SendAt: now + 60_000}, now)
	if err != nil {
		t.Fatal(err)
	}
	// Not due yet → nothing dispatched.
	s.dispatchDue(ctx)
	if sender.count() != 0 {
		t.Fatal("dispatched before the scheduled time")
	}
	// Make it due by rescheduling into the past via a fresh row.
	if _, err := s.Schedule(ctx, Input{ChatID: "c1", SenderID: "u1", Text: "now", SendAt: now + 1}, now); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	s.dispatchDue(ctx)
	if sender.count() != 1 {
		t.Fatalf("dispatched %d messages, want 1", sender.count())
	}
	if sender.sent[0].Text != "now" {
		t.Fatalf("dispatched wrong message: %q", sender.sent[0].Text)
	}
	// The pending one is still listed.
	list, err := s.List(ctx, "u1", "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != m.ID {
		t.Fatalf("pending list: %+v", list)
	}
}

func TestDispatchIsIdempotent(t *testing.T) {
	s, _, sender := newSvc(true)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	if _, err := s.Schedule(ctx, Input{ChatID: "c1", SenderID: "u1", Text: "x", SendAt: now + 1}, now); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	// Two dispatcher passes (as two nodes would): the claim must send only once.
	s.dispatchDue(ctx)
	s.dispatchDue(ctx)
	if sender.count() != 1 {
		t.Fatalf("sent %d times, want exactly 1 (claim not exclusive)", sender.count())
	}
	// And the send carries a stable dedup key, so even a retried Send is safe.
	if sender.sent[0].DedupKey == "" {
		t.Fatal("dispatched send has no dedup key")
	}
}

func TestPermissionRecheckedAtFireTime(t *testing.T) {
	s, chats, sender := newSvc(true)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	if _, err := s.Schedule(ctx, Input{ChatID: "c1", SenderID: "u1", Text: "x", SendAt: now + 1}, now); err != nil {
		t.Fatal(err)
	}
	// The sender loses posting rights before the message fires.
	chats.set(false)
	time.Sleep(5 * time.Millisecond)
	s.dispatchDue(ctx)
	if sender.count() != 0 {
		t.Fatal("sent a scheduled message after the sender lost permission")
	}
}

func TestScheduleRejectsBadTimes(t *testing.T) {
	s, _, _ := newSvc(true)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	if _, err := s.Schedule(ctx, Input{ChatID: "c1", SenderID: "u1", SendAt: now - 1}, now); !errors.Is(err, ErrPastTime) {
		t.Fatalf("past time: got %v, want ErrPastTime", err)
	}
	tooFar := now + MaxHorizon.Milliseconds() + 1000
	if _, err := s.Schedule(ctx, Input{ChatID: "c1", SenderID: "u1", SendAt: tooFar}, now); !errors.Is(err, ErrTooFar) {
		t.Fatalf("too far: got %v, want ErrTooFar", err)
	}
}

func TestScheduleRequiresPostPermission(t *testing.T) {
	s, _, _ := newSvc(false)
	now := time.Now().UnixMilli()
	if _, err := s.Schedule(context.Background(), Input{ChatID: "c1", SenderID: "u1", SendAt: now + 1000}, now); !errors.Is(err, ErrForbidden) {
		t.Fatalf("got %v, want ErrForbidden", err)
	}
}

func TestCancelOnlyByOwner(t *testing.T) {
	s, _, sender := newSvc(true)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	m, err := s.Schedule(ctx, Input{ChatID: "c1", SenderID: "u1", Text: "x", SendAt: now + 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	// Someone else cannot cancel it.
	if err := s.Cancel(ctx, m.ID, "u2"); err == nil {
		t.Fatal("a non-owner cancelled a pending send")
	}
	// The owner can, and then it never fires.
	if err := s.Cancel(ctx, m.ID, "u1"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	s.dispatchDue(ctx)
	if sender.count() != 0 {
		t.Fatal("a cancelled message was still dispatched")
	}
}
