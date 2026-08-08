package keydir

import (
	"context"
	"log/slog"

	"github.com/redis/go-redis/v9"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// NewRedis returns a Redis-backed directory.
func NewRedis(rdb *redis.Client, log *slog.Logger) Directory {
	return &redisDir{rdb: rdb, log: log}
}

func bKey(u, d string) string   { return "keydir:b:" + u + ":" + d }
func otpKey(u, d string) string { return "keydir:otp:" + u + ":" + d }
func devKey(u string) string    { return "keydir:dev:" + u }

func (r *redisDir) Publish(ctx context.Context, userID, deviceID string, b wire.KeyPublishBody) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	pipe := r.rdb.TxPipeline()
	pipe.HSet(ctx, bKey(userID, deviceID), map[string]any{
		"ik": b.IdentityKey, "sk": b.SigningKey, "spk": b.SignedPreKey, "sig": b.SignedPreKeySig,
	})
	pipe.SAdd(ctx, devKey(userID), deviceID)
	if len(b.PreKeys) > 0 {
		vals := make([]any, len(b.PreKeys))
		for i, p := range b.PreKeys {
			vals[i] = p
		}
		pipe.RPush(ctx, otpKey(userID, deviceID), vals...)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		r.log.Warn("keydir publish failed", "user", userID, "err", err)
	}
}

func (r *redisDir) Fetch(ctx context.Context, userID, deviceID string) (wire.KeyBundleBody, bool) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	m, err := r.rdb.HGetAll(ctx, bKey(userID, deviceID)).Result()
	if err != nil || len(m) == 0 || m["ik"] == "" {
		return wire.KeyBundleBody{}, false
	}
	bundle := wire.KeyBundleBody{
		UserID: userID, DeviceID: deviceID, IdentityKey: m["ik"], SigningKey: m["sk"],
		SignedPreKey: m["spk"], SignedPreKeySig: m["sig"],
	}
	// Consume one one-time prekey if available.
	if otp, err := r.rdb.LPop(ctx, otpKey(userID, deviceID)).Result(); err == nil {
		bundle.OneTimePreKey = otp
	}
	return bundle, true
}

func (r *redisDir) FetchAll(ctx context.Context, userID string) []wire.KeyBundleBody {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	devs, err := r.rdb.SMembers(ctx, devKey(userID)).Result()
	if err != nil {
		r.log.Warn("keydir fetchall failed", "user", userID, "err", err)
		return nil
	}
	var out []wire.KeyBundleBody
	for _, dev := range devs {
		if b, ok := r.Fetch(ctx, userID, dev); ok {
			out = append(out, b)
		}
	}
	return out
}
