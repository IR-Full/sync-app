package keydir

import (
	"context"
	"testing"
	"time"

	"github.com/IR-Full/sync-app/server/pkg/wire"
)

func publishBody(prekeys ...string) wire.KeyPublishBody {
	return wire.KeyPublishBody{
		IdentityKey: "ik", SigningKey: "sk", SignedPreKey: "spk", SignedPreKeySig: "sig",
		PreKeys: prekeys,
	}
}

func TestMemoryFetchConsumesOneTimePreKey(t *testing.T) {
	d := NewMemory()
	ctx := context.Background()
	d.Publish(ctx, "u1", "d1", publishBody("otp-a", "otp-b"))

	first, ok := d.Fetch(ctx, "u1", "d1")
	if !ok || first.OneTimePreKey != "otp-a" {
		t.Fatalf("first fetch: ok=%v otp=%q", ok, first.OneTimePreKey)
	}
	second, ok := d.Fetch(ctx, "u1", "d1")
	if !ok || second.OneTimePreKey != "otp-b" {
		t.Fatalf("second fetch: ok=%v otp=%q", ok, second.OneTimePreKey)
	}
	// Exhausted: the bundle is still served, just without a one-time prekey —
	// X3DH works without one, and refusing here would take the device offline
	// for new sessions until it topped up.
	third, ok := d.Fetch(ctx, "u1", "d1")
	if !ok || third.OneTimePreKey != "" {
		t.Fatalf("third fetch: ok=%v otp=%q", ok, third.OneTimePreKey)
	}
}

func TestMemoryPreKeysAreCapped(t *testing.T) {
	d := NewMemory().(*memoryDir)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		keys := make([]string, MaxPreKeysPerPublish)
		for j := range keys {
			keys[j] = "k"
		}
		d.Publish(ctx, "u1", "d1", publishBody(keys...))
	}
	if got := len(d.bundles[key("u1", "d1")].oneTime); got > MaxOneTimePreKeys {
		t.Fatalf("one-time prekeys grew to %d, cap is %d", got, MaxOneTimePreKeys)
	}
}

// An entry that has not been republished within EntryTTL must stop being served
// and stop being stored. Without this the directory only ever grew: every device
// that ever connected kept a bundle forever, which is both unbounded storage and
// a metadata trail naming devices that no longer exist.
func TestMemoryEntryExpires(t *testing.T) {
	d := NewMemory().(*memoryDir)
	ctx := context.Background()
	d.Publish(ctx, "u1", "d1", publishBody("otp"))

	// Age the entry past its TTL rather than waiting 90 days for it.
	d.bundles[key("u1", "d1")].expires = time.Now().Add(-time.Minute)

	if _, ok := d.Fetch(ctx, "u1", "d1"); ok {
		t.Fatal("an expired bundle must not be served")
	}
	if _, still := d.bundles[key("u1", "d1")]; still {
		t.Fatal("an expired bundle must not be retained")
	}
	if devices := d.devices["u1"]; len(devices) != 0 {
		t.Fatalf("the device must leave the user's device list, got %v", devices)
	}
}

func TestMemoryFetchAllSkipsExpiredDevices(t *testing.T) {
	d := NewMemory().(*memoryDir)
	ctx := context.Background()
	d.Publish(ctx, "u1", "d1", publishBody("a"))
	d.Publish(ctx, "u1", "d2", publishBody("b"))
	d.bundles[key("u1", "d1")].expires = time.Now().Add(-time.Minute)

	got := d.FetchAll(ctx, "u1")
	if len(got) != 1 || got[0].DeviceID != "d2" {
		t.Fatalf("expected only the live device, got %+v", got)
	}
}

// Republishing must renew the lease — a device in daily use tops up its prekeys
// every start, and that has to be what keeps it addressable.
func TestMemoryPublishRenewsTTL(t *testing.T) {
	d := NewMemory().(*memoryDir)
	ctx := context.Background()
	d.Publish(ctx, "u1", "d1", publishBody("a"))
	d.bundles[key("u1", "d1")].expires = time.Now().Add(-time.Minute)

	d.Publish(ctx, "u1", "d1", publishBody("b"))
	if _, ok := d.Fetch(ctx, "u1", "d1"); !ok {
		t.Fatal("a republished bundle must be served again")
	}
}
