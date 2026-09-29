package e2e

import (
	"crypto/sha512"
	"encoding/binary"
	"fmt"
	"strings"
)

// Safety numbers let two users verify, out of band, that no man-in-the-middle
// has substituted keys at the directory. X3DH authenticates the signed prekey
// against the identity signing key, but nothing stops a malicious directory from
// serving an attacker's *identity* key to both sides. Comparing safety numbers
// (read aloud, or scanned) closes that gap: if the numbers match, both parties
// hold each other's genuine long-term identity keys.
//
// The construction follows Signal's fingerprint scheme: each party's identity is
// reduced to a stable per-user fingerprint by iterated hashing (deliberately slow
// to make brute-forcing a colliding key pair expensive), and the two fingerprints
// are combined in a canonical order so both sides derive the identical number.

const (
	// safetyIterations is the hash-iteration count. Matching Signal, it makes
	// searching for a key that yields a chosen fingerprint computationally costly.
	safetyIterations = 5200
	// fingerprintBytes is how many bytes of the final digest form one party's
	// fingerprint (30 bytes → six 5-digit groups).
	fingerprintBytes = 30
	// safetyVersion guards against cross-version fingerprint collisions.
	safetyVersion = 0
)

// identityMaterial concatenates a party's long-term public keys. Binding BOTH the
// X25519 key-agreement identity key and the Ed25519 signing identity key means a
// MITM must forge the entire long-term identity, not just one half of it.
func identityMaterial(identityKey, signingKey []byte) []byte {
	m := make([]byte, 0, len(identityKey)+len(signingKey))
	m = append(m, identityKey...)
	m = append(m, signingKey...)
	return m
}

// fingerprint reduces one party's identity to a 30-byte digest via iterated
// SHA-512, salted with a stable identifier (e.g. the user id) so two users who
// somehow shared a key still get distinct fingerprints.
func fingerprint(identityKey, signingKey []byte, stableID string) []byte {
	key := identityMaterial(identityKey, signingKey)
	var ver [2]byte
	binary.BigEndian.PutUint16(ver[:], safetyVersion)

	// Initial input: version || key || stableID.
	buf := make([]byte, 0, len(ver)+len(key)+len(stableID))
	buf = append(buf, ver[:]...)
	buf = append(buf, key...)
	buf = append(buf, []byte(stableID)...)
	h := sha512.Sum512(buf)

	// Each round folds the key back in, so the work cannot be precomputed without
	// the key.
	next := make([]byte, 0, len(h)+len(key))
	for i := 0; i < safetyIterations; i++ {
		next = append(next[:0], h[:]...)
		next = append(next, key...)
		h = sha512.Sum512(next)
	}
	return h[:fingerprintBytes]
}

// displayFingerprint renders a 30-byte fingerprint as six space-separated groups
// of five decimal digits (each group is 5 bytes reduced mod 100000).
func displayFingerprint(fp []byte) string {
	var groups []string
	for i := 0; i+5 <= len(fp); i += 5 {
		var b [8]byte
		copy(b[3:], fp[i:i+5]) // 5 bytes → big-endian uint40
		v := binary.BigEndian.Uint64(b[:]) % 100000
		groups = append(groups, fmt.Sprintf("%05d", v))
	}
	return strings.Join(groups, " ")
}

// SafetyNumber returns the symmetric 60-digit safety number for a conversation
// between two identities. Both parties compute the identical string regardless of
// argument order, so either can read it out for comparison. A changed identity
// key on either side changes the number — that is exactly the MITM signal.
//
// Each identity is (X25519 identityKey, Ed25519 signingKey, stableID). stableID
// is any stable per-user handle (user id) both sides agree on.
func SafetyNumber(localID string, localIdentityKey, localSigningKey []byte,
	remoteID string, remoteIdentityKey, remoteSigningKey []byte) string {
	local := displayFingerprint(fingerprint(localIdentityKey, localSigningKey, localID))
	remote := displayFingerprint(fingerprint(remoteIdentityKey, remoteSigningKey, remoteID))
	// Canonical order: the lexicographically smaller fingerprint comes first, so
	// both endpoints — which disagree on which side is "local" — agree on the
	// combined number.
	if local <= remote {
		return local + " " + remote
	}
	return remote + " " + local
}
