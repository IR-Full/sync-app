package ratelimit

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// The script keeps two fields per key: the token count and when it was last
// refilled. TTL is set from the time it takes to refill the bucket completely,
// so idle keys expire on their own and Redis never accumulates a key per user
// who once sent a message.
var bucketScript = redis.NewScript(`
local tokens_key = KEYS[1]
local rate  = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local now   = tonumber(ARGV[3])
local ttl   = tonumber(ARGV[4])

local data = redis.call('HMGET', tokens_key, 'tokens', 'ts')
local tokens = tonumber(data[1])
local ts = tonumber(data[2])
if tokens == nil then
  tokens = burst
  ts = now
end

local elapsed = math.max(0, now - ts)
tokens = math.min(burst, tokens + elapsed * rate)

local allowed = 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
end

redis.call('HSET', tokens_key, 'tokens', tokens, 'ts', now)
redis.call('PEXPIRE', tokens_key, ttl)
return allowed
`)

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
