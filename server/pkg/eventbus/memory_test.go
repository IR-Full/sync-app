package eventbus

import (
	"context"
	"sync"
	"testing"
	"time"
)

// collector gathers handler invocations. The memory bus dispatches in
// goroutines, so every assertion waits on a signal rather than sleeping.
type collector struct {
	mu   sync.Mutex
	got  []Event
	done chan struct{}
	want int
}

func newCollector(want int) *collector {
	return &collector{done: make(chan struct{}), want: want}
}

func (c *collector) handle(_ context.Context, e Event) error {
	c.mu.Lock()
	c.got = append(c.got, e)
	reached := len(c.got) == c.want
	c.mu.Unlock()
	if reached {
		close(c.done)
	}
	return nil
}

func (c *collector) wait(t *testing.T) []Event {
	t.Helper()
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
		c.mu.Lock()
		defer c.mu.Unlock()
		t.Fatalf("timed out with %d of %d events", len(c.got), c.want)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Event(nil), c.got...)
}

func (c *collector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.got)
}

func ctx() context.Context { return context.Background() }

func TestPublishReachesSubscriber(t *testing.T) {
	b := NewMemory()
	c := newCollector(1)
	if err := b.Subscribe("message.created", "", c.handle); err != nil {
		t.Fatal(err)
	}

	if err := b.Publish(ctx(), Event{Subject: "message.created", Key: "chat1", Data: []byte("hi")}); err != nil {
		t.Fatal(err)
	}
	got := c.wait(t)
	if string(got[0].Data) != "hi" || got[0].Key != "chat1" {
		t.Fatalf("event altered in transit: %+v", got[0])
	}
}

// Every plain subscriber gets its own copy — that is what lets search, fanout
// and moderation all consume the same message event independently.
func TestEveryPlainSubscriberGetsACopy(t *testing.T) {
	b := NewMemory()
	a, second := newCollector(1), newCollector(1)
	_ = b.Subscribe("message.created", "", a.handle)
	_ = b.Subscribe("message.created", "", second.handle)

	_ = b.Publish(ctx(), Event{Subject: "message.created", Key: "k"})
	a.wait(t)
	second.wait(t)
}

// A queue group is competing consumers: exactly one member handles each event,
// which is how fanout scales horizontally without delivering twice.
func TestQueueGroupDeliversToExactlyOneMember(t *testing.T) {
	b := NewMemory()
	var mu sync.Mutex
	total := 0
	done := make(chan struct{})

	handler := func(_ context.Context, _ Event) error {
		mu.Lock()
		total++
		if total == 1 {
			close(done)
		}
		mu.Unlock()
		return nil
	}
	for i := 0; i < 4; i++ {
		_ = b.Subscribe("message.created", "workers", handler)
	}

	_ = b.Publish(ctx(), Event{Subject: "message.created", Key: "chat1"})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("no queue member handled the event")
	}
	time.Sleep(50 * time.Millisecond) // give any extra deliveries a chance to land

	mu.Lock()
	defer mu.Unlock()
	if total != 1 {
		t.Fatalf("%d queue members handled one event, want exactly 1", total)
	}
}

// Events for one key must land on the same member, or a chat's events can be
// processed out of order by different workers.
func TestQueueGroupPinsAKeyToOneMember(t *testing.T) {
	b := NewMemory()
	const members = 4
	hits := make([]int, members)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < members; i++ {
		i := i
		_ = b.Subscribe("message.created", "workers", func(_ context.Context, _ Event) error {
			mu.Lock()
			hits[i]++
			mu.Unlock()
			wg.Done()
			return nil
		})
	}

	const sends = 20
	wg.Add(sends)
	for i := 0; i < sends; i++ {
		_ = b.Publish(ctx(), Event{Subject: "message.created", Key: "same-chat"})
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	active := 0
	for _, n := range hits {
		if n > 0 {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("one key was spread across %d members: %v", active, hits)
	}
}

func TestSubjectsAreIsolated(t *testing.T) {
	b := NewMemory()
	wanted := newCollector(1)
	other := newCollector(1)
	_ = b.Subscribe("message.created", "", wanted.handle)
	_ = b.Subscribe("message.deleted", "", other.handle)

	_ = b.Publish(ctx(), Event{Subject: "message.created", Key: "k"})
	wanted.wait(t)

	time.Sleep(50 * time.Millisecond)
	if other.count() != 0 {
		t.Fatal("an event leaked to a different subject's subscriber")
	}
}

func TestTrailingWildcardMatches(t *testing.T) {
	b := NewMemory()
	c := newCollector(2)
	// "message.*" is how the search indexer follows created AND edited without
	// subscribing to each by name.
	_ = b.Subscribe("message.*", "", c.handle)

	_ = b.Publish(ctx(), Event{Subject: "message.created", Key: "k"})
	_ = b.Publish(ctx(), Event{Subject: "message.edited", Key: "k"})
	c.wait(t)
}

func TestPublishWithNoSubscribersIsNotAnError(t *testing.T) {
	b := NewMemory()
	// A worker that is not deployed must not fail the write path that publishes.
	if err := b.Publish(ctx(), Event{Subject: "nobody.listening", Key: "k"}); err != nil {
		t.Fatalf("publish to an empty subject: %v", err)
	}
}

func TestMatchRules(t *testing.T) {
	for _, tc := range []struct {
		pattern, subject string
		want             bool
	}{
		{"message.created", "message.created", true},
		{"message.created", "message.edited", false},
		{"message.*", "message.created", true},
		{"message.*", "message.deleted", true},
		{"message.*", "notify.push", false},
		{"*", "anything.at.all", true},
		{"message", "message.created", false},
	} {
		if got := match(tc.pattern, tc.subject); got != tc.want {
			t.Errorf("match(%q, %q) = %v, want %v", tc.pattern, tc.subject, got, tc.want)
		}
	}
}

func TestHashKeyIsStableAndNeverZero(t *testing.T) {
	// Golden values, not self-comparison.
	//
	// This used to assert `hashKey("chat1") != hashKey("chat1")`, which a pure
	// function cannot fail — the compiler can fold it away. What the test means
	// to guard is that the hash is stable ACROSS BUILDS, because it decides which
	// queue member owns a key: if it changed, a rolling deploy would have two
	// versions disagreeing about ownership and delivering the same key twice.
	// Only a fixed expected value can catch that.
	for key, want := range map[string]uint32{
		"chat1": 395963758,
		"chat2": 379186139,
		"":      2166136261,
	} {
		if got := hashKey(key); got != want {
			t.Errorf("hashKey(%q) = %d, want %d — changing this splits ownership mid-deploy", key, got, want)
		}
	}
	// Non-zero because zero would collapse onto member 0 for every key that
	// happened to hash there.
	if hashKey("") == 0 {
		t.Fatal("hashKey returned 0 for the empty key")
	}
}

func TestCloseIsSafe(t *testing.T) {
	b := NewMemory()
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestBusIsSafeUnderConcurrency(t *testing.T) {
	b := NewMemory()
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = b.Subscribe("concurrent.subject", "", func(context.Context, Event) error { return nil })
			_ = b.Publish(ctx(), Event{Subject: "concurrent.subject", Key: "k"})
		}()
	}
	wg.Wait() // the assertion is the race detector staying quiet
}
