import { describe, expect, it } from 'vitest'

import { fromBase64, fromUtf8, toBase64, toUtf8 } from './codec'
import { generateKeyPair, generateSigningKeyPair, signPreKey } from './keys'
import {
  DecryptError,
  RatchetSession,
  marshalHeader,
  unmarshalHeader,
  type RatchetHeader,
} from './ratchet'
import { x3dhInitiator, x3dhResponder, type PreKeyBundle } from './x3dh'

/**
 * Builds a live Alice/Bob pair the way the app does: a real X3DH handshake feeds
 * a real pair of ratchet sessions. Hand-rolling a shared secret would test the
 * ratchet against itself and miss any disagreement about what the first DHr is.
 */
function handshake(): { alice: RatchetSession; bob: RatchetSession } {
  const identity = generateKeyPair()
  const signing = generateSigningKeyPair()
  const signedPreKey = generateKeyPair()

  const bundle: PreKeyBundle = {
    identityKey: identity.publicKey,
    signingKey: signing.publicKey,
    signedPreKey: signedPreKey.publicKey,
    signedPreKeySig: signPreKey(signing.privateKey, signedPreKey.publicKey),
    oneTimePreKey: new Uint8Array(0),
  }

  const aliceKeys = { identity: generateKeyPair(), ephemeral: generateKeyPair() }
  const { sharedSecret, ephemeralPublicKey } = x3dhInitiator(aliceKeys, bundle)
  const bobSecret = x3dhResponder(
    { identity, signedPreKey },
    aliceKeys.identity.publicKey,
    ephemeralPublicKey,
    false,
  )

  return {
    alice: RatchetSession.initiator(sharedSecret, signedPreKey.publicKey),
    bob: RatchetSession.responder(bobSecret, signedPreKey),
  }
}

const send = (from: RatchetSession, text: string) => from.encrypt(toUtf8(text))
const receive = (to: RatchetSession, message: ReturnType<typeof send>) =>
  fromUtf8(to.decrypt(message.header, message.ciphertext))

describe('marshalHeader / unmarshalHeader', () => {
  /**
   * The header is the AEAD's additional data, so these bytes must match Go's
   * `json.Marshal` character for character. Go emits struct fields in
   * declaration order with no whitespace and renders `[]byte` as standard
   * base64; any deviation authenticates different bytes on each side and every
   * message fails to open — with no hint that serialisation is the cause.
   */
  it('serialises exactly as Go does: field order, no spaces, base64 dh', () => {
    const dh = new Uint8Array([1, 2, 3])
    const json = fromUtf8(marshalHeader({ dh, pn: 4, n: 7 }))
    expect(json).toBe(`{"dh":"${toBase64(dh)}","pn":4,"n":7}`)
  })

  it('round-trips', () => {
    const header: RatchetHeader = {
      dh: crypto.getRandomValues(new Uint8Array(32)),
      pn: 3,
      n: 9,
    }
    const parsed = unmarshalHeader(marshalHeader(header))
    expect(Array.from(parsed.dh)).toEqual(Array.from(header.dh))
    expect(parsed.pn).toBe(3)
    expect(parsed.n).toBe(9)
  })

  it('keeps zero counters as 0 rather than dropping them', () => {
    // Go's encoder has no omitempty here, so `{"dh":...,"pn":0,"n":0}` is what
    // the first message of a chain actually looks like on the wire.
    expect(fromUtf8(marshalHeader({ dh: new Uint8Array(0), pn: 0, n: 0 }))).toBe(
      '{"dh":"","pn":0,"n":0}',
    )
  })

  it('defaults missing counters to 0 when parsing', () => {
    const parsed = unmarshalHeader(toUtf8(`{"dh":"${toBase64(new Uint8Array([9]))}"}`))
    expect(parsed.pn).toBe(0)
    expect(parsed.n).toBe(0)
  })

  /**
   * The additional data is the bytes that TRAVELLED, not a re-encoding of the
   * values parsed out of them. These four pin that down; without it interop
   * rests on four independent implementations emitting byte-identical canonical
   * JSON forever, with nothing enforcing it and a `DecryptError` — which reads
   * exactly like a forgery — as the only symptom when one of them drifts.
   * Mirrors the Go tests in `server/pkg/e2e/e2e_test.go`.
   */
  it("keeps a peer's non-canonical encoding verbatim", () => {
    const foreign = toUtf8(`{ "n": 3, "pn": 1, "dh": "${toBase64(new Uint8Array([9]))}" }`)
    const parsed = unmarshalHeader(foreign)

    expect(parsed.n).toBe(3)
    expect(parsed.pn).toBe(1)
    expect(Array.from(marshalHeader(parsed))).toEqual(Array.from(foreign))
  })

  it('still encodes a header that has no bytes of its own', () => {
    // The sending side of a first message, and the one case where matching Go's
    // encoder is still what carries compatibility.
    const dh = new Uint8Array([1, 2, 3])
    expect(fromUtf8(marshalHeader({ dh, pn: 4, n: 7 }))).toBe(
      `{"dh":"${toBase64(dh)}","pn":4,"n":7}`,
    )
  })

  it('survives a base64 hop, which is how the header reaches the peer', () => {
    const header: RatchetHeader = {
      dh: crypto.getRandomValues(new Uint8Array(32)),
      pn: 1,
      n: 2,
    }
    const wire = toBase64(marshalHeader(header))
    expect(Array.from(unmarshalHeader(fromBase64(wire)).dh)).toEqual(Array.from(header.dh))
  })
})

describe('RatchetSession header authentication', () => {
  it('puts the bytes it authenticated on the wire', () => {
    // If these two ever differ, the receiver checks against something the sender
    // never signed and the two agree only by coincidence of encoder. Proven via
    // the round trip, because the association is deliberately not reachable from
    // the header object.
    const { alice, bob } = handshake()
    const { header, ciphertext } = send(alice, 'm')
    expect(fromUtf8(bob.decrypt(unmarshalHeader(marshalHeader(header)), ciphertext))).toBe('m')
  })

  it('refuses a header whose counters were rewritten after parsing', () => {
    // A spread copies the values and drops the association, so the AD falls back
    // to encoding the counters the object actually carries — which is the point:
    // the ratchet must never advance on a `pn` the AEAD did not verify.
    const { alice, bob } = handshake()
    const { header, ciphertext } = send(alice, 'hello')

    expect(() => bob.decrypt({ ...header, pn: 99 }, ciphertext)).toThrow(DecryptError)
  })

  it('opens a message whose header round-tripped through the wire', () => {
    const { alice, bob } = handshake()
    const { header, ciphertext } = send(alice, 'hello')

    const parsed = unmarshalHeader(marshalHeader(header))
    expect(fromUtf8(bob.decrypt(parsed, ciphertext))).toBe('hello')
  })

  it('rejects a header edited in flight', () => {
    // Carrying the bytes must not weaken the check it feeds: the counters and
    // the ratchet key are covered because they are inside the verified bytes.
    const { alice, bob } = handshake()
    const { header, ciphertext } = send(alice, 'hello')

    const forged = fromUtf8(marshalHeader(header)).replace('"n":0', '"n":7')
    expect(forged).not.toBe(fromUtf8(marshalHeader(header)))
    expect(() => bob.decrypt(unmarshalHeader(toUtf8(forged)), ciphertext)).toThrow(DecryptError)
  })
})

describe('RatchetSession basic exchange', () => {
  it('carries a message from initiator to responder', () => {
    const { alice, bob } = handshake()
    expect(receive(bob, send(alice, 'hello'))).toBe('hello')
  })

  it('carries multi-byte text intact', () => {
    const { alice, bob } = handshake()
    const text = 'Привет 🔐 你好'
    expect(receive(bob, send(alice, text))).toBe(text)
  })

  it('carries an empty message', () => {
    // An attachment-only message has no text body; the AEAD still has to produce
    // a valid (tag-only) ciphertext.
    const { alice, bob } = handshake()
    expect(receive(bob, send(alice, ''))).toBe('')
  })

  it('keeps several messages in order', () => {
    const { alice, bob } = handshake()
    for (const text of ['one', 'two', 'three']) {
      expect(receive(bob, send(alice, text))).toBe(text)
    }
  })

  it('supports a full back-and-forth conversation', () => {
    // Each reversal triggers a DH ratchet step on the other side. This is the
    // path that actually exercises post-compromise security.
    const { alice, bob } = handshake()
    expect(receive(bob, send(alice, 'a1'))).toBe('a1')
    expect(receive(alice, send(bob, 'b1'))).toBe('b1')
    expect(receive(bob, send(alice, 'a2'))).toBe('a2')
    expect(receive(alice, send(bob, 'b2'))).toBe('b2')
    expect(receive(bob, send(alice, 'a3'))).toBe('a3')
  })

  it('refuses to encrypt before the responder has a sending chain', () => {
    // A responder that has received nothing has no chain to derive from. Silently
    // producing a message would be worse: the peer could never open it.
    const { bob } = handshake()
    expect(() => bob.encrypt(toUtf8('too early'))).toThrow(/no sending chain/)
  })

  it('numbers messages within a chain', () => {
    const { alice } = handshake()
    expect(send(alice, 'a').header.n).toBe(0)
    expect(send(alice, 'b').header.n).toBe(1)
    expect(send(alice, 'c').header.n).toBe(2)
  })

  it('never emits the same ciphertext twice for the same plaintext', () => {
    // The AEAD uses a fixed nonce, so key reuse — not nonce reuse — is what would
    // break it. Identical ciphertexts would mean the chain key never advanced.
    const { alice } = handshake()
    const first = toBase64(send(alice, 'same').ciphertext)
    const second = toBase64(send(alice, 'same').ciphertext)
    expect(second).not.toBe(first)
  })

  it('advances the ratchet key when the direction reverses', () => {
    const { alice, bob } = handshake()
    const firstFromAlice = send(alice, 'a1')
    receive(bob, firstFromAlice)
    receive(alice, send(bob, 'b1'))
    const secondFromAlice = send(alice, 'a2')

    expect(toBase64(secondFromAlice.header.dh)).not.toBe(toBase64(firstFromAlice.header.dh))
  })

  it('reports the previous chain length after a ratchet step', () => {
    // `pn` is how the peer knows how many keys to skip in the chain being left
    // behind; an undercount permanently loses the tail of that chain.
    const { alice, bob } = handshake()
    receive(bob, send(alice, 'a1'))
    send(alice, 'a2')
    send(alice, 'a3')
    receive(alice, send(bob, 'b1'))

    expect(send(alice, 'a4').header.pn).toBe(3)
  })
})

describe('RatchetSession out-of-order delivery', () => {
  /**
   * The gateway does not promise ordering across a reconnect, and a multi-device
   * peer can interleave sends. Skipped-key handling is what keeps a late arrival
   * readable instead of permanently lost.
   */
  it('opens a message that arrives after a later one', () => {
    const { alice, bob } = handshake()
    const first = send(alice, 'first')
    const second = send(alice, 'second')

    expect(receive(bob, second)).toBe('second')
    expect(receive(bob, first)).toBe('first')
  })

  it('opens several messages delivered in reverse', () => {
    const { alice, bob } = handshake()
    const messages = ['m0', 'm1', 'm2', 'm3'].map((text) => send(alice, text))

    for (let i = messages.length - 1; i >= 0; i--) {
      expect(receive(bob, messages[i])).toBe(`m${i}`)
    }
  })

  it('recovers messages stranded in a chain the peer already left', () => {
    // Alice sends three, Bob only receives the first, then replies — which
    // ratchets Alice forward. The two stranded messages must still open.
    const { alice, bob } = handshake()
    const a1 = send(alice, 'a1')
    const a2 = send(alice, 'a2')
    const a3 = send(alice, 'a3')

    expect(receive(bob, a1)).toBe('a1')
    receive(alice, send(bob, 'b1'))
    const a4 = send(alice, 'a4')

    expect(receive(bob, a4)).toBe('a4')
    expect(receive(bob, a2)).toBe('a2')
    expect(receive(bob, a3)).toBe('a3')
  })

  it('rejects a replay of an already-opened message', () => {
    // A message key is consumed on use. Accepting a replay would let the relay
    // duplicate a message into the conversation.
    const { alice, bob } = handshake()
    const first = send(alice, 'first')
    receive(bob, send(alice, 'second'))
    expect(receive(bob, first)).toBe('first')

    expect(() => bob.decrypt(first.header, first.ciphertext)).toThrow()
  })

  it('refuses a header claiming an implausible gap', () => {
    // Without the bound, a hostile `n` would make the receiver derive millions
    // of keys and hang the tab — a cheap denial of service through a relay.
    const { alice, bob } = handshake()
    const message = send(alice, 'hello')

    expect(() => bob.decrypt({ ...message.header, n: 5_000_000 }, message.ciphertext)).toThrow(
      /too many skipped/,
    )
  })
})

describe('RatchetSession tamper detection', () => {
  it('rejects modified ciphertext', () => {
    const { alice, bob } = handshake()
    const message = send(alice, 'hello')
    message.ciphertext[0] ^= 0xff

    expect(() => bob.decrypt(message.header, message.ciphertext)).toThrow(DecryptError)
  })

  it('rejects a truncated ciphertext', () => {
    const { alice, bob } = handshake()
    const message = send(alice, 'hello')

    expect(() => bob.decrypt(message.header, message.ciphertext.slice(0, -1))).toThrow(
      DecryptError,
    )
  })

  /**
   * The header travels in the clear but is authenticated as additional data.
   * These three cases are what that buys: a relay cannot renumber, reorder or
   * re-key a message without the receiver noticing.
   */
  it('rejects a message whose header counter was rewritten', () => {
    const { alice, bob } = handshake()
    send(alice, 'a0')
    const message = send(alice, 'a1')

    expect(() => bob.decrypt({ ...message.header, n: 0 }, message.ciphertext)).toThrow()
  })

  it('rejects a message whose header pn was rewritten', () => {
    const { alice, bob } = handshake()
    const message = send(alice, 'hello')

    expect(() => bob.decrypt({ ...message.header, pn: 99 }, message.ciphertext)).toThrow()
  })

  it('rejects a message whose ratchet key was substituted', () => {
    const { alice, bob } = handshake()
    const message = send(alice, 'hello')
    const attacker = generateKeyPair()

    expect(() =>
      bob.decrypt({ ...message.header, dh: attacker.publicKey }, message.ciphertext),
    ).toThrow()
  })

  it('does not open a message meant for a different session', () => {
    const first = handshake()
    const second = handshake()
    const message = send(first.alice, 'hello')

    expect(() => second.bob.decrypt(message.header, message.ciphertext)).toThrow()
  })

  it('names the failure so the UI can show "cannot decrypt" instead of crashing', () => {
    const { alice, bob } = handshake()
    const message = send(alice, 'hello')
    message.ciphertext[1] ^= 0x01

    try {
      bob.decrypt(message.header, message.ciphertext)
      expect.unreachable('tampered ciphertext must throw')
    } catch (error) {
      expect((error as Error).name).toBe('DecryptError')
    }
  })
})

describe('RatchetSession persistence', () => {
  /**
   * Sessions outlive the tab. Everything the ratchet needs has to survive a JSON
   * round-trip through localStorage — and a field quietly dropped from
   * `serialize` shows up as an unreadable conversation after a reload, by which
   * point the original keys are gone.
   */
  it('keeps sending where it left off after a reload', () => {
    const { alice, bob } = handshake()
    receive(bob, send(alice, 'before'))

    const restored = RatchetSession.deserialize(
      JSON.parse(JSON.stringify(alice.serialize())) as ReturnType<typeof alice.serialize>,
    )

    expect(receive(bob, send(restored, 'after'))).toBe('after')
  })

  it('keeps receiving where it left off after a reload', () => {
    const { alice, bob } = handshake()
    receive(bob, send(alice, 'before'))

    const restored = RatchetSession.deserialize(
      JSON.parse(JSON.stringify(bob.serialize())) as ReturnType<typeof bob.serialize>,
    )

    expect(receive(restored, send(alice, 'after'))).toBe('after')
  })

  it('carries skipped keys across a reload', () => {
    // The whole point of storing a skipped key is that the late message may not
    // arrive until after the user has closed and reopened the tab.
    const { alice, bob } = handshake()
    const first = send(alice, 'first')
    receive(bob, send(alice, 'second'))

    const restored = RatchetSession.deserialize(
      JSON.parse(JSON.stringify(bob.serialize())) as ReturnType<typeof bob.serialize>,
    )

    expect(receive(restored, first)).toBe('first')
  })

  it('survives a reload across a ratchet step in both directions', () => {
    const roundTrip = (session: RatchetSession) =>
      RatchetSession.deserialize(
        JSON.parse(JSON.stringify(session.serialize())) as ReturnType<typeof session.serialize>,
      )

    let { alice, bob } = handshake()
    expect(receive(bob, send(alice, 'a1'))).toBe('a1')

    alice = roundTrip(alice)
    bob = roundTrip(bob)
    expect(receive(alice, send(bob, 'b1'))).toBe('b1')

    alice = roundTrip(alice)
    bob = roundTrip(bob)
    expect(receive(bob, send(alice, 'a2'))).toBe('a2')
  })

  it('serialises to JSON-safe values only', () => {
    const { alice } = handshake()
    const state = alice.serialize()

    expect(typeof state.dhsPrivate).toBe('string')
    expect(typeof state.rootKey).toBe('string')
    expect(typeof state.sent).toBe('number')
    // Round-tripping through JSON must not change the shape.
    expect(JSON.parse(JSON.stringify(state))).toEqual(state)
  })

  it('preserves a null receiving chain for a session that has sent only', () => {
    // `null` and "32 zero bytes" are different states; conflating them would let
    // a fresh initiator "decrypt" with an all-zero chain key.
    const { alice } = handshake()
    expect(alice.serialize().receivingChainKey).toBeNull()
  })

  it('tolerates a stored state written before skipped keys existed', () => {
    const { alice, bob } = handshake()
    const state = bob.serialize()
    delete (state as Partial<typeof state>).skipped

    const restored = RatchetSession.deserialize(state)
    expect(receive(restored, send(alice, 'hello'))).toBe('hello')
  })
})

describe('a forged frame', () => {
  /**
   * The regression test for the defect this file shipped with: `decrypt` ran the
   * DH ratchet and the skipped-key walk BEFORE authenticating, so anyone able to
   * reach the relay could rewrite a session with one frame and permanently break
   * the real conversation. The header is attacker-written, so the ordering is
   * the whole security property.
   */
  it('leaves the session able to keep talking to the real peer', () => {
    const { alice, bob } = handshake()
    expect(receive(bob, send(alice, 'first'))).toBe('first')

    const attacker = generateKeyPair()
    expect(() =>
      bob.decrypt({ dh: attacker.publicKey, pn: 500, n: 500 }, toUtf8('not a real ciphertext')),
    ).toThrow(DecryptError)

    // Before the fix this threw: bob's root key and DHr had been replaced by
    // the attacker's.
    expect(receive(bob, send(alice, 'second'))).toBe('second')
  })

  // The 20s budget is not slack, it is the measurement: this test deliberately
  // asks for the most expensive thing a forged frame can demand — 20 frames each
  // claiming a 999-message gap — and each one costs ~2000 HMAC derivations on a
  // discarded copy. It ran at ~4s against the 5s default, so it failed by
  // timeout on a loaded machine and passed on an idle one, which reads as a
  // flaky crypto test rather than as what it is.
  it('stores no skipped keys', () => {
    const { alice, bob } = handshake()
    receive(bob, send(alice, 'first'))
    const before = Object.keys(bob.serialize().skipped).length

    for (let i = 0; i < 20; i++) {
      const attacker = generateKeyPair()
      try {
        bob.decrypt({ dh: attacker.publicKey, pn: 999, n: 999 }, toUtf8('junk'))
      } catch {
        // expected
      }
    }

    expect(Object.keys(bob.serialize().skipped).length).toBe(before)
  }, 20_000)

  it('does not advance the receive counter', () => {
    const { alice, bob } = handshake()
    receive(bob, send(alice, 'first'))
    const before = bob.serialize().received

    const attacker = generateKeyPair()
    try {
      bob.decrypt({ dh: attacker.publicKey, pn: 0, n: 0 }, toUtf8('junk'))
    } catch {
      // expected
    }
    expect(bob.serialize().received).toBe(before)
  })

  it('does not corrupt the in-order fast path either', () => {
    // A forgery on the CURRENT ratchet key takes the cheap branch, which holds
    // its state in locals rather than staging a copy — same guarantee, different
    // mechanism, so it needs its own case.
    const { alice, bob } = handshake()
    const real = send(alice, 'real')
    expect(() => bob.decrypt(real.header, toUtf8('wrong ciphertext'))).toThrow(DecryptError)
    expect(receive(bob, real)).toBe('real')
  })
})

describe('the skipped-key store', () => {
  it('stays bounded across ratchet steps', () => {
    // MAX_SKIP only ever bounded ONE call; every DH ratchet step restarted the
    // count, so the map grew without limit — and it is persisted, so the growth
    // outlived the tab.
    const { alice, bob } = handshake()

    for (let round = 0; round < 3; round++) {
      let last = send(alice, 'filler')
      for (let i = 0; i < 900; i++) last = send(alice, 'filler')
      receive(bob, last)
    }

    expect(Object.keys(bob.serialize().skipped).length).toBeLessThanOrEqual(2000)
  })

  it('still opens an out-of-order message after staging was introduced', () => {
    const { alice, bob } = handshake()
    const one = send(alice, 'one')
    const two = send(alice, 'two')
    const three = send(alice, 'three')

    expect(receive(bob, three)).toBe('three')
    expect(receive(bob, one)).toBe('one')
    expect(receive(bob, two)).toBe('two')
  })

  it('round-trips its eviction order through persistence', () => {
    const { alice, bob } = handshake()
    send(alice, 'skipped')
    receive(bob, send(alice, 'arrived'))

    const state = bob.serialize()
    expect(state.skippedOrder).toEqual(Object.keys(state.skipped))

    const restored = RatchetSession.deserialize(state)
    expect(restored.serialize().skippedOrder).toEqual(state.skippedOrder)
  })

  it('recovers an eviction order for a state written before it existed', () => {
    const { alice, bob } = handshake()
    send(alice, 'skipped')
    receive(bob, send(alice, 'arrived'))

    const state = bob.serialize()
    delete (state as Partial<typeof state>).skippedOrder

    const restored = RatchetSession.deserialize(state)
    expect(restored.serialize().skippedOrder).toEqual(Object.keys(state.skipped))
  })
})
