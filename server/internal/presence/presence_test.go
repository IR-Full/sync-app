package presence

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
)

// capBus records what was published so the transitions can be asserted on.
type capBus struct {
	mu     sync.Mutex
	events []eventbus.Event
}

func (b *capBus) Publish(_ context.Context, e eventbus.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, e)
	return nil
}
func (b *capBus) Subscribe(string, string, eventbus.Handler) error { return nil }
func (b *capBus) Close() error                                     { return nil }

func (b *capBus) bySubject(subject string) []eventbus.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []eventbus.Event
	for _, e := range b.events {
		if e.Subject == subject {
			out = append(out, e)
		}
	}
	return out
}

func newSvc() (*Service, *capBus) {
	bus := &capBus{}
	return New(NewMemoryBackend(), bus, time.Minute), bus
}

func ctx() context.Context { return context.Background() }

func TestOnlineStoresAndPublishes(t *testing.T) {
	s, bus := newSvc()
	if err := s.Online(ctx(), "alice"); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx(), "alice")
	if err != nil || !got.Online {
		t.Fatalf("Get: %v / %+v", err, got)
	}

	events := bus.bySubject(eventbus.SubjPresence)
	if len(events) != 1 {
		t.Fatalf("published %d presence events, want 1", len(events))
	}
	var body wire.PresenceBody
	if err := wire.Unmarshal(events[0].Data, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.UserID != "alice" || !body.Online {
		t.Fatalf("published body: %+v", body)
	}
	// Keyed by user so fanout can route the transition to that user's peers.
	if events[0].Key != "alice" {
		t.Fatalf("event key = %q, want the user id", events[0].Key)
	}
}

func TestOfflineRecordsLastSeen(t *testing.T) {
	s, bus := newSvc()
	_ = s.Online(ctx(), "alice")

	before := time.Now().UnixMilli()
	if err := s.Offline(ctx(), "alice"); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UnixMilli()

	got, _ := s.Get(ctx(), "alice")
	if got.Online {
		t.Fatal("still online after going offline")
	}
	// "Last seen" is the only thing the UI can show once someone disconnects, so
	// the timestamp has to be real rather than zero.
	if got.LastSeenMs < before || got.LastSeenMs > after {
		t.Fatalf("last seen %d outside [%d, %d]", got.LastSeenMs, before, after)
	}

	events := bus.bySubject(eventbus.SubjPresence)
	if len(events) != 2 {
		t.Fatalf("published %d transitions, want 2 (online then offline)", len(events))
	}
	var body wire.PresenceBody
	_ = wire.Unmarshal(events[1].Data, &body)
	if body.Online {
		t.Fatal("offline transition published as online")
	}
}

// A heartbeat refreshes the marker without republishing: presence flips on every
// ping otherwise, and fanout would multiply that by the user's whole audience.
func TestHeartbeatDoesNotRepublish(t *testing.T) {
	s, bus := newSvc()
	_ = s.Online(ctx(), "alice")
	for i := 0; i < 5; i++ {
		if err := s.Heartbeat(ctx(), "alice"); err != nil {
			t.Fatal(err)
		}
	}

	if n := len(bus.bySubject(eventbus.SubjPresence)); n != 1 {
		t.Fatalf("published %d presence events after 5 heartbeats, want 1", n)
	}
	got, _ := s.Get(ctx(), "alice")
	if !got.Online {
		t.Fatal("heartbeat lost the online marker")
	}
}

func TestGetUnknownUserReadsOffline(t *testing.T) {
	s, _ := newSvc()
	// Someone who has never connected is offline, not an error: that is the
	// common case for every contact a user has not seen today.
	got, err := s.Get(ctx(), "stranger")
	if err != nil {
		t.Fatalf("Get for an unknown user: %v", err)
	}
	if got.Online || got.UserID != "stranger" {
		t.Fatalf("unexpected presence: %+v", got)
	}
}

func TestTypingIsRelayedNotStored(t *testing.T) {
	s, bus := newSvc()
	if err := s.Typing(ctx(), "chat1", "alice", true); err != nil {
		t.Fatal(err)
	}

	events := bus.bySubject(eventbus.SubjTyping)
	if len(events) != 1 {
		t.Fatalf("published %d typing events, want 1", len(events))
	}
	// Keyed by CHAT, unlike presence: a typing indicator is addressed to the
	// conversation, not to the typist's contacts.
	if events[0].Key != "chat1" {
		t.Fatalf("typing keyed by %q, want the chat id", events[0].Key)
	}
	var body wire.TypingBody
	_ = wire.Unmarshal(events[0].Data, &body)
	if body.ChatID != "chat1" || body.UserID != "alice" || !body.Active {
		t.Fatalf("typing body: %+v", body)
	}

	// Typing is fire-and-forget — it must not leave presence state behind.
	got, _ := s.Get(ctx(), "alice")
	if got.Online {
		t.Fatal("typing marked the user online as a side effect")
	}
}

func TestTypingStopIsRelayed(t *testing.T) {
	s, bus := newSvc()
	_ = s.Typing(ctx(), "chat1", "alice", false)

	var body wire.TypingBody
	_ = wire.Unmarshal(bus.bySubject(eventbus.SubjTyping)[0].Data, &body)
	if body.Active {
		t.Fatal("a stop-typing signal was published as active")
	}
}

func TestNewAppliesADefaultTTL(t *testing.T) {
	// A zero TTL would mean the online marker never expires, so a node that dies
	// would leave its users online forever.
	s := New(NewMemoryBackend(), &capBus{}, 0)
	if s.ttl <= 0 {
		t.Fatalf("ttl = %v, want a non-zero default", s.ttl)
	}
}

func TestServiceIsSafeUnderConcurrency(t *testing.T) {
	s, _ := newSvc()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Online(ctx(), "alice")
			_ = s.Heartbeat(ctx(), "alice")
			_, _ = s.Get(ctx(), "alice")
			_ = s.Typing(ctx(), "chat1", "alice", true)
			_ = s.Offline(ctx(), "alice")
		}()
	}
	wg.Wait() // the assertion is the race detector staying quiet
}
