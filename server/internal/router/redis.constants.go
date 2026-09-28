package router

import "github.com/redis/go-redis/v9"

// bindLua increments a node's refcount and (re)sets the TTL on every key it is
// given — the user-level index, and the per-device one when a device was named.
// Both in ONE script so a connection can never be registered in one index and
// missing from the other.
var bindLua = redis.NewScript(`
for i = 1, #KEYS do
  redis.call("HINCRBY", KEYS[i], ARGV[1], 1)
  redis.call("PEXPIRE", KEYS[i], ARGV[2])
end
return 1`)

// unbindLua decrements; removes the field at zero and the key when empty.
var unbindLua = redis.NewScript(`
for i = 1, #KEYS do
  local n = redis.call("HINCRBY", KEYS[i], ARGV[1], -1)
  if n <= 0 then redis.call("HDEL", KEYS[i], ARGV[1]) end
  if redis.call("HLEN", KEYS[i]) == 0 then redis.call("DEL", KEYS[i]) end
end
return 1`)
