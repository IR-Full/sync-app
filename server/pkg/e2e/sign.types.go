package e2e

import "crypto/ed25519"

// SigningKeyPair is an Ed25519 identity signing key. It is separate from the
// X25519 DH identity key: Ed25519 for signatures, X25519 for key agreement — the
// standard split, using only vetted primitives.
type SigningKeyPair struct {
	Priv ed25519.PrivateKey
	Pub  ed25519.PublicKey
}
