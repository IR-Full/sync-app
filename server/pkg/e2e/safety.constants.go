package e2e

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
