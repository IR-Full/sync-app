package ratelimit

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// Shared is a rate limiter whose state is visible to every gateway node.
type Shared interface {
	// Allow reports whether one unit of work is permitted for key right now.
	// Implementations must fail OPEN: a limiter that cannot reach its backend
	// must not become an outage of the feature it protects.
	Allow(ctx context.Context, key string) bool
}

// redisLimiter is a token bucket held in Redis, refilled lazily. The whole
// operation is one Lua script so the read-modify-write cannot interleave with
// another node's — a bucket that can be read twice before either write lands is
// not a limit, it is a suggestion.
type redisLimiter struct {
	rdb    *redis.Client
	prefix string
	rate   float64 // tokens per second
	burst  float64
	script *redis.Script
}

// localShared adapts the in-process Limiter to the Shared interface. On a single
// node it is exactly right; across nodes each node enforces its own share, which
// is still strictly better than a budget per connection.
type localShared struct{ l *Limiter }
