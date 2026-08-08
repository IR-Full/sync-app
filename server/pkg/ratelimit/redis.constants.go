package ratelimit

import "github.com/redis/go-redis/v9"

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
