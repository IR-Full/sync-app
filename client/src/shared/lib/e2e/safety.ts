import { sha512 } from '@noble/hashes/sha2.js'

import { concatBytes, toUtf8 } from './codec'

/**
 * Safety numbers — the out-of-band check that nobody swapped an identity key.
 *
 * X3DH authenticates the signed prekey against the identity signing key, so a
 * hostile directory cannot substitute a prekey. Nothing in the protocol stops it
 * substituting the *identity* key itself and serving a different one to each
 * side, which is a textbook MITM: both halves verify perfectly, against the
 * attacker. Comparing a safety number out of band — read aloud, or photographed
 * — is what closes that, because the number is derived from the keys each side
 * actually holds.
 *
 * A direct port of `server/pkg/e2e/safety.go`. Every constant here is part of
 * the contract: a different iteration count or byte length produces a number
 * that will not match what the Go client shows for the same pair of identities,
 * and the failure mode is two people staring at different digits and concluding
 * they are being attacked.
 */

/**
 * Hash-iteration count, matching Signal. Deliberately slow: it is what makes
 * searching for a key pair that yields a chosen fingerprint expensive.
 */
const SAFETY_ITERATIONS = 5200

/** Bytes of the final digest that form one party's fingerprint (30 → six groups). */
const FINGERPRINT_BYTES = 30

/** Guards against cross-version fingerprint collisions. */
const SAFETY_VERSION = 0

/**
 * Both long-term keys go in.
 *
 * Binding the X25519 agreement key AND the Ed25519 signing key means a MITM has
 * to forge the whole identity rather than the half that is checked.
 */
function identityMaterial(identityKey: Uint8Array, signingKey: Uint8Array): Uint8Array {
  return concatBytes(identityKey, signingKey)
}

/** Reduces one party's identity to a 30-byte digest. */
function fingerprint(
  identityKey: Uint8Array,
  signingKey: Uint8Array,
  stableId: string,
): Uint8Array {
  const key = identityMaterial(identityKey, signingKey)
  const version = new Uint8Array([(SAFETY_VERSION >> 8) & 0xff, SAFETY_VERSION & 0xff])

  let digest = sha512(concatBytes(version, key, toUtf8(stableId)))
  // Each round folds the key back in, so the work cannot be precomputed without
  // knowing the key.
  for (let i = 0; i < SAFETY_ITERATIONS; i++) {
    digest = sha512(concatBytes(digest, key))
  }
  return digest.slice(0, FINGERPRINT_BYTES)
}

/** Renders a fingerprint as six groups of five decimal digits. */
function displayFingerprint(fp: Uint8Array): string {
  const groups: string[] = []
  for (let i = 0; i + 5 <= fp.length; i += 5) {
    // Five bytes as a big-endian 40-bit integer, built with multiplication
    // rather than shifts. JavaScript's bit operators coerce to 32 bits, so
    // `value << 8` would silently truncate and yield a plausible-looking number
    // that does not match the Go side. 2^40 is far below Number's exact-integer
    // ceiling of 2^53, so plain arithmetic is exact here.
    let value = 0
    for (let j = 0; j < 5; j++) value = value * 256 + fp[i + j]
    groups.push((value % 100000).toString().padStart(5, '0'))
  }
  return groups.join(' ')
}

export interface SafetyIdentity {
  /** any stable per-user handle both sides agree on — the user id */
  stableId: string
  /** X25519 identity public key */
  identityKey: Uint8Array
  /** Ed25519 identity signing public key */
  signingKey: Uint8Array
}

/**
 * The symmetric 60-digit safety number for a conversation between two
 * identities.
 *
 * Both parties compute the identical string regardless of argument order — they
 * disagree about which side is "local", so the two fingerprints are combined in
 * a canonical order rather than the caller's. A changed identity key on either
 * side changes the number, which is the whole signal.
 */
export function safetyNumber(local: SafetyIdentity, remote: SafetyIdentity): string {
  const a = displayFingerprint(fingerprint(local.identityKey, local.signingKey, local.stableId))
  const b = displayFingerprint(
    fingerprint(remote.identityKey, remote.signingKey, remote.stableId),
  )
  return a <= b ? `${a} ${b}` : `${b} ${a}`
}
