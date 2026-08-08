package presence

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/synapse-chat/synapse/internal/model"
)

// NewRedisBackend connects to Redis (e.g. "localhost:6379").
func NewRedisBackend(addr, password string, db int) (Backend, error) {
	rdb := redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		return nil, err
	}
	return &redisBackend{rdb: rdb}, nil
}

func onlineKey(u string) string   { return "presence:online:" + u }
func lastSeenKey(u string) string { return "presence:lastseen:" + u }

func (b *redisBackend) SetOnline(ctx context.Context, userID string, ttl time.Duration) error {
	return b.rdb.Set(ctx, onlineKey(userID), nowMs(), ttl).Err()
}

func (b *redisBackend) SetOffline(ctx context.Context, userID string, lastSeen int64) error {
	pipe := b.rdb.TxPipeline()
	pipe.Del(ctx, onlineKey(userID))
	pipe.Set(ctx, lastSeenKey(userID), lastSeen, 0)
	_, err := pipe.Exec(ctx)
	return err
}

func (b *redisBackend) Get(ctx context.Context, userID string) (model.Presence, error) {
	p := model.Presence{UserID: userID}
	if v, err := b.rdb.Get(ctx, onlineKey(userID)).Result(); err == nil {
		p.Online = true
		p.LastSeenMs, _ = strconv.ParseInt(v, 10, 64)
		return p, nil
	} else if err != redis.Nil {
		return p, err
	}
	if v, err := b.rdb.Get(ctx, lastSeenKey(userID)).Result(); err == nil {
		p.LastSeenMs, _ = strconv.ParseInt(v, 10, 64)
	} else if err != redis.Nil {
		return p, err
	}
	return p, nil
}
