// Package keydir is the key directory for E2E secret chats. Devices publish
// their public prekey bundles here so peers can start an encrypted session while
// they are offline (asynchronous X3DH). The server stores ONLY public keys — it
// can never derive a shared secret or read messages. One-time prekeys are
// consumed on fetch to preserve forward secrecy for the initial message.
//
// Directory is an interface with an in-memory backend (single node) and a Redis
// backend (shared across nodes) — because a prekey published on node A must be
// visible when a peer connected to node B starts a session.
package keydir

import (
	"context"
	"sync"
	"time"

	"github.com/SyncApp-chat/SyncApp/pkg/wire"
)

// opTimeout bounds one directory round trip when the caller's context carries no
// deadline of its own. A backend can be a remote Redis or another process, so a
// client waiting on a prekey fetch must not be able to wait forever because a
// dependency wedged.
const opTimeout = 3 * time.Second

// MaxOneTimePreKeys caps the one-time prekeys the directory holds per device.
//
// Prekeys are ACCUMULATED across publishes — that is the point, since each is
// consumed by one fetch — so without a ceiling a device can grow its own bucket
// without bound by publishing repeatedly, and the store is remembered per device
// in memory or in Redis with no expiry. The cap is generous next to real client
// behaviour (clients top up a small reserve, ~100), so the only thing it stops is
// the unbounded case. Over the cap the OLDEST are dropped: a fresh key is the one
// worth keeping, and the sender's X3DH works with no one-time prekey at all.
const MaxOneTimePreKeys = 256

// MaxPreKeysPerPublish bounds a single KEY_PUBLISH frame, so one request cannot
// cost an unbounded write even though the total is capped.
const MaxPreKeysPerPublish = 128

// EntryTTL is how long a device's directory entry survives without a republish.
//
// Without one the directory only ever grew: every device that ever connected
// kept a bundle, a prekey list and a set membership forever, including devices
// wiped, reinstalled or logged out years ago. That is both unbounded storage and
// a needless metadata trail — the entry names a device that no longer exists.
//
// The value is generous on purpose. Clients republish whenever they start (they
// have to: one-time prekeys are consumed by fetches and need topping up), so a
// device in real use refreshes this many times over. A device that has not
// published in three months is not one a peer should be opening a session with.
const EntryTTL = 90 * 24 * time.Hour

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

// opCtx returns the caller's context when it already has a deadline, else one
// bounded by opTimeout. Cancellation and trace context propagate either way.
func opCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, opTimeout)
}

// NewMemory returns an in-process directory.
func NewMemory() Directory {
	return &memoryDir{bundles: make(map[string]*deviceKeys), devices: make(map[string][]string)}
}

func key(userID, deviceID string) string { return userID + "|" + deviceID }

func (d *memoryDir) Publish(_ context.Context, userID, deviceID string, b wire.KeyPublishBody) State {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	k := key(userID, deviceID)
	dk := d.bundles[k]
	if dk == nil {
		dk = &deviceKeys{}
		d.bundles[k] = dk
		d.devices[userID] = append(d.devices[userID], deviceID)
	}
	dk.identityKey = b.IdentityKey
	dk.signingKey = b.SigningKey
	// The timestamp moves only when the KEY changes. Stamping it on every publish
	// would reset the age on each reconnect, so a signed prekey that has never
	// rotated would report itself as fresh forever — which is exactly the state the
	// age exists to make visible.
	if dk.signedPreKey != b.SignedPreKey {
		dk.signedPreKey = b.SignedPreKey
		dk.signedPreKeySig = b.SignedPreKeySig
		dk.spkFirstSeen = now
	} else {
		// Same key, possibly a re-signature. Take the signature anyway: a client that
		// re-signed the same prekey is entitled to have the newer signature served.
		dk.signedPreKeySig = b.SignedPreKeySig
	}
	var accepted int
	dk.oneTime, accepted = appendCapped(dk.oneTime, b.PreKeys)
	dk.expires = now.Add(EntryTTL)
	return State{
		OneTimePreKeysLeft:    len(dk.oneTime),
		Accepted:              accepted,
		SignedPreKeyFirstSeen: dk.spkFirstSeen,
	}
}

// appendCapped adds new prekeys, trims the oldest past MaxOneTimePreKeys, and reports
// how many of the INCOMING ones survived.
func appendCapped(existing, incoming []string) ([]string, int) {
	if len(incoming) > MaxPreKeysPerPublish {
		incoming = incoming[:MaxPreKeysPerPublish]
	}
	pushed := len(incoming)
	out := append(existing, incoming...)
	if len(out) > MaxOneTimePreKeys {
		// Re-slice into a fresh backing array; keeping the tail of the old one would
		// hold the dropped keys alive for as long as the device stays published.
		out = append([]string(nil), out[len(out)-MaxOneTimePreKeys:]...)
	}
	return out, survivors(pushed, len(out))
}

// survivors reports how many of a publish's prekeys are still stored, given how many
// were pushed and how long the list is afterwards.
//
// Both backends trim from the FRONT, oldest first, and a publish appends to the back —
// so this frame's keys are the last to go. That makes the answer `min(pushed, left)`
// and nothing more: below the ceiling every pushed key survived, and at the ceiling the
// list is entirely the newest keys, which are this frame's until it runs out.
//
// It matters because a client keeps the PRIVATE halves. Told it stored 100 when the
// directory kept 20, it holds eighty private keys for public ones no peer can ever
// fetch — and believes its reserve is four times what it is.
func survivors(pushed, left int) int {
	if pushed < left {
		return pushed
	}
	return left
}

func (d *memoryDir) Fetch(_ context.Context, userID, deviceID string) (wire.KeyBundleBody, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	dk := d.bundles[key(userID, deviceID)]
	if dk == nil || dk.identityKey == "" {
		return wire.KeyBundleBody{}, false
	}
	// An entry past its TTL is dropped rather than served. Serving it would hand
	// a peer keys for a device that has not been seen in three months, and
	// keeping it would let the map grow with every device that ever connected.
	if !dk.expires.IsZero() && time.Now().After(dk.expires) {
		d.forgetLocked(userID, deviceID)
		return wire.KeyBundleBody{}, false
	}
	bundle := wire.KeyBundleBody{
		UserID: userID, DeviceID: deviceID, IdentityKey: dk.identityKey, SigningKey: dk.signingKey,
		SignedPreKey: dk.signedPreKey, SignedPreKeySig: dk.signedPreKeySig,
	}
	if len(dk.oneTime) > 0 {
		bundle.OneTimePreKey = dk.oneTime[0]
		dk.oneTime = dk.oneTime[1:]
	}
	return bundle, true
}

// forgetLocked removes a device's entry and its place in the user's device list.
// Caller holds the write lock.
func (d *memoryDir) forgetLocked(userID, deviceID string) {
	delete(d.bundles, key(userID, deviceID))
	devices := d.devices[userID]
	for i, id := range devices {
		if id != deviceID {
			continue
		}
		d.devices[userID] = append(devices[:i:i], devices[i+1:]...)
		break
	}
	if len(d.devices[userID]) == 0 {
		delete(d.devices, userID)
	}
}

func (d *memoryDir) FetchAll(ctx context.Context, userID string) []wire.KeyBundleBody {
	d.mu.Lock()
	deviceIDs := append([]string(nil), d.devices[userID]...)
	d.mu.Unlock()
	var out []wire.KeyBundleBody
	for _, dev := range deviceIDs {
		if b, ok := d.Fetch(ctx, userID, dev); ok {
			out = append(out, b)
		}
	}
	return out
}
