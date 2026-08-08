import { ed25519, x25519 } from '@noble/curves/ed25519.js'

/**
 * Key primitives for secret chats, matching `server/pkg/e2e`:
 * X25519 for agreement, Ed25519 for prekey signatures.
 *
 * Implemented with @noble rather than WebCrypto because the ratchet needs
 * ChaCha20-Poly1305, which WebCrypto does not provide at all — so the crypto
 * stack has to be a library anyway, and mixing two would only add ways for the
 * key formats to disagree.
 */
export interface KeyPair {
  privateKey: Uint8Array
  publicKey: Uint8Array
}

export function generateKeyPair(): KeyPair {
  const privateKey = x25519.utils.randomSecretKey()
  return { privateKey, publicKey: x25519.getPublicKey(privateKey) }
}

/** X25519 Diffie-Hellman — the `dh()` helper on the Go side. */
export function diffieHellman(privateKey: Uint8Array, publicKey: Uint8Array): Uint8Array {
  return x25519.getSharedSecret(privateKey, publicKey)
}

export interface SigningKeyPair {
  /** 32-byte Ed25519 seed. Go stores a 64-byte private key (seed+public); the
   * seed is the part that actually needs keeping. */
  privateKey: Uint8Array
  publicKey: Uint8Array
}

export function generateSigningKeyPair(): SigningKeyPair {
  const privateKey = ed25519.utils.randomSecretKey()
  return { privateKey, publicKey: ed25519.getPublicKey(privateKey) }
}

/** Signs a signed-prekey's public bytes, as `e2e.SignPreKey` does. */
export function signPreKey(privateKey: Uint8Array, signedPreKeyPublic: Uint8Array): Uint8Array {
  return ed25519.sign(signedPreKeyPublic, privateKey)
}

/**
 * Verifies a bundle's prekey signature.
 *
 * This is the MITM defence: without it a hostile key directory could hand out a
 * prekey it controls. A bundle that fails here must be rejected, not "tried
 * anyway".
 */
export function verifyPreKey(
  signingPublicKey: Uint8Array,
  signedPreKeyPublic: Uint8Array,
  signature: Uint8Array,
): boolean {
  if (signingPublicKey.length !== 32 || signature.length !== 64) return false
  try {
    return ed25519.verify(signature, signedPreKeyPublic, signingPublicKey)
  } catch {
    return false
  }
}
