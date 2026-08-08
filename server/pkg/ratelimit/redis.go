package ratelimit

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// A per-connection bucket answers "is this SOCKET being abusive?", which is the
// wrong question for anything expensive. A client that opens ten connections
// gets ten budgets; the cost the limit is protecting — a media ticket, a search,
// a chat export — is paid by the server once per request regardless of which
// socket asked. Those limits belong to the USER, and on more than one node they
// have to live somewhere both nodes can see.

// NewRedisShared builds a limiter shared across nodes. prefix namespaces the
// keys (e.g. "media", "search") so one budget cannot be spent by another action.
func NewRedisShared(rdb *redis.Client, prefix string, ratePerSec, burst float64) Shared {
	return &redisLimiter{rdb: rdb, prefix: prefix, rate: ratePerSec, burst: burst, script: bucketScript}
}

func (r *redisLimiter) Allow(ctx context.Context, key string) bool {
	ttl := time.Duration(r.burst/r.rate*1000) * time.Millisecond * 2
	if ttl < time.Second {
		ttl = time.Second
	}
	res, err := r.script.Run(ctx, r.rdb,
		[]string{"rl:" + r.prefix + ":" + key},
		r.rate, r.burst, float64(time.Now().UnixMilli())/1000.0, ttl.Milliseconds(),
	).Int()
	if err != nil {
		// Fail open. A limiter is a guard rail, not a dependency: if Redis is down,
		// refusing every expensive request would turn a degraded cache into an
		// outage of search, media and export at once.
		return true
	}
	return res == 1
}

// NewLocalShared builds a node-local shared limiter.
func NewLocalShared(ratePerSec, burst float64) Shared {
	return &localShared{l: NewLimiter(ratePerSec, burst)}
}

func (s *localShared) Allow(_ context.Context, key string) bool { return s.l.Allow(key) }
