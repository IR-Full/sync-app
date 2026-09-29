package keydir

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/IR-Full/sync-app/server/pkg/wire"
)

// redisDir is the shared, multi-node key directory. Per device it stores a hash
// keydir:b:<user>:<device> (identity/signing/spk/sig), a list
// keydir:otp:<user>:<device> of one-time prekeys (LPOP on fetch), and a set
// keydir:dev:<user> of the user's device ids. Only public keys are stored, so a
// leak never enables decryption.
type redisDir struct {
	rdb *redis.Client
	log *slog.Logger
}

// NewRedis returns a Redis-backed directory.
func NewRedis(rdb *redis.Client, log *slog.Logger) Directory {
	return &redisDir{rdb: rdb, log: log}
}

func bKey(u, d string) string   { return "keydir:b:" + u + ":" + d }
func otpKey(u, d string) string { return "keydir:otp:" + u + ":" + d }
func devKey(u string) string    { return "keydir:dev:" + u }

func (r *redisDir) Publish(ctx context.Context, userID, deviceID string, b wire.KeyPublishBody) State {
	ctx, cancel := opCtx(ctx)
	defer cancel()

	// The signed prekey's age needs the PREVIOUS value, so it has to be read first.
	// Outside the transaction rather than under a WATCH loop: the only writer for a
	// given device is that device, so there is no second party to race.
	previous, _ := r.rdb.HMGet(ctx, bKey(userID, deviceID), "spk", "spk_at").Result()
	firstSeen := time.Now()
	if len(previous) == 2 {
		if stored, ok := previous[0].(string); ok && stored == b.SignedPreKey {
			// Unchanged key, so keep the original timestamp. Refreshing it on every
			// publish would make a prekey that has never rotated report itself as
			// fresh forever — the exact state the age exists to expose.
			if at, ok := previous[1].(string); ok {
				if ms, err := strconv.ParseInt(at, 10, 64); err == nil {
					firstSeen = time.UnixMilli(ms)
				}
			}
		}
	}

	keys := b.PreKeys
	if len(keys) > MaxPreKeysPerPublish {
		keys = keys[:MaxPreKeysPerPublish]
	}

	otp := otpKey(userID, deviceID)
	pipe := r.rdb.TxPipeline()
	pipe.HSet(ctx, bKey(userID, deviceID), map[string]any{
		"ik": b.IdentityKey, "sk": b.SigningKey, "spk": b.SignedPreKey, "sig": b.SignedPreKeySig,
		"spk_at": strconv.FormatInt(firstSeen.UnixMilli(), 10),
	})
	pipe.SAdd(ctx, devKey(userID), deviceID)
	// Refresh the expiry of everything this publish touches. Redis has no
	// per-field or per-member TTL, so the unit is the whole key — which is the
	// right unit anyway: a device's bundle, its prekey list and its membership in
	// the user's device set all become meaningless together.
	pipe.Expire(ctx, bKey(userID, deviceID), EntryTTL)
	pipe.Expire(ctx, devKey(userID), EntryTTL)
	if len(keys) > 0 {
		vals := make([]any, len(keys))
		for i, p := range keys {
			vals[i] = p
		}
		pipe.RPush(ctx, otp, vals...)
		// Trim to the newest MaxOneTimePreKeys inside the same transaction. The list
		// has no expiry and is only drained one key per fetch, so repeated publishes
		// would otherwise grow it in Redis without bound.
		pipe.LTrim(ctx, otp, int64(-MaxOneTimePreKeys), -1)
		pipe.Expire(ctx, otp, EntryTTL)
	}
	// Queued AFTER the trim, inside the same transaction, so it reports the post-trim
	// length. A second round trip would race a peer's concurrent fetch and report a
	// count that was never true.
	remaining := pipe.LLen(ctx, otp)

	if _, err := pipe.Exec(ctx); err != nil {
		r.log.Warn("keydir publish failed", "user", userID, "err", err)
		// Part of the write may have landed, so the counts are UNKNOWN rather than
		// zero. Reporting zero would tell the client it holds no prekeys and provoke a
		// republish of a batch that is very likely already stored.
		return State{}
	}

	left := int(remaining.Val())
	return State{
		OneTimePreKeysLeft:    left,
		Accepted:              survivors(len(keys), left),
		SignedPreKeyFirstSeen: firstSeen,
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
