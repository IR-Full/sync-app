package e2e

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
)

// ErrBadPreKeySignature means a prekey bundle's signed prekey was not validly
// signed by the advertised identity signing key — the bundle must be rejected,
// as it may have been substituted by a malicious key directory (MITM).
var ErrBadPreKeySignature = errors.New("e2e: bad signed-prekey signature")

// SigningKeyPair is an Ed25519 identity signing key. It is separate from the
// X25519 DH identity key: Ed25519 for signatures, X25519 for key agreement — the
// standard split, using only vetted primitives.
type SigningKeyPair struct {
	Priv ed25519.PrivateKey
	Pub  ed25519.PublicKey
}

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
