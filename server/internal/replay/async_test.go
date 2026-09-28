package replay

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// slowBuffer blocks every Append until released, standing in for a hung Redis.
type slowBuffer struct {
	Buffer
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (s *slowBuffer) Append(ctx context.Context, id string, seq uint64, p []byte) error {
	<-s.release
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return s.Buffer.Append(ctx, id, seq, p)
}

// The point of Async: the connection's writer must not wait on the store. With
// the store completely stuck, a burst of appends still returns at once.
func TestAsyncAppendNeverBlocks(t *testing.T) {
	slow := &slowBuffer{Buffer: NewMemory(), release: make(chan struct{})}
	a := NewAsync(slow, 1024, nil)
	defer func() { close(slow.release); a.Close() }()

	start := time.Now()
	for i := 1; i <= 500; i++ {
		_ = a.Append(context.Background(), "s", uint64(i), []byte("frame"))
	}
	if took := time.Since(start); took > 100*time.Millisecond {
		t.Fatalf("500 appends against a stuck store took %v", took)
	}
}

// A resume on the same node must see every frame sent before it, even ones
// still queued when it asks.
func TestAsyncSinceSeesQueuedFrames(t *testing.T) {
	a := NewAsync(NewMemory(), 1024, nil)
	defer a.Close()
	ctx := context.Background()
	for i := 1; i <= 50; i++ {
		_ = a.Append(ctx, "s", uint64(i), []byte{byte(i)})
	}
	frames, err := a.Since(ctx, "s", 40)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 10 || frames[0].Seq != 41 || frames[9].Seq != 50 {
		t.Fatalf("got %d frames (%v…), want seqs 41..50", len(frames), frames)
	}
	if hw, _ := a.HighWater(ctx, "s"); hw != 50 {
		t.Fatalf("high water = %d, want 50", hw)
	}
}

// A full queue drops rather than blocks; nothing enqueued is lost on Close.
func TestAsyncDropsWhenFullAndDrainsOnClose(t *testing.T) {
	slow := &slowBuffer{Buffer: NewMemory(), release: make(chan struct{})}
	a := NewAsync(slow, 4, nil)
	ctx := context.Background()
	for i := 1; i <= 100; i++ {
		_ = a.Append(ctx, "s", uint64(i), []byte("x"))
	}
	close(slow.release)
	a.Close()

	slow.mu.Lock()
	defer slow.mu.Unlock()
	// At most the queue plus the one batch the worker had already taken.
	if slow.calls == 0 || slow.calls >= 100 {
		t.Fatalf("store saw %d appends; want some but not all of 100", slow.calls)
	}
}

func TestRedisAppendBatch(t *testing.T) {
	addr := os.Getenv("SYNCAPP_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set SYNCAPP_TEST_REDIS_ADDR to run the Redis replay test")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()
	b := NewRedis(rdb, time.Minute)
	ctx := context.Background()
	sid := "batch-" + time.Now().Format("150405.000000000")
	defer func() { _ = b.Drop(ctx, sid) }()

	entries := make([]Entry, 0, 20)
	for i := 1; i <= 20; i++ {
		entries = append(entries, Entry{SessionID: sid, Seq: uint64(i), Payload: []byte{byte(i)}})
	}
	if err := b.(BatchAppender).AppendBatch(ctx, entries); err != nil {
		t.Fatal(err)
	}
	frames, err := b.Since(ctx, sid, 15)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 5 || frames[0].Seq != 16 || frames[0].Payload[0] != 16 {
		t.Fatalf("got %v, want seqs 16..20", frames)
	}
	if ttl := rdb.TTL(ctx, streamKey(sid)).Val(); ttl <= 0 {
		t.Fatalf("batch left the stream without a TTL (%v)", ttl)
	}
}
