package router

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
)

/*
 * The resilient wrapper turns a Redis outage into partial degradation instead of
 * a total delivery outage: the node keeps its own view, so same-node recipients
 * keep receiving, and cross-node delivery falls back to history-sync on
 * reconnect.
 *
 * The property that matters is that a failing shared store never surfaces as an
 * error to the caller — a Bind that returned one would close the connection that
 * had just been established, turning a Redis blip into a disconnect storm.
 */

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// countingRouter records calls and can be switched between healthy and failing
// mid-test, which is how the breaker's recovery path is reached.
type countingRouter struct {
	mu      sync.Mutex
	failing bool
	binds   atomic.Int64
	unbinds atomic.Int64
	refresh atomic.Int64
	lookups atomic.Int64
	nodes   []string
}

func (c *countingRouter) fail(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failing = v
}

func (c *countingRouter) broken() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failing
}

func (c *countingRouter) Bind(context.Context, string, string, string) error {
	c.binds.Add(1)
	if c.broken() {
		return errors.New("redis is down")
	}
	return nil
}

func (c *countingRouter) Unbind(context.Context, string, string, string) error {
	c.unbinds.Add(1)
	if c.broken() {
		return errors.New("redis is down")
	}
	return nil
}

func (c *countingRouter) Refresh(context.Context, string, string) error {
	c.refresh.Add(1)
	if c.broken() {
		return errors.New("redis is down")
	}
	return nil
}

func (c *countingRouter) NodesFor(context.Context, string) ([]string, error) {
	c.lookups.Add(1)
	if c.broken() {
		return nil, errors.New("redis is down")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.nodes...), nil
}

func (c *countingRouter) NodesForDevice(ctx context.Context, userID, _ string) ([]string, error) {
	return c.NodesFor(ctx, userID)
}

func (c *countingRouter) NodesForMany(ctx context.Context, userIDs []string) (map[string][]string, error) {
	out := make(map[string][]string, len(userIDs))
	for _, uid := range userIDs {
		nodes, err := c.NodesFor(ctx, uid)
		if err != nil {
			return nil, err
		}
		if len(nodes) > 0 {
			out[uid] = nodes
		}
	}
	return out, nil
}

// ------------------------------------------------------------- healthy path

func TestSharedLookupIsPreferredWhenHealthy(t *testing.T) {
	// The whole point of a shared registry is cross-node reach; silently using
	// the local view would quietly cap delivery at one node again.
	shared := &countingRouter{nodes: []string{"node-1", "node-2"}}
	r := NewResilient(shared, discard())

	nodes, err := r.NodesFor(bg(), "u1")
	if err != nil {
		t.Fatal(err)
	}

	if len(nodes) != 2 {
		t.Errorf("nodes = %v, want both from the shared router", nodes)
	}
	if shared.lookups.Load() == 0 {
		t.Error("the shared router was never consulted")
	}
}

func TestWritesReachTheSharedRouterWhenHealthy(t *testing.T) {
	shared := &countingRouter{}
	r := NewResilient(shared, discard())

	if err := r.Bind(bg(), "u1", "d1", "node-1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Refresh(bg(), "u1", "node-1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Unbind(bg(), "u1", "d1", "node-1"); err != nil {
		t.Fatal(err)
	}

	if shared.binds.Load() == 0 || shared.refresh.Load() == 0 || shared.unbinds.Load() == 0 {
		t.Errorf("shared writes: bind=%d refresh=%d unbind=%d, want all non-zero",
			shared.binds.Load(), shared.refresh.Load(), shared.unbinds.Load())
	}
}

// ----------------------------------------------------------- degraded path

/*
 * Every write is best-effort. A Bind that propagated the shared store's failure
 * would make the gateway tear down a connection that is perfectly usable for
 * same-node delivery — converting a dependency blip into a user-visible
 * disconnect for everyone at once.
 */
func TestWritesNeverSurfaceASharedFailure(t *testing.T) {
	r := NewResilient(&countingRouter{failing: true}, discard())

	if err := r.Bind(bg(), "u1", "d1", "node-1"); err != nil {
		t.Errorf("bind surfaced a shared failure: %v", err)
	}
	if err := r.Unbind(bg(), "u1", "d1", "node-1"); err != nil {
		t.Errorf("unbind surfaced a shared failure: %v", err)
	}
	if err := r.Refresh(bg(), "u1", "node-1"); err != nil {
		t.Errorf("refresh surfaced a shared failure: %v", err)
	}
}

func TestLocalViewIsWrittenEvenWhenTheSharedStoreIsDown(t *testing.T) {
	// This is what keeps same-node delivery working through the outage.
	r := NewResilient(&countingRouter{failing: true}, discard())

	if err := r.Bind(bg(), "u1", "d1", "node-7"); err != nil {
		t.Fatal(err)
	}

	if got := nodesFor(t, r, "u1"); len(got) != 1 || got[0] != "node-7" {
		t.Errorf("nodes = %v, want the local binding to have survived", got)
	}
}

/*
 * Unbind has to reach the local view too. If it only ever wrote to the shared
 * store, a node whose Redis was down would accumulate bindings for connections
 * that closed long ago — and then deliver to itself for users who are no longer
 * there, while the push path believes them online.
 */
func TestUnbindClearsTheLocalViewDuringAnOutage(t *testing.T) {
	r := NewResilient(&countingRouter{failing: true}, discard())
	if err := r.Bind(bg(), "u1", "d1", "node-7"); err != nil {
		t.Fatal(err)
	}

	if err := r.Unbind(bg(), "u1", "d1", "node-7"); err != nil {
		t.Fatal(err)
	}

	if got := nodesFor(t, r, "u1"); len(got) != 0 {
		t.Errorf("nodes = %v after unbinding; the local view leaked a closed connection", got)
	}
}

func TestLookupFallsBackToTheLocalViewOnFailure(t *testing.T) {
	shared := &countingRouter{failing: true}
	r := NewResilient(shared, discard())
	if err := r.Bind(bg(), "u1", "d1", "node-7"); err != nil {
		t.Fatal(err)
	}

	nodes, err := r.NodesFor(bg(), "u1")
	if err != nil {
		t.Fatalf("a shared failure surfaced as a lookup error: %v", err)
	}

	if len(nodes) != 1 || nodes[0] != "node-7" {
		t.Errorf("nodes = %v, want the local fallback", nodes)
	}
}

func TestLookupReportsNoNodesForAUserThisNodeDoesNotHold(t *testing.T) {
	// Degraded means "only what I can see", not "guess". Fanout reads an empty
	// list as offline and queues a push, which is the right outcome — the user
	// gets the message on another device rather than not at all.
	r := NewResilient(&countingRouter{failing: true}, discard())

	nodes, err := r.NodesFor(bg(), "elsewhere")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Errorf("nodes = %v, want none", nodes)
	}
}

// ----------------------------------------------------------------- breaker

/*
 * The breaker is what stops a dead Redis from costing every lookup its full
 * timeout. Once it opens, the shared store is not consulted at all — the node
 * answers from its local view immediately.
 */
func TestTheBreakerStopsConsultingADeadSharedStore(t *testing.T) {
	shared := &countingRouter{failing: true}
	r := NewResilient(shared, discard())

	// Trip it: the breaker opens after its failure threshold.
	for range 10 {
		if _, err := r.NodesFor(bg(), "u1"); err != nil {
			t.Fatal(err)
		}
	}
	tripped := shared.lookups.Load()

	for range 10 {
		if _, err := r.NodesFor(bg(), "u1"); err != nil {
			t.Fatal(err)
		}
	}

	if shared.lookups.Load() != tripped {
		t.Errorf("the shared store was consulted %d more times after the breaker opened",
			shared.lookups.Load()-tripped)
	}
}

func TestLookupsKeepWorkingWhileTheBreakerIsOpen(t *testing.T) {
	// Degradation must not become an outage of its own.
	shared := &countingRouter{failing: true}
	r := NewResilient(shared, discard())
	if err := r.Bind(bg(), "u1", "d1", "node-7"); err != nil {
		t.Fatal(err)
	}

	for range 20 {
		nodes, err := r.NodesFor(bg(), "u1")
		if err != nil {
			t.Fatalf("a lookup failed while degraded: %v", err)
		}
		if len(nodes) != 1 {
			t.Fatalf("nodes = %v, want the local view throughout", nodes)
		}
	}
}

func TestWritesKeepBeingAcceptedWhileTheBreakerIsOpen(t *testing.T) {
	// Connections continue to open and close during the outage; each one binds.
	shared := &countingRouter{failing: true}
	r := NewResilient(shared, discard())

	for i := range 20 {
		device := string(rune('a' + i%26))
		if err := r.Bind(bg(), "u1", device, "node-7"); err != nil {
			t.Fatalf("bind %d failed while degraded: %v", i, err)
		}
	}

	if got := nodesFor(t, r, "u1"); len(got) != 1 {
		t.Errorf("nodes = %v, want the node still locally reachable", got)
	}
}

// ------------------------------------------------------------- concurrency

func TestTheResilientRouterIsSafeForConcurrentUse(t *testing.T) {
	// It sits on the connection open/close path, so it is called from one
	// goroutine per connection — and the breaker is shared mutable state.
	shared := &countingRouter{nodes: []string{"node-1"}}
	r := NewResilient(shared, discard())
	var wg sync.WaitGroup

	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			device := string(rune('a' + i%26))
			_ = r.Bind(bg(), "u1", device, "node-1")
			_, _ = r.NodesFor(bg(), "u1")
			_ = r.Refresh(bg(), "u1", "node-1")
			_ = r.Unbind(bg(), "u1", device, "node-1")
		}(i)
	}
	wg.Wait()
}

func TestTheResilientRouterSurvivesTheSharedStoreFailingMidFlight(t *testing.T) {
	// A Redis that fails partway through is the realistic outage, not one that
	// was down from the start.
	shared := &countingRouter{nodes: []string{"node-1"}}
	r := NewResilient(shared, discard())

	if err := r.Bind(bg(), "u1", "d1", "node-1"); err != nil {
		t.Fatal(err)
	}
	if nodes, _ := r.NodesFor(bg(), "u1"); len(nodes) != 1 {
		t.Fatalf("nodes = %v before the outage", nodes)
	}

	shared.fail(true)

	nodes, err := r.NodesFor(bg(), "u1")
	if err != nil {
		t.Fatalf("the lookup failed after the shared store went down: %v", err)
	}
	if len(nodes) != 1 || nodes[0] != "node-1" {
		t.Errorf("nodes = %v, want the local view to cover the outage", nodes)
	}
}
