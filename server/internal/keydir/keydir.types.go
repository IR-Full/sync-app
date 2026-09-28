package keydir

import (
	"context"
	"sync"
	"time"

	"github.com/SyncApp-chat/SyncApp/pkg/wire"
)

// Directory stores and serves public prekey bundles.
//
// Every method takes a context: a directory call can cross a network (Redis, or
// gRPC to keydird), and the rest of the system is ctx-first for exactly that
// reason — a request that goes away should not leave work running behind it, and
// a trace should not stop at this boundary.
type Directory interface {
	// Publish stores/refreshes a device's identity + signed prekey and appends new
	// one-time prekeys, returning what the directory now holds.
	//
	// It RETURNS something now, and that is the point of the change. Publishing used
	// to be write-only, so a device could not learn its own one-time prekey balance —
	// and it cannot learn it any other way, because those keys are consumed by PEERS
	// fetching bundles. A device that runs dry silently drops to the weaker three-DH
	// handshake, and nobody involved finds out: the peer cannot see the difference and
	// the owner never hears.
	Publish(ctx context.Context, userID, deviceID string, b wire.KeyPublishBody) State
	// Fetch returns a consumable bundle for a peer device (pops one one-time
	// prekey), or ok=false if the device published nothing.
	Fetch(ctx context.Context, userID, deviceID string) (wire.KeyBundleBody, bool)
	// FetchAll returns a bundle for every device of a user (multi-device sync).
	FetchAll(ctx context.Context, userID string) []wire.KeyBundleBody
}

// State is what the directory holds for one device after a publish.
type State struct {
	// OneTimePreKeysLeft is the count AFTER the publish was applied and trimmed.
	OneTimePreKeysLeft int
	// Accepted is how many prekeys from THIS frame survived the per-publish cap and
	// the per-device ceiling. A client that keeps the private halves needs it: without
	// it, it holds private keys for public ones the directory dropped, and cannot tell
	// which.
	Accepted int
	// SignedPreKeyFirstSeen is when the CURRENT signed prekey first appeared. Zero when
	// this publish introduced it. The client decides when to rotate; it cannot know how
	// old the stored one is, because it cannot know whether its own last publish landed.
	SignedPreKeyFirstSeen time.Time
}

type deviceKeys struct {
	identityKey     string
	signingKey      string
	signedPreKey    string
	signedPreKeySig string
	// spkFirstSeen is when signedPreKey was first stored, so a rotating client can be
	// told the age of what the directory actually has rather than what it believes it
	// sent.
	spkFirstSeen time.Time
	oneTime      []string
	// expires mirrors the Redis backend's EntryTTL so the two behave alike. It is
	// enforced lazily, on read: a directory with no readers has no stale answers
	// to give, and a sweeper goroutine per process for a map that only a dev or
	// single-node run ever holds is not worth its own failure mode.
	expires time.Time
}

// memoryDir is the in-process backend (single node / dev / tests).
type memoryDir struct {
	mu      sync.Mutex
	bundles map[string]*deviceKeys
	devices map[string][]string
}
