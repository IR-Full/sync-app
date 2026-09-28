package replay

import (
	"context"
	"testing"
	"time"
)

// TestMemoryBufferExpiresIdleSessions pins the buffer's bound. Nothing calls
// Drop on a normal disconnect — surviving one is the whole point — so without
// expiry the process keeps up to maxFrames per session for every session it has
// ever served. A dropped buffer costs a returning client only a history
// backfill, which is exactly what it would get with no buffer at all.
func TestMemoryBufferExpiresIdleSessions(t *testing.T) {
	ctx := context.Background()
	b := NewMemory()
	m := b.(*memoryBuffer)

	if err := b.Append(ctx, "idle", 1, []byte("frame")); err != nil {
		t.Fatal(err)
	}
	if err := b.Append(ctx, "active", 1, []byte("frame")); err != nil {
		t.Fatal(err)
	}

	// Age both sessions past the TTL, then keep one alive the way a resume would.
	m.mu.Lock()
	old := time.Now().Add(-2 * sessionTTL)
	m.sessions["idle"].lastSeen = old
	m.sessions["active"].lastSeen = old
	m.lastSweep = time.Now().Add(-2 * sweepEvery)
	m.mu.Unlock()

	if _, err := b.Since(ctx, "active", 0); err != nil {
		t.Fatal(err)
	}
	if err := b.Append(ctx, "active", 2, []byte("frame")); err != nil { // triggers the sweep
		t.Fatal(err)
	}

	m.mu.Lock()
	_, idleKept := m.sessions["idle"]
	_, activeKept := m.sessions["active"]
	m.mu.Unlock()

	if idleKept {
		t.Fatal("an idle session survived the sweep; the buffer grows without bound")
	}
	if !activeKept {
		t.Fatal("a session that just resumed was evicted")
	}

	frames, err := b.Since(ctx, "gone", 0)
	if err != nil || len(frames) != 0 {
		t.Fatalf("unknown session should replay nothing, got %d frames (%v)", len(frames), err)
	}
}

// TestHighWaterSurvivesAFullyAcknowledgedSession is the regression test for a
// sequence that ran backwards.
//
// A client that has acknowledged every frame gets nothing from Since — correctly,
// there is nothing it missed. The resume path read that empty result as "no
// evidence any seq was ever sent" and reset the connection's outbound counter to
// zero, so the next frame went out numbered 1 to a client that had already
// acknowledged 100. HighWater is the distinction Since cannot express: "nothing
// to replay" is not "no such session".
func TestHighWaterSurvivesAFullyAcknowledgedSession(t *testing.T) {
	b := NewMemory()
	ctx := context.Background()
	for seq := uint64(1); seq <= 100; seq++ {
		if err := b.Append(ctx, "s1", seq, []byte("f")); err != nil {
			t.Fatal(err)
		}
	}

	// The client acknowledged everything: no frames to replay.
	frames, err := b.Since(ctx, "s1", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 0 {
		t.Fatalf("want nothing to replay, got %d frames", len(frames))
	}

	// But the server still knows how far it got.
	hw, err := b.HighWater(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if hw != 100 {
		t.Fatalf("high water = %d, want 100 — a resume here would restart numbering", hw)
	}
}

func TestHighWaterIsZeroForAnUnknownSession(t *testing.T) {
	// Zero has to mean "nothing recorded", because that is what the resume path
	// falls back on. An error would make an ordinary first connection look broken.
	hw, err := NewMemory().HighWater(context.Background(), "never-seen")
	if err != nil {
		t.Fatal(err)
	}
	if hw != 0 {
		t.Fatalf("high water = %d for an unknown session, want 0", hw)
	}
}

// TestHighWaterTracksTheTrimmedRing checks the interaction with the ring bound:
// once frames are evicted, the high-water mark must follow the newest frame, not
// the oldest surviving one.
func TestHighWaterTracksTheTrimmedRing(t *testing.T) {
	b := NewMemory()
	ctx := context.Background()
	total := uint64(maxFrames + 50)
	for seq := uint64(1); seq <= total; seq++ {
		if err := b.Append(ctx, "s1", seq, []byte("f")); err != nil {
			t.Fatal(err)
		}
	}
	hw, err := b.HighWater(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if hw != total {
		t.Fatalf("high water = %d after trimming, want %d", hw, total)
	}
}
