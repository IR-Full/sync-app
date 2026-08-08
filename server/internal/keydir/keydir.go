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

	"github.com/synapse-chat/synapse/pkg/wire"
)

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

func (d *memoryDir) Publish(_ context.Context, userID, deviceID string, b wire.KeyPublishBody) {
	d.mu.Lock()
	defer d.mu.Unlock()
	k := key(userID, deviceID)
	dk := d.bundles[k]
	if dk == nil {
		dk = &deviceKeys{}
		d.bundles[k] = dk
		d.devices[userID] = append(d.devices[userID], deviceID)
	}
	dk.identityKey = b.IdentityKey
	dk.signingKey = b.SigningKey
	dk.signedPreKey = b.SignedPreKey
	dk.signedPreKeySig = b.SignedPreKeySig
	dk.oneTime = append(dk.oneTime, b.PreKeys...)
}

func (d *memoryDir) Fetch(_ context.Context, userID, deviceID string) (wire.KeyBundleBody, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	dk := d.bundles[key(userID, deviceID)]
	if dk == nil || dk.identityKey == "" {
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
