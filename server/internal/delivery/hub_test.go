package delivery

import (
	"fmt"
	"sync"
	"testing"

	"github.com/IR-Full/sync-app/server/pkg/wire"
)

// fakeSink is a connection that records what it was handed. `full` makes Send
// report a saturated queue, which is how a slow client is modelled.
type fakeSink struct {
	mu       sync.Mutex
	device   string
	full     bool
	received []Delivery
}

func (f *fakeSink) DeviceID() string { return f.device }

func (f *fakeSink) Send(d Delivery) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.full {
		return false
	}
	f.received = append(f.received, d)
	return true
}

func (f *fakeSink) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.received)
}

func msg(text string) Delivery {
	return Delivery{Type: wire.MsgNew, Body: text}
}

func TestRouteReachesEveryDeviceOfAUser(t *testing.T) {
	h := NewHub()
	phone := &fakeSink{device: "phone"}
	laptop := &fakeSink{device: "laptop"}
	h.Register("alice", phone)
	h.Register("alice", laptop)

	// Multi-device delivery is the point: a message goes to every device the user
	// has connected, not just the most recent one.
	if n := h.Route("alice", msg("hi")); n != 2 {
		t.Fatalf("routed to %d sinks, want 2", n)
	}
	if phone.count() != 1 || laptop.count() != 1 {
		t.Fatalf("delivery missed a device: phone=%d laptop=%d", phone.count(), laptop.count())
	}
}

func TestRouteToOfflineUserReportsZero(t *testing.T) {
	h := NewHub()
	// Zero is the signal the caller turns into a push notification, so it has to
	// be distinguishable from a successful local delivery.
	if n := h.Route("nobody", msg("hi")); n != 0 {
		t.Fatalf("routed to %d sinks for an offline user", n)
	}
}

func TestRouteCountsOnlyAcceptedSends(t *testing.T) {
	h := NewHub()
	ok := &fakeSink{device: "ok"}
	saturated := &fakeSink{device: "slow", full: true}
	h.Register("alice", ok)
	h.Register("alice", saturated)

	// A client whose queue is full has NOT received the message, and counting it
	// would suppress the push that should replace it.
	if n := h.Route("alice", msg("hi")); n != 1 {
		t.Fatalf("routed to %d sinks, want 1 (the saturated one must not count)", n)
	}
}

func TestRegisterReplacesTheSameDevice(t *testing.T) {
	h := NewHub()
	first := &fakeSink{device: "phone"}
	second := &fakeSink{device: "phone"}
	h.Register("alice", first)
	h.Register("alice", second)

	// A reconnect from the same device must replace the stale sink, not add a
	// second one — otherwise every reconnect doubles the delivery.
	if n := h.Route("alice", msg("hi")); n != 1 {
		t.Fatalf("routed to %d sinks after a reconnect, want 1", n)
	}
	if first.count() != 0 {
		t.Fatal("the replaced connection still received the message")
	}
	if second.count() != 1 {
		t.Fatal("the current connection did not receive the message")
	}
}

func TestUnregisterRemovesTheSink(t *testing.T) {
	h := NewHub()
	s := &fakeSink{device: "phone"}
	unregister := h.Register("alice", s)

	if !h.IsOnline("alice") {
		t.Fatal("user not online after registering")
	}
	unregister()
	if h.IsOnline("alice") {
		t.Fatal("user still online after unregistering their only device")
	}
	if n := h.Route("alice", msg("hi")); n != 0 {
		t.Fatalf("routed to %d sinks after unregister", n)
	}
}

// The unregister closure is captured at register time, but a reconnect may have
// replaced the sink by the time it runs. Removing blindly would then evict the
// LIVE connection — the classic disconnect-after-reconnect race.
func TestUnregisterDoesNotEvictAReplacementConnection(t *testing.T) {
	h := NewHub()
	old := &fakeSink{device: "phone"}
	unregisterOld := h.Register("alice", old)

	fresh := &fakeSink{device: "phone"}
	h.Register("alice", fresh)

	unregisterOld() // the old connection's teardown finally runs

	if !h.IsOnline("alice") {
		t.Fatal("the reconnected device was evicted by the old connection's teardown")
	}
	if n := h.Route("alice", msg("hi")); n != 1 {
		t.Fatalf("routed to %d sinks, want the replacement to still be reachable", n)
	}
	if fresh.count() != 1 {
		t.Fatal("the live connection stopped receiving messages")
	}
}

func TestUnregisterIsIdempotent(t *testing.T) {
	h := NewHub()
	s := &fakeSink{device: "phone"}
	unregister := h.Register("alice", s)
	unregister()
	unregister() // a second teardown must not panic or corrupt the table
	if h.IsOnline("alice") {
		t.Fatal("user online after a double unregister")
	}
}

func TestRouteDeviceTargetsOneDevice(t *testing.T) {
	h := NewHub()
	phone := &fakeSink{device: "phone"}
	laptop := &fakeSink{device: "laptop"}
	h.Register("alice", phone)
	h.Register("alice", laptop)

	// E2E ciphertext is encrypted to ONE device's session, so delivering it to
	// the others would be traffic nobody can read.
	if !h.RouteDevice("alice", "laptop", msg("cipher")) {
		t.Fatal("RouteDevice reported the device unreachable")
	}
	if laptop.count() != 1 {
		t.Fatal("addressed device did not receive it")
	}
	if phone.count() != 0 {
		t.Fatal("device-addressed delivery leaked to another device")
	}
}

func TestRouteDeviceReportsUnreachable(t *testing.T) {
	h := NewHub()
	h.Register("alice", &fakeSink{device: "phone"})

	if h.RouteDevice("alice", "tablet", msg("x")) {
		t.Fatal("RouteDevice claimed success for a device that is not connected")
	}
	if h.RouteDevice("bob", "phone", msg("x")) {
		t.Fatal("RouteDevice claimed success for a user with no connections")
	}
}

func TestRouteDeviceRespectsBackpressure(t *testing.T) {
	h := NewHub()
	h.Register("alice", &fakeSink{device: "phone", full: true})
	if h.RouteDevice("alice", "phone", msg("x")) {
		t.Fatal("RouteDevice counted a send the connection refused")
	}
}

func TestIsOnlineTracksRemainingDevices(t *testing.T) {
	h := NewHub()
	unregisterPhone := h.Register("alice", &fakeSink{device: "phone"})
	h.Register("alice", &fakeSink{device: "laptop"})

	unregisterPhone()
	if !h.IsOnline("alice") {
		t.Fatal("user reported offline while another device is still connected")
	}
}

func TestHubIsSafeUnderConcurrency(t *testing.T) {
	h := NewHub()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := &fakeSink{device: "d"}
			unregister := h.Register("alice", s)
			h.Route("alice", msg("x"))
			h.RouteDevice("alice", "d", msg("x"))
			h.IsOnline("alice")
			unregister()
		}(i)
	}
	wg.Wait() // the assertion is the race detector staying quiet
}

// TestHubShardsSpreadUsers checks the sharding actually distributes rather than
// funnelling everyone into one bucket — a hash that returned a constant would
// pass every functional test above while reintroducing the single lock this
// change exists to remove.
func TestHubShardsSpreadUsers(t *testing.T) {
	h := NewHub()
	const users = 4096
	for i := 0; i < users; i++ {
		h.Register(fmt.Sprintf("user-%d", i), &fakeSink{device: "d1"})
	}

	occupied := 0
	worst := 0
	for i := range h.shards {
		n := len(h.shards[i].users)
		if n > 0 {
			occupied++
		}
		if n > worst {
			worst = n
		}
	}
	if occupied < hubShards/2 {
		t.Errorf("only %d of %d shards used; the hash is not distributing", occupied, hubShards)
	}
	// With 4096 users over 256 shards the mean is 16. A shard holding an order of
	// magnitude more than that means the distribution is not doing its job.
	if worst > 16*10 {
		t.Errorf("worst shard holds %d users against a mean of %d", worst, users/hubShards)
	}
}

// TestHubOperationsStayOnOneShardPerUser is the invariant that keeps every method
// atomic under a single lock: a user's devices must never be split across shards,
// or "route to all of this user's devices" would need several.
func TestHubOperationsStayOnOneShardPerUser(t *testing.T) {
	h := NewHub()
	const user = "u1"
	h.Register(user, &fakeSink{device: "phone"})
	h.Register(user, &fakeSink{device: "laptop"})

	found := 0
	for i := range h.shards {
		if devs, ok := h.shards[i].users[user]; ok {
			found++
			if len(devs) != 2 {
				t.Errorf("shard holds %d devices, want both", len(devs))
			}
		}
	}
	if found != 1 {
		t.Errorf("user appears in %d shards, want exactly 1", found)
	}
}
