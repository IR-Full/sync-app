package presence

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

/*
 * The Redis backend needs a real Redis, so the read/write paths live in the
 * integration test below and skip without one. The key-namespacing functions are
 * pure, though, and they are worth pinning on their own: the two keys carry
 * different lifetimes — "online" expires on a TTL, "last seen" is durable — so a
 * collision between them would make a graceful sign-off expire a minute later
 * and resurrect the user as permanently online.
 */

func TestPresenceKeysNeverCollide(t *testing.T) {
	if onlineKey("u1") == lastSeenKey("u1") {
		t.Fatal("online and last-seen share a key; the TTL would delete the durable value")
	}
}

func TestPresenceKeysAreNamespaced(t *testing.T) {
	// Redis is shared with the key directory and the router; an unprefixed user
	// id would collide with whatever else keys on one.
	for _, key := range []string{onlineKey("u1"), lastSeenKey("u1")} {
		if !strings.HasPrefix(key, "presence:") {
			t.Errorf("key %q is not namespaced", key)
		}
	}
}

func TestPresenceKeysSeparateUsers(t *testing.T) {
	if onlineKey("u1") == onlineKey("u2") {
		t.Error("two users share an online key")
	}
	if lastSeenKey("u1") == lastSeenKey("u2") {
		t.Error("two users share a last-seen key")
	}
}

func TestPresenceKeysCarryTheUserID(t *testing.T) {
	// Operators grep these directly when a user reports being stuck online.
	if !strings.HasSuffix(onlineKey("u1"), "u1") {
		t.Errorf("online key %q does not end in the user id", onlineKey("u1"))
	}
	if !strings.HasSuffix(lastSeenKey("u1"), "u1") {
		t.Errorf("last-seen key %q does not end in the user id", lastSeenKey("u1"))
	}
}

// TestRedisBackendRoundTrip exercises the real backend. It runs only when
// SYNCAPP_TEST_REDIS_ADDR points at a Redis, matching how the rest of this
// repository gates its integration tests.
func TestRedisBackendRoundTrip(t *testing.T) {
	addr := os.Getenv("SYNCAPP_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set SYNCAPP_TEST_REDIS_ADDR to run the Redis presence test")
	}
	backend, err := NewRedisBackend(addr, "", 0)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	ctx := context.Background()
	user := "presence-test-" + time.Now().Format("150405.000")

	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()
	t.Cleanup(func() { rdb.Del(ctx, onlineKey(user), lastSeenKey(user)) })

	if err := backend.SetOnline(ctx, user, time.Minute); err != nil {
		t.Fatalf("set online: %v", err)
	}
	got, err := backend.Get(ctx, user)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Online {
		t.Error("the user was not reported online")
	}
	if got.LastSeenMs == 0 {
		t.Error("an online user has no timestamp")
	}

	// The TTL is what makes a crashed gateway self-correcting.
	ttl, err := rdb.TTL(ctx, onlineKey(user)).Result()
	if err != nil {
		t.Fatalf("ttl: %v", err)
	}
	if ttl <= 0 {
		t.Errorf("online key has no expiry (ttl=%v); a crashed node would leave it online forever", ttl)
	}

	if err := backend.SetOffline(ctx, user, 1_700_000); err != nil {
		t.Fatalf("set offline: %v", err)
	}
	got, err = backend.Get(ctx, user)
	if err != nil {
		t.Fatalf("get after offline: %v", err)
	}
	if got.Online {
		t.Error("the user is still online after signing off")
	}
	if got.LastSeenMs != 1_700_000 {
		t.Errorf("last seen = %d, want the value that was stored", got.LastSeenMs)
	}

	// Going offline must not leave the TTL'd online key behind.
	if n, _ := rdb.Exists(ctx, onlineKey(user)).Result(); n != 0 {
		t.Error("the online key survived a graceful sign-off")
	}
}

func TestRedisBackendReportsAnUnknownUserAsOffline(t *testing.T) {
	addr := os.Getenv("SYNCAPP_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set SYNCAPP_TEST_REDIS_ADDR to run the Redis presence test")
	}
	backend, err := NewRedisBackend(addr, "", 0)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	got, err := backend.Get(context.Background(), "presence-test-never-seen")
	if err != nil {
		t.Fatalf("a missing key must not be an error: %v", err)
	}
	if got.Online {
		t.Error("an unknown user was reported online")
	}
}
