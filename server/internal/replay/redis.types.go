package replay

import (
	"time"

	"github.com/redis/go-redis/v9"
)

// redisBuffer stores each session's recent frames in a capped Redis stream
// (resume:<session>), so a client can resume on ANY node. XADD with MAXLEN
// bounds memory; the key TTL reaps sessions that never resume.
type redisBuffer struct {
	rdb *redis.Client
	ttl time.Duration
}
