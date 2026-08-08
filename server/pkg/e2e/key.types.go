package e2e

import "crypto/ecdh"

// KeyPair is an X25519 key pair.
type KeyPair struct {
	Priv *ecdh.PrivateKey
	Pub  *ecdh.PublicKey
}
