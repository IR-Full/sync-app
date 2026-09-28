import { describe, expect, it } from 'vitest'

import {
  diffieHellman,
  generateKeyPair,
  generateSigningKeyPair,
  signPreKey,
  verifyPreKey,
} from './keys'

describe('generateKeyPair', () => {
  it('produces 32-byte X25519 keys', () => {
    const pair = generateKeyPair()
    expect(pair.privateKey.length).toBe(32)
    expect(pair.publicKey.length).toBe(32)
  })

  it('produces a distinct pair each call', () => {
    // A repeated identity key would silently make two "different" devices share
    // a session — the failure mode is silent, so the check has to be explicit.
    const keys = Array.from({ length: 16 }, () => generateKeyPair())
    const seen = new Set(keys.map((pair) => pair.privateKey.join(',')))
    expect(seen.size).toBe(keys.length)
  })

  it('never returns the private key as the public key', () => {
    const pair = generateKeyPair()
    expect(Array.from(pair.publicKey)).not.toEqual(Array.from(pair.privateKey))
  })
})

describe('diffieHellman', () => {
  /**
   * The whole of X3DH and the ratchet rest on this one property. If it held only
   * in one direction, the initiator and responder would derive different root
   * keys and every message would fail to decrypt.
   */
  it('agrees in both directions', () => {
    const alice = generateKeyPair()
    const bob = generateKeyPair()
    expect(Array.from(diffieHellman(alice.privateKey, bob.publicKey))).toEqual(
      Array.from(diffieHellman(bob.privateKey, alice.publicKey)),
    )
  })

  it('produces a 32-byte secret', () => {
    const a = generateKeyPair()
    const b = generateKeyPair()
    expect(diffieHellman(a.privateKey, b.publicKey).length).toBe(32)
  })

  it('gives a different secret for a different peer', () => {
    const alice = generateKeyPair()
    const bob = generateKeyPair()
    const mallory = generateKeyPair()
    expect(Array.from(diffieHellman(alice.privateKey, bob.publicKey))).not.toEqual(
      Array.from(diffieHellman(alice.privateKey, mallory.publicKey)),
    )
  })

  it('is deterministic for the same pair of keys', () => {
    // A session resumed from storage re-derives the same secrets; randomness
    // here would break every reload.
    const alice = generateKeyPair()
    const bob = generateKeyPair()
    expect(Array.from(diffieHellman(alice.privateKey, bob.publicKey))).toEqual(
      Array.from(diffieHellman(alice.privateKey, bob.publicKey)),
    )
  })
})

describe('generateSigningKeyPair', () => {
  it('produces a 32-byte seed and a 32-byte public key', () => {
    // Go keeps a 64-byte private key (seed+public); only the seed is stored
    // here, and a 64-byte value would be rejected by @noble's signer.
    const pair = generateSigningKeyPair()
    expect(pair.privateKey.length).toBe(32)
    expect(pair.publicKey.length).toBe(32)
  })

  it('produces a distinct pair each call', () => {
    const a = generateSigningKeyPair()
    const b = generateSigningKeyPair()
    expect(Array.from(a.privateKey)).not.toEqual(Array.from(b.privateKey))
  })
})

describe('signPreKey / verifyPreKey', () => {
  it('produces a 64-byte Ed25519 signature', () => {
    const signing = generateSigningKeyPair()
    const preKey = generateKeyPair()
    expect(signPreKey(signing.privateKey, preKey.publicKey).length).toBe(64)
  })

  it('verifies a signature it just made', () => {
    const signing = generateSigningKeyPair()
    const preKey = generateKeyPair()
    const signature = signPreKey(signing.privateKey, preKey.publicKey)
    expect(verifyPreKey(signing.publicKey, preKey.publicKey, signature)).toBe(true)
  })

  /**
   * This is the MITM defence. A hostile key directory can serve any bytes it
   * likes for the prekey; the only thing stopping it is that the signature no
   * longer matches the identity the user already trusts. Every rejection case
   * below is a substitution attack that must not be "tried anyway".
   */
  it('rejects a prekey swapped for the attacker`s own', () => {
    const signing = generateSigningKeyPair()
    const honest = generateKeyPair()
    const attacker = generateKeyPair()
    const signature = signPreKey(signing.privateKey, honest.publicKey)
    expect(verifyPreKey(signing.publicKey, attacker.publicKey, signature)).toBe(false)
  })

  it('rejects a signature made by a different identity', () => {
    const signing = generateSigningKeyPair()
    const attacker = generateSigningKeyPair()
    const preKey = generateKeyPair()
    const signature = signPreKey(attacker.privateKey, preKey.publicKey)
    expect(verifyPreKey(signing.publicKey, preKey.publicKey, signature)).toBe(false)
  })

  it('rejects a signature with a single flipped bit', () => {
    const signing = generateSigningKeyPair()
    const preKey = generateKeyPair()
    const signature = signPreKey(signing.privateKey, preKey.publicKey)
    signature[0] ^= 0x01
    expect(verifyPreKey(signing.publicKey, preKey.publicKey, signature)).toBe(false)
  })

  it('returns false rather than throwing on a wrong-length signing key', () => {
    // Bundle fields come off the wire base64-decoded; a truncated or absent key
    // must fail closed, not crash the secret-chat panel.
    const preKey = generateKeyPair()
    const signature = new Uint8Array(64)
    expect(verifyPreKey(new Uint8Array(0), preKey.publicKey, signature)).toBe(false)
    expect(verifyPreKey(new Uint8Array(31), preKey.publicKey, signature)).toBe(false)
    expect(verifyPreKey(new Uint8Array(64), preKey.publicKey, signature)).toBe(false)
  })

  it('returns false rather than throwing on a wrong-length signature', () => {
    const signing = generateSigningKeyPair()
    const preKey = generateKeyPair()
    expect(verifyPreKey(signing.publicKey, preKey.publicKey, new Uint8Array(0))).toBe(false)
    expect(verifyPreKey(signing.publicKey, preKey.publicKey, new Uint8Array(63))).toBe(false)
    expect(verifyPreKey(signing.publicKey, preKey.publicKey, new Uint8Array(65))).toBe(false)
  })

  it('returns false rather than throwing on 64 bytes of garbage', () => {
    // Right length, wrong structure: @noble throws on a malformed point, and an
    // uncaught throw here would take down the render rather than reject a peer.
    const signing = generateSigningKeyPair()
    const preKey = generateKeyPair()
    const garbage = new Uint8Array(64).fill(0xff)
    expect(verifyPreKey(signing.publicKey, preKey.publicKey, garbage)).toBe(false)
  })

  it('is deterministic — Ed25519 signatures are not randomised', () => {
    const signing = generateSigningKeyPair()
    const preKey = generateKeyPair()
    expect(Array.from(signPreKey(signing.privateKey, preKey.publicKey))).toEqual(
      Array.from(signPreKey(signing.privateKey, preKey.publicKey)),
    )
  })
})
