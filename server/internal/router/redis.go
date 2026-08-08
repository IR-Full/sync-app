package router

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// NewRedis builds a Redis-backed router.
func NewRedis(rdb *redis.Client, ttl time.Duration) Router {
	if ttl == 0 {
		ttl = 60 * time.Second
	}
	return &redisRouter{rdb: rdb, ttl: ttl}
}

func routeKey(userID string) string { return "route:" + userID }

func (r *redisRouter) Bind(ctx context.Context, userID, _, nodeID string) error {
	return bindLua.Run(ctx, r.rdb, []string{routeKey(userID)}, nodeID, r.ttl.Milliseconds()).Err()
}

func (r *redisRouter) Unbind(ctx context.Context, userID, _, nodeID string) error {
	return unbindLua.Run(ctx, r.rdb, []string{routeKey(userID)}, nodeID).Err()
}

func (r *redisRouter) NodesFor(ctx context.Context, userID string) ([]string, error) {
	m, err := r.rdb.HKeys(ctx, routeKey(userID)).Result()
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (r *redisRouter) Refresh(ctx context.Context, userID, _ string) error {
	return r.rdb.PExpire(ctx, routeKey(userID), r.ttl).Err()
}
