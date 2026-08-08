import { hkdf } from '@noble/hashes/hkdf.js'
import { sha256 } from '@noble/hashes/sha2.js'

import { concatBytes, toUtf8 } from './codec'
import { diffieHellman, verifyPreKey, type KeyPair } from './keys'

/**
 * X3DH — the asynchronous key agreement that bootstraps a secret chat.
 *
 * A direct port of `server/pkg/e2e/x3dh.go`. Every constant here is part of the
 * wire contract: change the DH order, the 0xFF prefix or the HKDF info string
 * and the two sides silently derive different keys, which surfaces only as
 * "decryption failed" much later.
 */

export interface PreKeyBundle {
  identityKey: Uint8Array
  signingKey: Uint8Array
  signedPreKey: Uint8Array
  signedPreKeySig: Uint8Array
  /** optional; consumed once by whoever fetched it */
  oneTimePreKey: Uint8Array
}

export class BadPreKeySignatureError extends Error {
  constructor() {
    super('e2e: bad signed-prekey signature')
    this.name = 'BadPreKeySignatureError'
  }
}

/** Turns the concatenated DH outputs into the 32-byte root key. */
function rootFromDH(dhConcat: Uint8Array): Uint8Array {
  // The 32-byte 0xFF prefix is the X3DH spec's domain separator.
  const prefix = new Uint8Array(32).fill(0xff)
  return hkdf(sha256, concatBytes(prefix, dhConcat), undefined, toUtf8('Synapse-X3DH'), 32)
}

/**
 * Initiator (Alice) side: derives the shared secret from the peer's published
 * bundle, returning it together with the ephemeral public key the responder
 * needs to derive the same secret.
 */
export function x3dhInitiator(
  keys: { identity: KeyPair; ephemeral: KeyPair },
  bundle: PreKeyBundle,
): { sharedSecret: Uint8Array; ephemeralPublicKey: Uint8Array } {
  // Verify before trusting anything else in the bundle.
  if (bundle.signingKey.length > 0 || bundle.signedPreKeySig.length > 0) {
    if (!verifyPreKey(bundle.signingKey, bundle.signedPreKey, bundle.signedPreKeySig)) {
      throw new BadPreKeySignatureError()
    }
  }

  // DH1 = DH(IK_A, SPK_B); DH2 = DH(EK_A, IK_B); DH3 = DH(EK_A, SPK_B)
  const dh1 = diffieHellman(keys.identity.privateKey, bundle.signedPreKey)
  const dh2 = diffieHellman(keys.ephemeral.privateKey, bundle.identityKey)
  const dh3 = diffieHellman(keys.ephemeral.privateKey, bundle.signedPreKey)
  let concat = concatBytes(dh1, dh2, dh3)

  // DH4 = DH(EK_A, OPK_B), only when the bundle carried a one-time prekey.
  if (bundle.oneTimePreKey.length > 0) {
    concat = concatBytes(concat, diffieHellman(keys.ephemeral.privateKey, bundle.oneTimePreKey))
  }

  return {
    sharedSecret: rootFromDH(concat),
    ephemeralPublicKey: keys.ephemeral.publicKey,
  }
}

/** Responder (Bob) side: mirrors the initiator's DH order exactly. */
export function x3dhResponder(
  keys: { identity: KeyPair; signedPreKey: KeyPair; oneTimePreKey?: KeyPair },
  initiatorIdentityPublic: Uint8Array,
  initiatorEphemeralPublic: Uint8Array,
  usedOneTime: boolean,
): Uint8Array {
  // DH1 = DH(SPK_B, IK_A); DH2 = DH(IK_B, EK_A); DH3 = DH(SPK_B, EK_A)
  const dh1 = diffieHellman(keys.signedPreKey.privateKey, initiatorIdentityPublic)
  const dh2 = diffieHellman(keys.identity.privateKey, initiatorEphemeralPublic)
  const dh3 = diffieHellman(keys.signedPreKey.privateKey, initiatorEphemeralPublic)
  let concat = concatBytes(dh1, dh2, dh3)

  if (usedOneTime) {
    if (!keys.oneTimePreKey) throw new Error('e2e: responder missing one-time prekey')
    concat = concatBytes(
      concat,
      diffieHellman(keys.oneTimePreKey.privateKey, initiatorEphemeralPublic),
    )
  }

  return rootFromDH(concat)
}
