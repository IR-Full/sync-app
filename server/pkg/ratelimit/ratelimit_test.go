package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestBucketStartsFullAndDrains(t *testing.T) {
	b := NewBucket(1, 5) // 1/s, burst 5

	// Starting full is what lets a legitimate burst through on a fresh connection
	// instead of throttling the first five actions of every session.
	for i := 0; i < 5; i++ {
		if !b.Allow() {
			t.Fatalf("burst token %d refused on a full bucket", i+1)
		}
	}
	if b.Allow() {
		t.Fatal("bucket admitted a sixth action past its burst")
	}
}

func TestBucketRefillsOverTime(t *testing.T) {
	// 100/s so one token is back in ~10ms — fast enough to test without a sleep
	// long enough to make the suite drag.
	b := NewBucket(100, 1)
	if !b.Allow() {
		t.Fatal("first action refused")
	}
	if b.Allow() {
		t.Fatal("bucket had two tokens when it was built with a burst of one")
	}
	time.Sleep(30 * time.Millisecond)
	if !b.Allow() {
		t.Fatal("bucket did not refill")
	}
}

func TestBucketRefillIsCappedAtBurst(t *testing.T) {
	b := NewBucket(1000, 2)
	// Idle long enough to have earned far more than the capacity.
	time.Sleep(20 * time.Millisecond)
	// Two separate statements rather than `!b.Allow() || !b.Allow()`: Allow has
	// a side effect, so the short-circuit made the second call conditional on the
	// first — and the two identical-looking calls read as a copy-paste slip.
	if !b.Allow() {
		t.Fatal("first token not available after idling")
	}
	if !b.Allow() {
		t.Fatal("second token not available after idling")
	}
	// Without the cap an idle connection would bank an unbounded burst, which is
	// exactly the flood the limiter exists to stop.
	if b.Allow() {
		t.Fatal("idle bucket accumulated more than its burst")
	}
}

func TestAllowNCostsMultipleTokens(t *testing.T) {
	b := NewBucket(1, 10)
	if !b.AllowN(7) {
		t.Fatal("AllowN(7) refused on a bucket of 10")
	}
	if b.AllowN(4) {
		t.Fatal("AllowN(4) admitted with only 3 tokens left")
	}
	if !b.AllowN(3) {
		t.Fatal("AllowN(3) refused with exactly 3 tokens left")
	}
}

func TestAllowNZeroIsFree(t *testing.T) {
	b := NewBucket(1, 1)
	if !b.AllowN(0) {
		t.Fatal("a zero-cost action was refused")
	}
	if !b.Allow() {
		t.Fatal("a zero-cost action consumed a token")
	}
}

func TestBucketIsSafeUnderConcurrency(t *testing.T) {
	// Exactly `burst` admissions must get through, no more: the bucket fronts the
	// gateway's per-connection loop, where several goroutines can race it.
	const burst = 50
	b := NewBucket(0, burst) // no refill, so the count is deterministic

	var wg sync.WaitGroup
	var mu sync.Mutex
	admitted := 0
	for i := 0; i < burst*4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if b.Allow() {
				mu.Lock()
				admitted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if admitted != burst {
		t.Fatalf("admitted %d of a %d-token bucket", admitted, burst)
	}
}

func TestLimiterKeepsKeysIndependent(t *testing.T) {
	l := NewLimiter(0, 2) // no refill, burst 2 per key

	for i := 0; i < 2; i++ {
		if !l.Allow("alice") {
			t.Fatalf("alice refused at action %d", i+1)
		}
	}
	if l.Allow("alice") {
		t.Fatal("alice exceeded her burst")
	}
	// Bob has his own budget — a keyed limiter that leaked between keys would let
	// one abusive account throttle everyone else.
	if !l.Allow("bob") {
		t.Fatal("bob was throttled by alice's usage")
	}
}

func TestLimiterCollectsIdleBuckets(t *testing.T) {
	l := NewLimiter(1, 1)
	// Reach in to shorten the TTL: the real one is 10 minutes, and the behaviour
	// worth pinning is that idle keys are dropped at all — otherwise the map grows
	// with every key the node has ever seen.
	l.mu.Lock()
	l.ttl = time.Millisecond
	l.mu.Unlock()

	l.Allow("transient")
	l.mu.Lock()
	n := len(l.buckets)
	l.mu.Unlock()
	if n != 1 {
		t.Fatalf("bucket not created: %d entries", n)
	}

	time.Sleep(5 * time.Millisecond)
	l.Allow("other") // any call triggers the lazy sweep

	l.mu.Lock()
	_, stillThere := l.buckets["transient"]
	l.mu.Unlock()
	if stillThere {
		t.Fatal("idle bucket survived the sweep — the map would grow without bound")
	}
}

func TestLimiterIsSafeUnderConcurrency(t *testing.T) {
	l := NewLimiter(100, 10)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l.Allow("key")
			l.Allow("other")
		}(i)
	}
	wg.Wait() // the assertion is the race detector staying quiet
}
