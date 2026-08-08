package presence

import "github.com/redis/go-redis/v9"

// redisBackend stores presence in Redis. "online" is a key with a TTL: if the
// gateway stops heartbeating (crash, network drop) the key expires and the user
// is implicitly offline — no cleanup job needed. last-seen is a separate durable
// key updated on graceful offline.
type redisBackend struct {
	rdb *redis.Client
}
