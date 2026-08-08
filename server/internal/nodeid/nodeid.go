// Package nodeid coordinates unique Snowflake node ids across instances using
// Redis. Snowflake correctness requires each running gateway to own a distinct
// id in 0..1023; hostname derivation can collide. This lease claims a free slot
// atomically (SETNX with a TTL) and renews it, so two instances never share an
// id. On release (or crash — the TTL expires) the slot returns to the pool.
package nodeid

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

// Lease claims a unique node id from Redis and keeps it renewed until the
// returned release func is called. The value stored is a per-process owner tag so
// the renewer only refreshes a slot it still owns.
func Lease(ctx context.Context, rdb *redis.Client, ttl time.Duration) (int64, func(), error) {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	owner := fmt.Sprintf("%s-%d", hostname(), os.Getpid())

	for n := 0; n < slots; n++ {
		key := slotKey(n)
		ok, err := rdb.SetNX(ctx, key, owner, ttl).Result()
		if err != nil {
			return 0, nil, err
		}
		if !ok {
			continue // slot taken
		}
		// Won slot n; renew it periodically while we hold it.
		renewCtx, cancel := context.WithCancel(context.Background())
		go renew(renewCtx, rdb, key, owner, ttl)
		release := func() {
			cancel()
			// Only delete if we still own it (avoid deleting a re-leased slot).
			_ = releaseSlot(rdb, key, owner)
		}
		return int64(n), release, nil
	}
	return 0, nil, errors.New("nodeid: no free node-id slot (>1024 instances?)")
}

func renew(ctx context.Context, rdb *redis.Client, key, owner string, ttl time.Duration) {
	t := time.NewTicker(ttl / 2)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// Refresh only if we still own the slot (compare-and-extend).
			_ = renewSlot(rdb, key, owner, ttl)
		}
	}
}

// renewSlot extends the TTL iff the current value is still our owner tag.
func renewSlot(rdb *redis.Client, key, owner string, ttl time.Duration) error {
	const lua = `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("pexpire", KEYS[1], ARGV[2]) else return 0 end`
	return rdb.Eval(context.Background(), lua, []string{key}, owner, ttl.Milliseconds()).Err()
}

// releaseSlot deletes the slot iff we still own it.
func releaseSlot(rdb *redis.Client, key, owner string) error {
	const lua = `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`
	return rdb.Eval(context.Background(), lua, []string{key}, owner).Err()
}

func slotKey(n int) string { return fmt.Sprintf("synapse:nodeid:%d", n) }

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}
