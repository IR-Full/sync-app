package router

import (
	"time"

	"github.com/redis/go-redis/v9"
)

// redisRouter is the multi-node routing table. For each user it keeps a Redis
// hash route:<user> mapping nodeID → device refcount, with a TTL so a crashed
// node's bindings self-expire (refreshed by the gateway heartbeat). All ops are
// atomic Lua so concurrent bind/unbind across nodes stay consistent.
type redisRouter struct {
	rdb *redis.Client
	ttl time.Duration
}
