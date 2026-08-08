package replay

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// NewRedis returns a Redis-backed replay buffer.
func NewRedis(rdb *redis.Client, ttl time.Duration) Buffer {
	if ttl == 0 {
		ttl = 10 * time.Minute
	}
	return &redisBuffer{rdb: rdb, ttl: ttl}
}

func streamKey(sessionID string) string { return "resume:" + sessionID }

func (b *redisBuffer) Append(ctx context.Context, sessionID string, seq uint64, payload []byte) error {
	key := streamKey(sessionID)
	pipe := b.rdb.TxPipeline()
	pipe.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		MaxLen: maxFrames,
		Approx: true,
		Values: map[string]any{"seq": seq, "p": payload},
	})
	pipe.Expire(ctx, key, b.ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (b *redisBuffer) Since(ctx context.Context, sessionID string, afterSeq uint64) ([]Frame, error) {
	msgs, err := b.rdb.XRange(ctx, streamKey(sessionID), "-", "+").Result()
	if err != nil {
		return nil, err
	}
	var out []Frame
	for _, m := range msgs {
		seq, _ := strconv.ParseUint(toStr(m.Values["seq"]), 10, 64)
		if seq <= afterSeq {
			continue
		}
		out = append(out, Frame{Seq: seq, Payload: []byte(toStr(m.Values["p"]))})
	}
	return out, nil
}

func (b *redisBuffer) Drop(ctx context.Context, sessionID string) error {
	return b.rdb.Del(ctx, streamKey(sessionID)).Err()
}

func toStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
