package e2e

import (
	"crypto/ed25519"
	"crypto/rand"
)

// GenerateSigningKey creates a fresh Ed25519 identity signing key.
func GenerateSigningKey() (*SigningKeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &SigningKeyPair{Priv: priv, Pub: pub}, nil
}

// PublicBytes returns the 32-byte Ed25519 public key.
func (k *SigningKeyPair) PublicBytes() []byte { return k.Pub }

// SignPreKey signs a signed-prekey's public bytes with the identity signing key.
// Publishers call this so peers can verify the prekey really came from them.
func SignPreKey(priv ed25519.PrivateKey, signedPreKeyPub []byte) []byte {
	return ed25519.Sign(priv, signedPreKeyPub)
}

// VerifyPreKey checks a signed-prekey signature against the identity signing key.
func VerifyPreKey(signingPub, signedPreKeyPub, sig []byte) bool {
	if len(signingPub) != ed25519.PublicKeySize || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(signingPub, signedPreKeyPub, sig)
}
