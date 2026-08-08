package router

import "github.com/redis/go-redis/v9"

// bindLua increments a node's refcount and (re)sets the key TTL.
var bindLua = redis.NewScript(`
redis.call("HINCRBY", KEYS[1], ARGV[1], 1)
redis.call("PEXPIRE", KEYS[1], ARGV[2])
return 1`)

// unbindLua decrements; removes the field at zero and the key when empty.
var unbindLua = redis.NewScript(`
local n = redis.call("HINCRBY", KEYS[1], ARGV[1], -1)
if n <= 0 then redis.call("HDEL", KEYS[1], ARGV[1]) end
if redis.call("HLEN", KEYS[1]) == 0 then redis.call("DEL", KEYS[1]) end
return 1`)
