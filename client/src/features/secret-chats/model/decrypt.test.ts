import { describe, expect, it } from 'vitest'

import type { SecretIdentity } from '@/entities/secret-chat'
import { fromBase64, toBase64, toUtf8 } from '@/shared/lib/e2e/codec'
import {
  generateKeyPair,
  generateSigningKeyPair,
  signPreKey,
  type KeyPair,
} from '@/shared/lib/e2e/keys'
import { RatchetSession, marshalHeader } from '@/shared/lib/e2e/ratchet'
import { x3dhInitiator, type PreKeyBundle } from '@/shared/lib/e2e/x3dh'
import { openSecretMessage, type InitEnvelope } from './decrypt'

const ONE_TIME_COUNT = 4

/** Our own device identity, as `useSecretStore.load()` would mint it. */
function identity(oneTimeCount = ONE_TIME_COUNT): SecretIdentity {
  return {
    identity: generateKeyPair(),
    signing: generateSigningKeyPair(),
    signedPreKey: generateKeyPair(),
    signedPreKeyCreatedAt: Date.now(),
    oneTimePreKeys: Array.from({ length: oneTimeCount }, () => generateKeyPair()),
    publishedPreKeys: [],
  }
}

function bundleFor(me: SecretIdentity, oneTime?: KeyPair): PreKeyBundle {
  return {
    identityKey: me.identity.publicKey,
    signingKey: me.signing.publicKey,
    signedPreKey: me.signedPreKey.publicKey,
    signedPreKeySig: signPreKey(me.signing.privateKey, me.signedPreKey.publicKey),
    oneTimePreKey: oneTime ? oneTime.publicKey : new Uint8Array(0),
  }
}

/**
 * Plays the peer: runs X3DH against our published bundle and produces the wire
 * pair (`ratchetHeader`, `ciphertext`) that a SecretMsg frame carries.
 *
 * Driving the sender through the real initiator path is the point — the
 * responder half is the half that cannot be proven by sending, so a hand-built
 * fixture would only test this file against itself.
 */
function peer(me: SecretIdentity, oneTime?: KeyPair) {
  const keys = { identity: generateKeyPair(), ephemeral: generateKeyPair() }
  const { sharedSecret, ephemeralPublicKey } = x3dhInitiator(keys, bundleFor(me, oneTime))
  const session = RatchetSession.initiator(sharedSecret, me.signedPreKey.publicKey)

  return {
    send(text: string) {
      const { header, ciphertext } = session.encrypt(toUtf8(text))
      const init: InitEnvelope = {
        ik: toBase64(keys.identity.publicKey),
        ek: toBase64(ephemeralPublicKey),
        rh: toBase64(marshalHeader(header)),
      }
      return { ratchetHeader: JSON.stringify(init), ciphertext: toBase64(ciphertext) }
    },
    /** A follow-up message, sent without repeating the X3DH bootstrap. */
    sendOnEstablished(text: string) {
      const { header, ciphertext } = session.encrypt(toUtf8(text))
      const init: InitEnvelope = { rh: toBase64(marshalHeader(header)) }
      return { ratchetHeader: JSON.stringify(init), ciphertext: toBase64(ciphertext) }
    },
  }
}

describe('first message', () => {
  it('opens a message sent without a one-time prekey', () => {
    const me = identity()
    const result = openSecretMessage(me, undefined, peer(me).send('hello'))

    expect(result?.plaintext).toBe('hello')
  })

  it('reports no consumed prekey when the sender used none', () => {
    const me = identity()
    const result = openSecretMessage(me, undefined, peer(me).send('hello'))
    expect(result?.consumedOneTimePreKey).toBeUndefined()
  })

  /**
   * The directory hands a fetcher one of our prekeys and never tells us which,
   * so every unconsumed candidate is tried and the AEAD is the oracle. This is
   * the case that makes the search necessary — and the one that would silently
   * fail if the loop stopped at the first candidate.
   */
  it('finds the right one-time prekey wherever it sits in the pool', () => {
    for (const index of [0, 1, ONE_TIME_COUNT - 1]) {
      const me = identity()
      const chosen = me.oneTimePreKeys[index]
      const result = openSecretMessage(me, undefined, peer(me, chosen).send('hi'))

      expect({ index, text: result?.plaintext }).toEqual({ index, text: 'hi' })
    }
  })

  it('reports which prekey was consumed so the caller can forget it', () => {
    // Reusing a one-time prekey defeats exactly the forward secrecy it exists
    // to provide, and only the AEAD knows which one matched.
    const me = identity()
    const chosen = me.oneTimePreKeys[2]
    const result = openSecretMessage(me, undefined, peer(me, chosen).send('hi'))

    expect(result?.consumedOneTimePreKey).toBe(toBase64(chosen.publicKey))
  })

  it('still opens a no-prekey message when the pool is exhausted', () => {
    const me = identity(0)
    expect(openSecretMessage(me, undefined, peer(me).send('hi'))?.plaintext).toBe('hi')
  })

  it('returns a session the caller can persist', () => {
    const me = identity()
    const result = openSecretMessage(me, undefined, peer(me).send('hello'))

    expect(typeof result?.session.rootKey).toBe('string')
    expect(result?.session.skipped).toBeDefined()
  })

  it('carries multi-byte text through intact', () => {
    const me = identity()
    const text = 'секрет 🔐 你好'
    expect(openSecretMessage(me, undefined, peer(me).send(text))?.plaintext).toBe(text)
  })

  it('opens an empty message', () => {
    const me = identity()
    expect(openSecretMessage(me, undefined, peer(me).send(''))?.plaintext).toBe('')
  })
})

describe('established session', () => {
  it('advances an existing session', () => {
    const me = identity()
    const sender = peer(me)

    const first = openSecretMessage(me, undefined, sender.send('one'))
    const second = openSecretMessage(me, first!.session, sender.sendOnEstablished('two'))

    expect(second?.plaintext).toBe('two')
  })

  it('walks a run of messages, threading the session forward', () => {
    const me = identity()
    const sender = peer(me)

    let session = openSecretMessage(me, undefined, sender.send('m0'))!.session
    for (let i = 1; i < 5; i++) {
      const result = openSecretMessage(me, session, sender.sendOnEstablished(`m${i}`))
      expect(result?.plaintext).toBe(`m${i}`)
      session = result!.session
    }
  })

  it('reports no consumed prekey on a follow-up message', () => {
    // The X3DH bootstrap happens once; re-reporting a prekey would make the
    // caller drop a second, still-unused key.
    const me = identity()
    const sender = peer(me)
    const first = openSecretMessage(me, undefined, sender.send('one'))
    const second = openSecretMessage(me, first!.session, sender.sendOnEstablished('two'))

    expect(second?.consumedOneTimePreKey).toBeUndefined()
  })

  /**
   * A peer that reinstalled, or whose session we lost, starts over with a fresh
   * X3DH. Failing outright would strand the chat; falling through to the
   * bootstrap path recovers it.
   */
  it('falls back to a fresh handshake when the stored session no longer fits', () => {
    const me = identity()
    const stale = openSecretMessage(me, undefined, peer(me).send('old'))!.session

    const restarted = peer(me)
    expect(openSecretMessage(me, stale, restarted.send('new'))?.plaintext).toBe('new')
  })

  it('returns null when a stale session meets a message with no bootstrap', () => {
    // Nothing can be recovered here: the session does not fit and the message
    // carries no keys to build a new one.
    const me = identity()
    const stale = openSecretMessage(me, undefined, peer(me).send('old'))!.session
    const orphan = peer(me).sendOnEstablished('unopenable')

    expect(openSecretMessage(me, stale, orphan)).toBeNull()
  })
})

describe('malformed input', () => {
  it('returns null for a header that is not JSON', () => {
    // The header is attacker-influenced: it arrives from a relay as an opaque
    // string. A throw here would take down the message handler for the whole
    // connection, not just this message.
    const me = identity()
    expect(
      openSecretMessage(me, undefined, { ratchetHeader: 'not json', ciphertext: '' }),
    ).toBeNull()
  })

  it('returns null for a header with no ratchet field', () => {
    const me = identity()
    expect(
      openSecretMessage(me, undefined, {
        ratchetHeader: JSON.stringify({ ik: 'x' }),
        ciphertext: '',
      }),
    ).toBeNull()
  })

  it('returns null for a JSON header that is not an object', () => {
    const me = identity()
    expect(
      openSecretMessage(me, undefined, { ratchetHeader: '"a string"', ciphertext: '' }),
    ).toBeNull()
  })

  it('returns null for a first message missing the initiator identity', () => {
    const me = identity()
    const wire = peer(me).send('hi')
    const init = JSON.parse(wire.ratchetHeader) as InitEnvelope
    delete init.ik

    expect(
      openSecretMessage(me, undefined, { ...wire, ratchetHeader: JSON.stringify(init) }),
    ).toBeNull()
  })

  it('returns null for a first message missing the ephemeral key', () => {
    const me = identity()
    const wire = peer(me).send('hi')
    const init = JSON.parse(wire.ratchetHeader) as InitEnvelope
    delete init.ek

    expect(
      openSecretMessage(me, undefined, { ...wire, ratchetHeader: JSON.stringify(init) }),
    ).toBeNull()
  })

  it('returns null for tampered ciphertext', () => {
    const me = identity()
    const wire = peer(me).send('hi')
    const bytes = fromBase64(wire.ciphertext)
    bytes[0] ^= 0xff

    expect(
      openSecretMessage(me, undefined, { ...wire, ciphertext: toBase64(bytes) }),
    ).toBeNull()
  })

  it('returns null for a message addressed to someone else', () => {
    // Every candidate fails, including the no-prekey one — the search must
    // terminate rather than loop or throw.
    const me = identity()
    const someoneElse = identity()

    expect(openSecretMessage(someoneElse, undefined, peer(me).send('hi'))).toBeNull()
  })

  it('returns null when the ciphertext is empty', () => {
    const me = identity()
    const wire = peer(me).send('hi')

    expect(openSecretMessage(me, undefined, { ...wire, ciphertext: '' })).toBeNull()
  })

  it('never throws on any of the malformed shapes', () => {
    const me = identity()
    for (const header of ['', '{}', 'null', '[]', '{"rh":""}', '{"rh":"!!!not base64"}']) {
      expect(() =>
        openSecretMessage(me, undefined, { ratchetHeader: header, ciphertext: 'AAAA' }),
      ).not.toThrow()
    }
  })
})

describe('signed prekey rotation', () => {
  /**
   * The window this exists for: a peer fetched our bundle, we rotated, and their
   * first message is already in flight against the prekey we just replaced. The
   * relay is fire-and-forget, so without the grace copy that message is lost and
   * the sender is never told.
   */
  it('opens a message sent against the previous signed prekey', () => {
    const me = identity()
    const sender = peer(me) // runs X3DH against the CURRENT prekey

    // Now rotate: the key the sender used becomes the grace copy.
    const rotated: SecretIdentity = {
      ...me,
      previousSignedPreKey: me.signedPreKey,
      signedPreKey: generateKeyPair(),
      signedPreKeyCreatedAt: Date.now(),
    }

    expect(openSecretMessage(rotated, undefined, sender.send('in flight'))?.plaintext).toBe(
      'in flight',
    )
  })

  it('still opens a message sent against the current prekey after a rotation', () => {
    const me = identity()
    const rotated: SecretIdentity = {
      ...me,
      previousSignedPreKey: generateKeyPair(),
      signedPreKeyCreatedAt: Date.now(),
    }
    expect(openSecretMessage(rotated, undefined, peer(rotated).send('fresh'))?.plaintext).toBe(
      'fresh',
    )
  })

  /**
   * Only ONE generation is kept. A key held forever is a key that never rotated,
   * which is the whole thing rotation is for.
   */
  it('refuses a message sent against a prekey two generations old', () => {
    const me = identity()
    const sender = peer(me)

    const once: SecretIdentity = {
      ...me,
      previousSignedPreKey: me.signedPreKey,
      signedPreKey: generateKeyPair(),
      signedPreKeyCreatedAt: Date.now(),
    }
    const twice: SecretIdentity = {
      ...once,
      previousSignedPreKey: once.signedPreKey,
      signedPreKey: generateKeyPair(),
      signedPreKeyCreatedAt: Date.now(),
    }

    expect(openSecretMessage(twice, undefined, sender.send('too late'))).toBeNull()
  })
})
