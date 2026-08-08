package keydir

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// TestRedisKeydirSharedAcrossNodes proves a prekey published via one directory
// instance ("node A") is visible via another ("node B") — the multi-node E2E
// requirement. Runs only when SYNAPSE_TEST_REDIS_ADDR is set.
func TestRedisKeydirSharedAcrossNodes(t *testing.T) {
	addr := os.Getenv("SYNAPSE_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set SYNAPSE_TEST_REDIS_ADDR to run the Redis keydir test")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx := context.Background()
	nodeA := NewRedis(rdb, log)
	nodeB := NewRedis(rdb, log) // a different gateway node, same Redis

	nodeA.Publish(ctx, "bob", "dev1", wire.KeyPublishBody{IdentityKey: "ik1", SigningKey: "sk1", SignedPreKey: "spk1", PreKeys: []string{"otp1"}})
	nodeA.Publish(ctx, "bob", "dev2", wire.KeyPublishBody{IdentityKey: "ik2", SigningKey: "sk2", SignedPreKey: "spk2"})

	// Node B can fetch a device published on node A, and consumes the one-time key.
	b, ok := nodeB.Fetch(ctx, "bob", "dev1")
	if !ok || b.IdentityKey != "ik1" || b.OneTimePreKey != "otp1" {
		t.Fatalf("cross-node fetch failed: ok=%v bundle=%+v", ok, b)
	}
	// One-time prekey was consumed; next fetch has none.
	b2, _ := nodeB.Fetch(ctx, "bob", "dev1")
	if b2.OneTimePreKey != "" {
		t.Fatalf("one-time prekey not consumed: %q", b2.OneTimePreKey)
	}

	// FetchAll from node B sees both devices.
	all := nodeB.FetchAll(ctx, "bob")
	if len(all) != 2 {
		t.Fatalf("want 2 devices, got %d", len(all))
	}
}
