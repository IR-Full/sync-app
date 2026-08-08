package keydir

import (
	"context"
	"sync"

	"github.com/synapse-chat/synapse/pkg/wire"
)

// Directory stores and serves public prekey bundles.
//
// Every method takes a context: a directory call can cross a network (Redis, or
// gRPC to keydird), and the rest of the system is ctx-first for exactly that
// reason — a request that goes away should not leave work running behind it, and
// a trace should not stop at this boundary.
type Directory interface {
	// Publish stores/refreshes a device's identity + signed prekey and appends
	// new one-time prekeys.
	Publish(ctx context.Context, userID, deviceID string, b wire.KeyPublishBody)
	// Fetch returns a consumable bundle for a peer device (pops one one-time
	// prekey), or ok=false if the device published nothing.
	Fetch(ctx context.Context, userID, deviceID string) (wire.KeyBundleBody, bool)
	// FetchAll returns a bundle for every device of a user (multi-device sync).
	FetchAll(ctx context.Context, userID string) []wire.KeyBundleBody
}

type deviceKeys struct {
	identityKey     string
	signingKey      string
	signedPreKey    string
	signedPreKeySig string
	oneTime         []string
}

// memoryDir is the in-process backend (single node / dev / tests).
type memoryDir struct {
	mu      sync.Mutex
	bundles map[string]*deviceKeys
	devices map[string][]string
}
