import { existsSync, readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { ed25519, x25519 } from '@noble/curves/ed25519.js'
import { describe, expect, it } from 'vitest'

import { fromBase64, fromUtf8, toBase64, toUtf8 } from './codec'
import { signPreKey, type KeyPair } from './keys'
import { marshalHeader, RatchetSession, unmarshalHeader } from './ratchet'
import { safetyNumber } from './safety'
import { x3dhInitiator, x3dhResponder } from './x3dh'

/**
 * Replays `server/testdata/e2e/vectors.json`, produced by the Go implementation,
 * and requires this port to reproduce it byte for byte. With the ratchet keys
 * fixed, encryption is deterministic: matching Go's ciphertext means Go can
 * decrypt ours, and decrypting Go's means we can read Go's. Android and iOS
 * replay the same file.
 */

interface VecKeyPair {
  private: string
  public: string
}

interface VecStep {
  op: 'send' | 'recv' | 'forged'
  from?: 'alice' | 'bob'
  to?: 'alice' | 'bob'
  id: string
  plaintext?: string
  header?: string
  ciphertext?: string
}

interface Vectors {
  x3dh: {
    alice_identity: VecKeyPair
    alice_ephemeral: VecKeyPair
    bob_identity: VecKeyPair
    bob_signing: { seed: string; public: string }
    bob_signed_prekey: VecKeyPair
    bob_one_time_prekey: VecKeyPair
    signed_prekey_signature: string
    shared_secret: string
    shared_secret_no_one_time: string
  }
  conversation: {
    alice_ratchet_keys: VecKeyPair[]
    bob_ratchet_keys: VecKeyPair[]
    steps: VecStep[]
  }
  safety: {
    local_id: string
    local_identity_key: string
    local_signing_key: string
    remote_id: string
    remote_identity_key: string
    remote_signing_key: string
    number: string
  }[]
}

/** Walks up from this file so the test works from the repo root and from `client/`. */
function loadVectors(): Vectors {
  let dir = dirname(new URL(import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1'))
  for (let i = 0; i < 12; i++) {
    const candidate = join(dir, 'server', 'testdata', 'e2e', 'vectors.json')
    if (existsSync(candidate)) return JSON.parse(readFileSync(candidate, 'utf8')) as Vectors
    const parent = dirname(dir)
    if (parent === dir) break
    dir = parent
  }
  // Failing rather than skipping: a vector test that quietly does nothing is the
  // exact drift it exists to catch.
  throw new Error('server/testdata/e2e/vectors.json not found above ' + import.meta.url)
}

const v = loadVectors()

function keyPair(kp: VecKeyPair): KeyPair {
  return { privateKey: fromBase64(kp.private), publicKey: fromBase64(kp.public) }
}

/** Hands out recorded key pairs in order and counts how many were drawn. */
function recorded(kps: VecKeyPair[]) {
  const source = {
    drawn: 0,
    next: (): KeyPair => {
      if (source.drawn >= kps.length) throw new Error('vector key source exhausted')
      return keyPair(kps[source.drawn++])
    },
  }
  return source
}

describe('cross-language vectors (server/testdata/e2e)', () => {
  const x = v.x3dh

  it('derives every public key from its private key', () => {
    const pairs = [
      x.alice_identity,
      x.alice_ephemeral,
      x.bob_identity,
      x.bob_signed_prekey,
      x.bob_one_time_prekey,
      ...v.conversation.alice_ratchet_keys,
      ...v.conversation.bob_ratchet_keys,
    ]
    for (const kp of pairs) {
      expect(toBase64(x25519.getPublicKey(fromBase64(kp.private)))).toBe(kp.public)
    }
    expect(toBase64(ed25519.getPublicKey(fromBase64(x.bob_signing.seed)))).toBe(
      x.bob_signing.public,
    )
  })

  it('signs the prekey exactly as Go does', () => {
    const sig = signPreKey(
      fromBase64(x.bob_signing.seed),
      fromBase64(x.bob_signed_prekey.public),
    )
    expect(toBase64(sig)).toBe(x.signed_prekey_signature)
  })

  it('agrees on the X3DH shared secret from both sides', () => {
    const bundle = {
      identityKey: fromBase64(x.bob_identity.public),
      signingKey: fromBase64(x.bob_signing.public),
      signedPreKey: fromBase64(x.bob_signed_prekey.public),
      signedPreKeySig: fromBase64(x.signed_prekey_signature),
      oneTimePreKey: fromBase64(x.bob_one_time_prekey.public),
    }
    const alice = { identity: keyPair(x.alice_identity), ephemeral: keyPair(x.alice_ephemeral) }
    const init = x3dhInitiator(alice, bundle)
    expect(toBase64(init.sharedSecret)).toBe(x.shared_secret)

    const bob = {
      identity: keyPair(x.bob_identity),
      signedPreKey: keyPair(x.bob_signed_prekey),
      oneTimePreKey: keyPair(x.bob_one_time_prekey),
    }
    const aliceIK = fromBase64(x.alice_identity.public)
    expect(toBase64(x3dhResponder(bob, aliceIK, init.ephemeralPublicKey, true))).toBe(
      x.shared_secret,
    )
    expect(toBase64(x3dhResponder(bob, aliceIK, init.ephemeralPublicKey, false))).toBe(
      x.shared_secret_no_one_time,
    )
  })

  it('reproduces the whole conversation byte for byte', () => {
    const sk = fromBase64(x.shared_secret)
    const aliceKeys = recorded(v.conversation.alice_ratchet_keys)
    const bobKeys = recorded(v.conversation.bob_ratchet_keys)
    const sessions = {
      alice: RatchetSession.initiator(
        sk,
        fromBase64(x.bob_signed_prekey.public),
        aliceKeys.next,
      ),
      bob: RatchetSession.responder(sk, keyPair(x.bob_signed_prekey), bobKeys.next),
    }
    const sent = new Map<string, VecStep>()

    for (const [i, step] of v.conversation.steps.entries()) {
      const where = `step ${i} (${step.op} ${step.id})`
      if (step.op === 'send') {
        const { header, ciphertext } = sessions[step.from!].encrypt(
          toUtf8(step.plaintext ?? ''),
        )
        expect(fromUtf8(marshalHeader(header)), where).toBe(step.header)
        expect(toBase64(ciphertext), where).toBe(step.ciphertext)
        sent.set(step.id, step)
        continue
      }
      const msg = sent.get(step.id)!
      const header = unmarshalHeader(toUtf8(msg.header!))
      const ciphertext = fromBase64(msg.ciphertext!)
      if (step.op === 'forged') {
        ciphertext[ciphertext.length - 1] ^= 0x01
        expect(() => sessions[step.to!].decrypt(header, ciphertext), where).toThrow()
      } else {
        expect(fromUtf8(sessions[step.to!].decrypt(header, ciphertext)), where).toBe(
          msg.plaintext ?? '',
        )
      }
    }
    expect(aliceKeys.drawn).toBe(v.conversation.alice_ratchet_keys.length)
    expect(bobKeys.drawn).toBe(v.conversation.bob_ratchet_keys.length)
  })

  it('computes the same safety numbers', () => {
    for (const s of v.safety) {
      const number = safetyNumber(
        {
          stableId: s.local_id,
          identityKey: fromBase64(s.local_identity_key),
          signingKey: fromBase64(s.local_signing_key),
        },
        {
          stableId: s.remote_id,
          identityKey: fromBase64(s.remote_identity_key),
          signingKey: fromBase64(s.remote_signing_key),
        },
      )
      expect(number).toBe(s.number)
    }
  })
})
