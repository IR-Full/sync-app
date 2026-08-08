// Package e2e implements the cryptography for optional end-to-end encrypted
// "secret chats" (Section 4B). It uses ONLY standard, audited primitives — no
// home-grown crypto:
//
//   - X25519 (crypto/ecdh) for Diffie-Hellman key agreement
//   - HKDF-SHA256 for key derivation
//   - HMAC-SHA256 for the symmetric-key ratchet step
//   - ChaCha20-Poly1305 for authenticated encryption (AEAD)
//
// On top of these it builds X3DH (initial key agreement) and the Double Ratchet
// (per-message forward secrecy + post-compromise security), i.e. the Signal
// design. The server never sees any of these keys or the plaintext — it only
// relays opaque ciphertext between devices.
package e2e

import (
	"crypto/ecdh"
	"crypto/rand"
)

// GenerateKeyPair creates a fresh X25519 key pair.
func GenerateKeyPair() (*KeyPair, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &KeyPair{Priv: priv, Pub: priv.PublicKey()}, nil
}

// PublicBytes returns the 32-byte public key.
func (k *KeyPair) PublicBytes() []byte { return k.Pub.Bytes() }

// PublicKeyFromBytes parses a 32-byte X25519 public key.
func PublicKeyFromBytes(b []byte) (*ecdh.PublicKey, error) {
	return ecdh.X25519().NewPublicKey(b)
}

// dh performs X25519 and returns the shared secret.
func dh(priv *ecdh.PrivateKey, pub *ecdh.PublicKey) ([]byte, error) {
	return priv.ECDH(pub)
}
