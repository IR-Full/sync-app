package router

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestRedisRouterMultiNode proves the routing registry is shared: a user bound
// on two different nodes resolves to both. Runs only when SYNAPSE_TEST_REDIS_ADDR
// is set.
func TestRedisRouterMultiNode(t *testing.T) {
	addr := os.Getenv("SYNAPSE_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set SYNAPSE_TEST_REDIS_ADDR to run the Redis router test")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()
	ctx := context.Background()
	rdb.Del(ctx, routeKey("u1"))

	r := NewRedis(rdb, 30*time.Second)
	if err := r.Bind(ctx, "u1", "dA", "node1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Bind(ctx, "u1", "dB", "node2"); err != nil {
		t.Fatal(err)
	}
	nodes, _ := r.NodesFor(ctx, "u1")
	if len(nodes) != 2 {
		t.Fatalf("want 2 nodes, got %v", nodes)
	}

	// Unbinding one device on node1 (its only device there) drops node1.
	_ = r.Unbind(ctx, "u1", "dA", "node1")
	nodes, _ = r.NodesFor(ctx, "u1")
	if len(nodes) != 1 || nodes[0] != "node2" {
		t.Fatalf("want [node2], got %v", nodes)
	}
}
