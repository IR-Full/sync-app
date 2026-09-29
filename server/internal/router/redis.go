package router

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

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

// redisRouter is the multi-node routing table. For each user it keeps a Redis
// hash route:<user> mapping nodeID → device refcount, with a TTL so a crashed
// node's bindings self-expire (refreshed by the gateway heartbeat). All ops are
// atomic Lua so concurrent bind/unbind across nodes stay consistent.
type redisRouter struct {
	rdb *redis.Client
	ttl time.Duration
}

// NewRedis builds a Redis-backed router.
func NewRedis(rdb *redis.Client, ttl time.Duration) Router {
	if ttl == 0 {
		ttl = 60 * time.Second
	}
	return &redisRouter{rdb: rdb, ttl: ttl}
}

func routeKey(userID string) string { return "route:" + userID }

// deviceRouteKey indexes ONE device's nodes.
//
// A second key rather than a richer value under routeKey: the user-level lookup
// is on the hot path for every fanout recipient, and making it parse a
// per-device structure would charge every group message for something only the
// E2E relay asks. Both keys carry the same TTL, so a crashed node's entries
// expire from both together.
func deviceRouteKey(userID, deviceID string) string {
	return "route:" + userID + "|" + deviceID
}

func (r *redisRouter) Bind(ctx context.Context, userID, deviceID, nodeID string) error {
	keys := []string{routeKey(userID)}
	if deviceID != "" {
		keys = append(keys, deviceRouteKey(userID, deviceID))
	}
	return bindLua.Run(ctx, r.rdb, keys, nodeID, r.ttl.Milliseconds()).Err()
}

func (r *redisRouter) Unbind(ctx context.Context, userID, deviceID, nodeID string) error {
	keys := []string{routeKey(userID)}
	if deviceID != "" {
		keys = append(keys, deviceRouteKey(userID, deviceID))
	}
	return unbindLua.Run(ctx, r.rdb, keys, nodeID).Err()
}

func (r *redisRouter) NodesFor(ctx context.Context, userID string) ([]string, error) {
	m, err := r.rdb.HKeys(ctx, routeKey(userID)).Result()
	if err != nil {
		return nil, err
	}
	return m, nil
}

// NodesForMany resolves a page of users in one pipelined round trip.
//
// A pipeline rather than a Lua script or one big MGET: the values are hashes, so
// there is no single command that reads many of them, and a script would have to
// declare every key up front anyway. The pipeline is the idiomatic answer and it
// collapses N round trips into one regardless of N.
//
// A per-user error is skipped rather than failing the batch. The caller is
// delivering a message to a group, and one unreadable routing entry should cost
// that recipient a push instead of costing everyone else the message.
func (r *redisRouter) NodesForMany(ctx context.Context, userIDs []string) (map[string][]string, error) {
	if len(userIDs) == 0 {
		return map[string][]string{}, nil
	}
	pipe := r.rdb.Pipeline()
	cmds := make([]*redis.StringSliceCmd, len(userIDs))
	for i, uid := range userIDs {
		cmds[i] = pipe.HKeys(ctx, routeKey(uid))
	}
	// Exec reports the FIRST command error, which for a batch of independent
	// lookups is not a batch failure — the individual results are still there.
	// A transport failure shows up as an error on every command below.
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		allFailed := true
		for _, c := range cmds {
			if c.Err() == nil {
				allFailed = false
				break
			}
		}
		if allFailed {
			return nil, err
		}
	}
	out := make(map[string][]string, len(userIDs))
	for i, c := range cmds {
		nodes, err := c.Result()
		if err != nil || len(nodes) == 0 {
			continue
		}
		out[userIDs[i]] = nodes
	}
	return out, nil
}

func (r *redisRouter) NodesForDevice(ctx context.Context, userID, deviceID string) ([]string, error) {
	if deviceID == "" {
		return r.NodesFor(ctx, userID)
	}
	return r.rdb.HKeys(ctx, deviceRouteKey(userID, deviceID)).Result()
}

// Refresh extends BOTH indexes in one round trip. The per-device key rides the
// same heartbeat because a device whose user-level binding is alive and whose
// device-level binding expired would be reported offline while it is connected
// — which would queue ciphertext for a device already holding the socket.
func (r *redisRouter) Refresh(ctx context.Context, userID, deviceID string) error {
	if deviceID == "" {
		return r.rdb.PExpire(ctx, routeKey(userID), r.ttl).Err()
	}
	pipe := r.rdb.Pipeline()
	pipe.PExpire(ctx, routeKey(userID), r.ttl)
	pipe.PExpire(ctx, deviceRouteKey(userID, deviceID), r.ttl)
	_, err := pipe.Exec(ctx)
	return err
}
