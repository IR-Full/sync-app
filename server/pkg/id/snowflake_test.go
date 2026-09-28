package id

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestNewGeneratorRejectsNodeOutOfRange(t *testing.T) {
	// The node id is 10 bits of every id this process mints. A value outside the
	// range would silently overlap another node's id space, so it is refused at
	// construction rather than truncated.
	for _, node := range []int64{-1, 1024, 99999} {
		if _, err := NewGenerator(node); err == nil {
			t.Fatalf("node %d accepted, want an error", node)
		}
	}
	for _, node := range []int64{0, 1, 512, 1023} {
		if _, err := NewGenerator(node); err != nil {
			t.Fatalf("node %d refused: %v", node, err)
		}
	}
}

func TestIDsAreUniqueAndIncreasing(t *testing.T) {
	g, err := NewGenerator(7)
	if err != nil {
		t.Fatal(err)
	}
	const n = 10_000
	seen := make(map[int64]bool, n)
	var prev int64
	for i := 0; i < n; i++ {
		id := g.Next()
		if seen[id] {
			t.Fatalf("duplicate id %d at iteration %d", id, i)
		}
		seen[id] = true
		// Monotonic per node is what makes ids usable as a tie-break and keeps
		// index inserts append-only.
		if id <= prev {
			t.Fatalf("id went backwards: %d after %d", id, prev)
		}
		prev = id
	}
}

// 4096 ids per millisecond per node is the per-ms sequence space; past it the
// generator must advance the clock rather than repeat or wrap.
func TestSequenceExhaustionAdvancesTheClock(t *testing.T) {
	g, _ := NewGenerator(1)
	const n = 9000 // comfortably more than two milliseconds' worth
	seen := make(map[int64]bool, n)
	for i := 0; i < n; i++ {
		id := g.Next()
		if seen[id] {
			t.Fatalf("id %d repeated after %d ids — the sequence wrapped", id, i)
		}
		seen[id] = true
	}
}

func TestIDsArePositive(t *testing.T) {
	// The sign bit is deliberately left clear so ids survive a trip through
	// languages and databases with no unsigned 64-bit type.
	g, _ := NewGenerator(1023)
	for i := 0; i < 100; i++ {
		if id := g.Next(); id < 0 {
			t.Fatalf("negative id: %d", id)
		}
	}
}

func TestNodeIsEncodedInTheID(t *testing.T) {
	// Encoding the origin node is the reason for choosing Snowflake over a random
	// id: an id tells you which writer produced it.
	for _, node := range []int64{0, 1, 42, 1023} {
		g, _ := NewGenerator(node)
		id := g.Next()
		got := (id >> nodeShift) & maxNode
		if got != node {
			t.Fatalf("id encodes node %d, want %d", got, node)
		}
	}
}

func TestDifferentNodesDoNotCollide(t *testing.T) {
	// Two nodes minting at the same instant must not produce the same id — that
	// is the whole coordination-free promise.
	a, _ := NewGenerator(1)
	b, _ := NewGenerator(2)
	seen := map[int64]bool{}
	for i := 0; i < 5000; i++ {
		for _, id := range []int64{a.Next(), b.Next()} {
			if seen[id] {
				t.Fatalf("collision across nodes at %d", id)
			}
			seen[id] = true
		}
	}
}

func TestTimeOfRoundTrips(t *testing.T) {
	g, _ := NewGenerator(3)
	before := time.Now().UnixMilli()
	id := g.Next()
	after := time.Now().UnixMilli()

	got := TimeOf(id).UnixMilli()
	if got < before || got > after {
		t.Fatalf("TimeOf = %d, outside the [%d, %d] window the id was minted in", got, before, after)
	}
}

func TestIDsSortByTime(t *testing.T) {
	g, _ := NewGenerator(5)
	first := g.Next()
	time.Sleep(2 * time.Millisecond)
	second := g.Next()

	if !(first < second) {
		t.Fatalf("ids do not sort by time: %d then %d", first, second)
	}
	if !TimeOf(first).Before(TimeOf(second)) {
		t.Fatal("decoded times do not sort with the ids")
	}
}

func TestNextStringIsTheDecimalForm(t *testing.T) {
	// Ids travel as decimal strings so a client with no 64-bit integer type (a
	// browser) cannot round them off.
	g, _ := NewGenerator(9)
	s := g.NextString()
	parsed, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatalf("NextString produced %q, which does not parse: %v", s, err)
	}
	if (parsed>>nodeShift)&maxNode != 9 {
		t.Fatalf("string id %q lost its node", s)
	}
}

func TestGeneratorIsSafeUnderConcurrency(t *testing.T) {
	g, _ := NewGenerator(11)
	const workers, each = 16, 500

	var wg sync.WaitGroup
	out := make([][]int64, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			ids := make([]int64, each)
			for i := range ids {
				ids[i] = g.Next()
			}
			out[w] = ids
		}(w)
	}
	wg.Wait()

	seen := make(map[int64]bool, workers*each)
	for _, ids := range out {
		for _, id := range ids {
			if seen[id] {
				t.Fatalf("concurrent generation produced duplicate id %d", id)
			}
			seen[id] = true
		}
	}
	if len(seen) != workers*each {
		t.Fatalf("got %d unique ids, want %d", len(seen), workers*each)
	}
}

// A backwards clock step (NTP correction) must never produce an id smaller than
// one already handed out, because ids are used as an ordering.
func TestClockGoingBackwardsDoesNotLowerIDs(t *testing.T) {
	g, _ := NewGenerator(4)
	first := g.Next()

	g.mu.Lock()
	g.lastMs += 5000 // pretend we already minted ids 5s into the future
	g.mu.Unlock()

	second := g.Next()
	if second <= first {
		t.Fatalf("id went backwards across a clock step: %d then %d", first, second)
	}
}
