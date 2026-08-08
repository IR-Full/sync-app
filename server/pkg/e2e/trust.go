package e2e

import (
	"crypto/subtle"
	"time"
)

// A safety number lets two people DETECT a swapped identity key — if they think
// to compare it. Pinning is the half that does not depend on anyone remembering
// to look: the first time a peer device is seen, its identity key is recorded,
// and every later session with that device is checked against the record.
//
// This is trust-on-first-use, and its limits are worth stating plainly. It does
// not protect the first contact: a directory that lies from the very beginning
// is believed. What it does is make the ATTACK WINDOW a single moment rather
// than every session forever — a server that later starts handing out its own
// keys is caught immediately, on every existing conversation at once, which is
// the realistic version of the threat. The unpinned alternative silently accepts
// a new key at any time.
//
// A changed key is not automatically an attack (reinstalls happen), so this
// REPORTS rather than decides. The client shows the warning, the human chooses,
// and Accept records the new key once they do.

// NewTrustStore returns an empty store.
func NewTrustStore() *TrustStore {
	return &TrustStore{pinned: map[string]PinnedIdentity{}}
}

func trustKey(userID, deviceID string) string { return userID + "|" + deviceID }

// Verify checks a peer device's keys against what was pinned for it.
//
// It returns (true, nil) when this is the first sighting — the caller should
// Accept after the session is established — and (false, nil) when the keys match
// the pin. A mismatch is ErrIdentityChanged, and the caller must NOT proceed
// silently: that is the moment the safety number is worth showing.
func (t *TrustStore) Verify(userID, deviceID string, identityKey, signingKey []byte) (firstUse bool, err error) {
	t.mu.RLock()
	p, ok := t.pinned[trustKey(userID, deviceID)]
	t.mu.RUnlock()
	if !ok {
		return true, nil
	}
	// Constant time: the comparison is on public keys, but a timing signal here
	// would leak which prefix of a forged key is correct, which is a free hint to
	// anyone grinding one.
	if subtle.ConstantTimeCompare(p.IdentityKey, identityKey) != 1 ||
		subtle.ConstantTimeCompare(p.SigningKey, signingKey) != 1 {
		return false, ErrIdentityChanged
	}
	return false, nil
}

// Accept records (or replaces) the pin for a peer device. Replacing one is an
// explicit act: it should follow a human confirming the change, not a client
// deciding on its own that the new key is fine.
func (t *TrustStore) Accept(userID, deviceID string, identityKey, signingKey []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pinned[trustKey(userID, deviceID)] = PinnedIdentity{
		UserID: userID, DeviceID: deviceID,
		IdentityKey: append([]byte(nil), identityKey...),
		SigningKey:  append([]byte(nil), signingKey...),
		FirstSeen:   time.Now(),
	}
}

// Pinned returns what is recorded for a peer device.
func (t *TrustStore) Pinned(userID, deviceID string) (PinnedIdentity, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	p, ok := t.pinned[trustKey(userID, deviceID)]
	return p, ok
}

// Forget drops a pin (an explicit "trust this device fresh next time").
func (t *TrustStore) Forget(userID, deviceID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.pinned, trustKey(userID, deviceID))
}

// All returns every pin, for a client that wants to show or export them.
func (t *TrustStore) All() []PinnedIdentity {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]PinnedIdentity, 0, len(t.pinned))
	for _, p := range t.pinned {
		out = append(out, p)
	}
	return out
}
