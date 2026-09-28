import { describe, expect, it } from 'vitest'

import {
  diffieHellman,
  generateKeyPair,
  generateSigningKeyPair,
  signPreKey,
  type KeyPair,
} from './keys'
import {
  BadPreKeySignatureError,
  x3dhInitiator,
  x3dhResponder,
  type PreKeyBundle,
} from './x3dh'

/** Bob's published side: the long-lived keys plus a freshly signed prekey. */
function responderKeys(withOneTime: boolean) {
  const identity = generateKeyPair()
  const signing = generateSigningKeyPair()
  const signedPreKey = generateKeyPair()
  const oneTimePreKey = withOneTime ? generateKeyPair() : undefined

  const bundle: PreKeyBundle = {
    identityKey: identity.publicKey,
    signingKey: signing.publicKey,
    signedPreKey: signedPreKey.publicKey,
    signedPreKeySig: signPreKey(signing.privateKey, signedPreKey.publicKey),
    oneTimePreKey: oneTimePreKey ? oneTimePreKey.publicKey : new Uint8Array(0),
  }
  return { identity, signedPreKey, oneTimePreKey, bundle }
}

/** Alice's side: her identity plus the ephemeral she burns on this handshake. */
function initiatorKeys(): { identity: KeyPair; ephemeral: KeyPair } {
  return { identity: generateKeyPair(), ephemeral: generateKeyPair() }
}

const bytes = (value: Uint8Array) => Array.from(value)

describe('x3dh agreement', () => {
  /**
   * The one property the whole secret-chat feature rests on. Both sides run a
   * *different* sequence of DH operations and must land on the same 32 bytes; a
   * reordered DH or a changed HKDF info string still produces a plausible-looking
   * key on each side, and the only symptom is "decryption failed" much later,
   * after the session has already been persisted.
   */
  it('derives the same secret on both sides without a one-time prekey', () => {
    const bob = responderKeys(false)
    const alice = initiatorKeys()

    const { sharedSecret, ephemeralPublicKey } = x3dhInitiator(alice, bob.bundle)
    const theirs = x3dhResponder(
      { identity: bob.identity, signedPreKey: bob.signedPreKey },
      alice.identity.publicKey,
      ephemeralPublicKey,
      false,
    )

    expect(bytes(theirs)).toEqual(bytes(sharedSecret))
  })

  it('derives the same secret with a one-time prekey', () => {
    const bob = responderKeys(true)
    const alice = initiatorKeys()

    const { sharedSecret, ephemeralPublicKey } = x3dhInitiator(alice, bob.bundle)
    const theirs = x3dhResponder(
      {
        identity: bob.identity,
        signedPreKey: bob.signedPreKey,
        oneTimePreKey: bob.oneTimePreKey,
      },
      alice.identity.publicKey,
      ephemeralPublicKey,
      true,
    )

    expect(bytes(theirs)).toEqual(bytes(sharedSecret))
  })

  it('produces a 32-byte key', () => {
    const bob = responderKeys(true)
    expect(x3dhInitiator(initiatorKeys(), bob.bundle).sharedSecret.length).toBe(32)
  })

  it('returns the ephemeral public key the responder needs', () => {
    // The responder cannot derive DH2/DH3 without it, and it is not otherwise
    // published anywhere — it travels in the first ciphertext's envelope.
    const alice = initiatorKeys()
    const result = x3dhInitiator(alice, responderKeys(true).bundle)
    expect(bytes(result.ephemeralPublicKey)).toEqual(bytes(alice.ephemeral.publicKey))
  })

  it('binds the one-time prekey into the secret', () => {
    // A responder that forgot it consumed a one-time prekey would call
    // x3dhResponder with usedOneTime=false and derive a different key. The two
    // outcomes must not accidentally coincide.
    const bob = responderKeys(true)
    const alice = initiatorKeys()
    const { sharedSecret, ephemeralPublicKey } = x3dhInitiator(alice, bob.bundle)

    const withoutOneTime = x3dhResponder(
      { identity: bob.identity, signedPreKey: bob.signedPreKey },
      alice.identity.publicKey,
      ephemeralPublicKey,
      false,
    )

    expect(bytes(withoutOneTime)).not.toEqual(bytes(sharedSecret))
  })

  it('gives a different secret to each new handshake', () => {
    // The ephemeral is what makes two sessions with the same peer independent;
    // if the secret depended only on the long-lived keys, compromising one
    // session would open every other.
    const bob = responderKeys(false)
    const first = x3dhInitiator(initiatorKeys(), bob.bundle).sharedSecret
    const second = x3dhInitiator(initiatorKeys(), bob.bundle).sharedSecret
    expect(bytes(first)).not.toEqual(bytes(second))
  })

  it('gives a different secret against a different peer', () => {
    const alice = initiatorKeys()
    const first = x3dhInitiator(alice, responderKeys(false).bundle).sharedSecret
    const second = x3dhInitiator(alice, responderKeys(false).bundle).sharedSecret
    expect(bytes(first)).not.toEqual(bytes(second))
  })

  it('is not a bare DH output — the HKDF step actually runs', () => {
    // A missing KDF would still "work" between two JS clients while diverging
    // from Go, so compare against the raw DH1 the derivation starts from.
    const bob = responderKeys(false)
    const alice = initiatorKeys()
    const { sharedSecret } = x3dhInitiator(alice, bob.bundle)
    const rawDh1 = diffieHellman(alice.identity.privateKey, bob.bundle.signedPreKey)
    expect(bytes(sharedSecret)).not.toEqual(bytes(rawDh1))
  })
})

describe('x3dhInitiator prekey verification', () => {
  it('rejects a bundle whose prekey was swapped', () => {
    const bob = responderKeys(false)
    const attacker = generateKeyPair()
    const tampered: PreKeyBundle = { ...bob.bundle, signedPreKey: attacker.publicKey }

    expect(() => x3dhInitiator(initiatorKeys(), tampered)).toThrow(BadPreKeySignatureError)
  })

  it('rejects a bundle signed by the wrong identity', () => {
    const bob = responderKeys(false)
    const attacker = generateSigningKeyPair()
    const tampered: PreKeyBundle = {
      ...bob.bundle,
      signedPreKeySig: signPreKey(attacker.privateKey, bob.bundle.signedPreKey),
    }

    expect(() => x3dhInitiator(initiatorKeys(), tampered)).toThrow(BadPreKeySignatureError)
  })

  it('rejects a bundle with a corrupted signature', () => {
    const bob = responderKeys(false)
    const signature = Uint8Array.from(bob.bundle.signedPreKeySig)
    signature[10] ^= 0xff

    expect(() =>
      x3dhInitiator(initiatorKeys(), { ...bob.bundle, signedPreKeySig: signature }),
    ).toThrow(BadPreKeySignatureError)
  })

  it('rejects a bundle that carries a signing key but no signature', () => {
    // Stripping the signature is the cheapest downgrade attack there is; the
    // check must not be skippable by omission.
    const bob = responderKeys(false)
    expect(() =>
      x3dhInitiator(initiatorKeys(), { ...bob.bundle, signedPreKeySig: new Uint8Array(0) }),
    ).toThrow(BadPreKeySignatureError)
  })

  it('rejects a bundle that carries a signature but no signing key', () => {
    const bob = responderKeys(false)
    expect(() =>
      x3dhInitiator(initiatorKeys(), { ...bob.bundle, signingKey: new Uint8Array(0) }),
    ).toThrow(BadPreKeySignatureError)
  })

  it('names the failure so the UI can tell it apart from a transport error', () => {
    const bob = responderKeys(false)
    try {
      x3dhInitiator(initiatorKeys(), { ...bob.bundle, signedPreKeySig: new Uint8Array(64) })
      expect.unreachable('a bad signature must throw')
    } catch (error) {
      expect((error as Error).name).toBe('BadPreKeySignatureError')
    }
  })

  it('rejects an unsigned bundle even when both signing fields are absent', () => {
    // This case used to be allowed, on the theory that a bundle with neither
    // field came from a build predating prekey signing. That reading gave the
    // attacker the switch: a hostile directory never needed to forge a
    // signature, it just sent none. An unsigned bundle is indistinguishable
    // from a substituted one, so it is rejected — matching the Go side.
    const bob = responderKeys(false)
    const unsigned: PreKeyBundle = {
      ...bob.bundle,
      signingKey: new Uint8Array(0),
      signedPreKeySig: new Uint8Array(0),
    }
    expect(() => x3dhInitiator(initiatorKeys(), unsigned)).toThrow(BadPreKeySignatureError)
  })
})

describe('x3dhResponder', () => {
  it('refuses to derive a key when the one-time prekey it needs is gone', () => {
    // A consumed prekey that was not persisted must fail loudly. Deriving a
    // three-DH secret instead would produce a session that never decrypts and
    // would take a support ticket to diagnose.
    const bob = responderKeys(true)
    const alice = initiatorKeys()
    const { ephemeralPublicKey } = x3dhInitiator(alice, bob.bundle)

    expect(() =>
      x3dhResponder(
        { identity: bob.identity, signedPreKey: bob.signedPreKey },
        alice.identity.publicKey,
        ephemeralPublicKey,
        true,
      ),
    ).toThrow(/one-time prekey/)
  })

  it('derives a different key from a forged initiator identity', () => {
    // An attacker replaying Alice's ephemeral with their own identity must not
    // land on Alice's session key.
    const bob = responderKeys(false)
    const alice = initiatorKeys()
    const { sharedSecret, ephemeralPublicKey } = x3dhInitiator(alice, bob.bundle)

    const forged = x3dhResponder(
      { identity: bob.identity, signedPreKey: bob.signedPreKey },
      generateKeyPair().publicKey,
      ephemeralPublicKey,
      false,
    )

    expect(bytes(forged)).not.toEqual(bytes(sharedSecret))
  })

  it('is deterministic, so a session survives a reload', () => {
    const bob = responderKeys(false)
    const alice = initiatorKeys()
    const { ephemeralPublicKey } = x3dhInitiator(alice, bob.bundle)
    const derive = () =>
      x3dhResponder(
        { identity: bob.identity, signedPreKey: bob.signedPreKey },
        alice.identity.publicKey,
        ephemeralPublicKey,
        false,
      )

    expect(bytes(derive())).toEqual(bytes(derive()))
  })
})
