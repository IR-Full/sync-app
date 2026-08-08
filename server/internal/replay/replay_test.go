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
