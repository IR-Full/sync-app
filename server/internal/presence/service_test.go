package presence

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
)

// recordingBus captures what the service published and can fail on demand.
type recordingBus struct {
	mu        sync.Mutex
	published []eventbus.Event
	err       error
}

func (b *recordingBus) Publish(_ context.Context, e eventbus.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return b.err
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

func (b *recordingBus) only(t *testing.T) eventbus.Event {
	t.Helper()
	events := b.events()
	if len(events) != 1 {
		t.Fatalf("published %d events, want exactly 1", len(events))
	}
	return events[0]
}

// failingBackend reports a storage failure, so a test can tell "the write was
// attempted" apart from "the write succeeded".
type failingBackend struct {
	Backend
	onlineErr  error
	offlineErr error
}

func (f failingBackend) SetOnline(context.Context, string, time.Duration) error {
	return f.onlineErr
}

func (f failingBackend) SetOffline(context.Context, string, int64) error { return f.offlineErr }

// ttlBackend records the TTL it was handed, which is the only thing standing
// between a crashed gateway and a user who appears online forever.
type ttlBackend struct {
	Backend
	mu   sync.Mutex
	ttls []time.Duration
}

func (b *ttlBackend) SetOnline(_ context.Context, _ string, ttl time.Duration) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ttls = append(b.ttls, ttl)
	return nil
}

func (b *ttlBackend) seen() []time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]time.Duration(nil), b.ttls...)
}

func newService(bus eventbus.Bus) (*Service, Backend) {
	backend := NewMemoryBackend()
	return New(backend, bus, time.Minute), backend
}

func decodePresence(t *testing.T, e eventbus.Event) wire.PresenceBody {
	t.Helper()
	var body wire.PresenceBody
	if err := wire.Unmarshal(e.Data, &body); err != nil {
		t.Fatalf("unmarshal presence: %v", err)
	}
	return body
}

// ------------------------------------------------------------------- Online

func TestOnlineStoresTheFlag(t *testing.T) {
	svc, backend := newService(&recordingBus{})

	if err := svc.Online(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}

	got, err := backend.Get(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Online {
		t.Error("the user was not recorded as online")
	}
}

func TestOnlinePublishesTheTransition(t *testing.T) {
	// Presence only reaches a peer through this event — there is no message that
	// asks for someone's status — so a stored flag that was never published is
	// invisible to every other client.
	bus := &recordingBus{}
	svc, _ := newService(bus)

	if err := svc.Online(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}

	body := decodePresence(t, bus.only(t))
	if body.UserID != "u1" || !body.Online {
		t.Errorf("published %+v, want u1 online", body)
	}
}

func TestOnlineKeysTheEventOnTheUser(t *testing.T) {
	// Fanout partitions by key; presence for one user has to stay ordered with
	// that user's other transitions, or a stale "offline" can overtake a fresh
	// "online" and leave them dark.
	bus := &recordingBus{}
	svc, _ := newService(bus)

	if err := svc.Online(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}

	if got := bus.only(t).Key; got != "u1" {
		t.Errorf("event key = %q, want the user id", got)
	}
}

func TestOnlineCarriesATimestamp(t *testing.T) {
	// The client renders "last seen" from it, and a zero would show 1970.
	bus := &recordingBus{}
	svc, _ := newService(bus)
	before := time.Now().UnixMilli()

	if err := svc.Online(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}

	if got := decodePresence(t, bus.only(t)).LastSeenMs; got < before {
		t.Errorf("timestamp = %d, want at least %d", got, before)
	}
}

func TestOnlinePublishesNothingWhenTheStoreFails(t *testing.T) {
	// Announcing a state that was never stored means the next Get disagrees with
	// what every client was just told.
	bus := &recordingBus{}
	svc := New(failingBackend{onlineErr: errors.New("redis is down")}, bus, time.Minute)

	if err := svc.Online(context.Background(), "u1"); err == nil {
		t.Fatal("a failed write reported success")
	}
	if got := len(bus.events()); got != 0 {
		t.Errorf("published %d events despite the write failing", got)
	}
}

func TestOnlinePropagatesAPublishFailure(t *testing.T) {
	bus := &recordingBus{err: errors.New("bus is down")}
	svc, _ := newService(bus)

	if err := svc.Online(context.Background(), "u1"); err == nil {
		t.Error("a failed publish reported success")
	}
}

// ------------------------------------------------------------------ Offline

func TestOfflineStoresTheFlagAndLastSeen(t *testing.T) {
	svc, backend := newService(&recordingBus{})
	before := time.Now().UnixMilli()

	if err := svc.Offline(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}

	got, err := backend.Get(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Online {
		t.Error("the user is still recorded as online")
	}
	if got.LastSeenMs < before {
		t.Errorf("last seen = %d, want at least %d", got.LastSeenMs, before)
	}
}

func TestOfflinePublishesTheTransition(t *testing.T) {
	bus := &recordingBus{}
	svc, _ := newService(bus)

	if err := svc.Offline(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}

	body := decodePresence(t, bus.only(t))
	if body.UserID != "u1" || body.Online {
		t.Errorf("published %+v, want u1 offline", body)
	}
}

func TestOfflinePublishesTheSameTimestampItStored(t *testing.T) {
	/*
	 * The stored value and the announced one have to be the same instant. Two
	 * separate `now()` calls would disagree by a millisecond or two, which is
	 * harmless-looking until a client that reconnects and re-reads presence sees
	 * "last seen" jump backwards relative to what it was just told.
	 */
	bus := &recordingBus{}
	svc, backend := newService(bus)

	if err := svc.Offline(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}

	stored, err := backend.Get(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if got := decodePresence(t, bus.only(t)).LastSeenMs; got != stored.LastSeenMs {
		t.Errorf("announced %d but stored %d", got, stored.LastSeenMs)
	}
}

func TestOfflinePublishesNothingWhenTheStoreFails(t *testing.T) {
	bus := &recordingBus{}
	svc := New(failingBackend{offlineErr: errors.New("redis is down")}, bus, time.Minute)

	if err := svc.Offline(context.Background(), "u1"); err == nil {
		t.Fatal("a failed write reported success")
	}
	if got := len(bus.events()); got != 0 {
		t.Errorf("published %d events despite the write failing", got)
	}
}

// ---------------------------------------------------------------- Heartbeat

func TestHeartbeatRefreshesTheTTL(t *testing.T) {
	/*
	 * The TTL is what makes a crashed gateway self-correcting: the "online" key
	 * expires on its own and the user goes dark without any cleanup job. A
	 * heartbeat that did not refresh it would make every long-lived connection
	 * appear to drop off exactly one TTL after connecting.
	 */
	backend := &ttlBackend{}
	svc := New(backend, &recordingBus{}, 30*time.Second)

	for i := 0; i < 3; i++ {
		if err := svc.Heartbeat(context.Background(), "u1"); err != nil {
			t.Fatal(err)
		}
	}

	ttls := backend.seen()
	if len(ttls) != 3 {
		t.Fatalf("refreshed %d times, want 3", len(ttls))
	}
	for _, ttl := range ttls {
		if ttl != 30*time.Second {
			t.Errorf("TTL = %v, want the configured 30s", ttl)
		}
	}
}

func TestHeartbeatPublishesNothing(t *testing.T) {
	// It fires on every ping — roughly once per 20 seconds per connection — so
	// announcing it would multiply presence traffic by the size of the fleet for
	// a state that has not changed.
	bus := &recordingBus{}
	svc, _ := newService(bus)

	if err := svc.Heartbeat(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}

	if got := len(bus.events()); got != 0 {
		t.Errorf("a heartbeat published %d events", got)
	}
}

func TestHeartbeatPropagatesAStoreFailure(t *testing.T) {
	svc := New(failingBackend{onlineErr: errors.New("redis is down")}, &recordingBus{}, time.Minute)

	if err := svc.Heartbeat(context.Background(), "u1"); err == nil {
		t.Error("a failed refresh reported success")
	}
}

// ------------------------------------------------------------------- Typing

func TestTypingPublishesTheIndicator(t *testing.T) {
	bus := &recordingBus{}
	svc, _ := newService(bus)

	if err := svc.Typing(context.Background(), "c1", "u1", true); err != nil {
		t.Fatal(err)
	}

	var body wire.TypingBody
	if err := wire.Unmarshal(bus.only(t).Data, &body); err != nil {
		t.Fatal(err)
	}
	if body.ChatID != "c1" || body.UserID != "u1" || !body.Active {
		t.Errorf("published %+v", body)
	}
}

func TestTypingCarriesAStop(t *testing.T) {
	// The stop frame is as droppable as the start, but when it does get through
	// it has to say `active: false` rather than simply be absent.
	bus := &recordingBus{}
	svc, _ := newService(bus)

	if err := svc.Typing(context.Background(), "c1", "u1", false); err != nil {
		t.Fatal(err)
	}

	var body wire.TypingBody
	if err := wire.Unmarshal(bus.only(t).Data, &body); err != nil {
		t.Fatal(err)
	}
	if body.Active {
		t.Error("a stop was published as a start")
	}
}

func TestTypingKeysTheEventOnTheChat(t *testing.T) {
	// Unlike presence, typing is per chat: keying it on the user would send it
	// to the wrong partition and reorder it against the chat's own messages.
	bus := &recordingBus{}
	svc, _ := newService(bus)

	if err := svc.Typing(context.Background(), "c1", "u1", true); err != nil {
		t.Fatal(err)
	}

	if got := bus.only(t).Key; got != "c1" {
		t.Errorf("event key = %q, want the chat id", got)
	}
}

func TestTypingIsNeverPersisted(t *testing.T) {
	// It is fire-and-forget by design; writing it would turn the highest-churn
	// signal in the system into storage traffic.
	bus := &recordingBus{}
	svc, backend := newService(bus)

	if err := svc.Typing(context.Background(), "c1", "u1", true); err != nil {
		t.Fatal(err)
	}

	got, err := backend.Get(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Online {
		t.Error("typing wrote presence state")
	}
}

func TestTypingPropagatesAPublishFailure(t *testing.T) {
	svc, _ := newService(&recordingBus{err: errors.New("bus is down")})

	if err := svc.Typing(context.Background(), "c1", "u1", true); err == nil {
		t.Error("a failed publish reported success")
	}
}

// ---------------------------------------------------------------------- Get

func TestGetReportsAnUnknownUserAsOffline(t *testing.T) {
	// Presence is ephemeral and lost on restart, so "never heard of them" is the
	// common case rather than an error.
	svc, _ := newService(&recordingBus{})

	got, err := svc.Get(context.Background(), "nobody")
	if err != nil {
		t.Fatal(err)
	}
	if got.Online {
		t.Error("an unknown user was reported online")
	}
	if got.UserID != "nobody" {
		t.Errorf("user id = %q, want the one that was asked for", got.UserID)
	}
}

func TestGetReflectsTheLatestTransition(t *testing.T) {
	svc, _ := newService(&recordingBus{})
	ctx := context.Background()

	if err := svc.Online(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Get(ctx, "u1"); !got.Online {
		t.Error("Get did not see the online transition")
	}

	if err := svc.Offline(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Get(ctx, "u1"); got.Online {
		t.Error("Get did not see the offline transition")
	}
}

// ---------------------------------------------------------------------- New

func TestNewSubstitutesADefaultTTL(t *testing.T) {
	// A zero TTL in Redis means "no expiry", which is precisely the bug the TTL
	// exists to prevent: a crashed gateway would leave its users online forever.
	backend := &ttlBackend{}
	svc := New(backend, &recordingBus{}, 0)

	if err := svc.Heartbeat(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}

	ttls := backend.seen()
	if len(ttls) != 1 || ttls[0] <= 0 {
		t.Fatalf("TTL = %v, want a positive default", ttls)
	}
}

func TestNewHonoursAConfiguredTTL(t *testing.T) {
	backend := &ttlBackend{}
	svc := New(backend, &recordingBus{}, 90*time.Second)

	if err := svc.Heartbeat(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}

	if got := backend.seen(); len(got) != 1 || got[0] != 90*time.Second {
		t.Errorf("TTL = %v, want 90s", got)
	}
}

// --------------------------------------------------------- memory backend

func TestMemoryBackendKeepsUsersIndependent(t *testing.T) {
	backend := NewMemoryBackend()
	ctx := context.Background()

	if err := backend.SetOnline(ctx, "u1", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := backend.SetOffline(ctx, "u2", 1_700); err != nil {
		t.Fatal(err)
	}

	if got, _ := backend.Get(ctx, "u1"); !got.Online {
		t.Error("u1 should be online")
	}
	if got, _ := backend.Get(ctx, "u2"); got.Online {
		t.Error("u2 should be offline")
	}
}

func TestMemoryBackendStoresTheGivenLastSeen(t *testing.T) {
	// The caller supplies the instant so it matches what was announced; an
	// internally generated one would drift from the published event.
	backend := NewMemoryBackend()
	ctx := context.Background()

	if err := backend.SetOffline(ctx, "u1", 1_700_000); err != nil {
		t.Fatal(err)
	}

	if got, _ := backend.Get(ctx, "u1"); got.LastSeenMs != 1_700_000 {
		t.Errorf("last seen = %d, want the value that was stored", got.LastSeenMs)
	}
}

func TestMemoryBackendIsSafeForConcurrentUse(t *testing.T) {
	// One gateway process handles many connections at once, and every ping
	// writes; without the lock this is a data race rather than a wrong answer.
	backend := NewMemoryBackend()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = backend.SetOnline(ctx, "u1", time.Minute)
			_, _ = backend.Get(ctx, "u1")
			_ = backend.SetOffline(ctx, "u1", 1)
		}()
	}
	wg.Wait()
}

func TestServiceIsSafeForConcurrentUse(t *testing.T) {
	svc, _ := newService(&recordingBus{})
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = svc.Online(ctx, "u1")
			_ = svc.Heartbeat(ctx, "u1")
			_ = svc.Typing(ctx, "c1", "u1", true)
			_ = svc.Offline(ctx, "u1")
			_, _ = svc.Get(ctx, "u1")
		}()
	}
	wg.Wait()
}

// ------------------------------------------------- presence model contract

func TestPresenceModelRoundTripsThroughTheWire(t *testing.T) {
	// The domain type and the wire body are separate so the protocol can change
	// without a storage migration — which also makes a dropped field easy.
	original := model.Presence{UserID: "u1", Online: true, LastSeenMs: 1_700_000}

	encoded := wire.Marshal(wire.PresenceBody{
		UserID: original.UserID, Online: original.Online, LastSeenMs: original.LastSeenMs,
	})
	var body wire.PresenceBody
	if err := wire.Unmarshal(encoded, &body); err != nil {
		t.Fatal(err)
	}

	if body.UserID != original.UserID || body.Online != original.Online ||
		body.LastSeenMs != original.LastSeenMs {
		t.Errorf("round-trip lost data: %+v", body)
	}
}
