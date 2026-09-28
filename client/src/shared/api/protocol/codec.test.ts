import { describe, expect, it } from 'vitest'

import { decodeBody, encodeBody, hasBody } from './codec'
import { MsgType, msgTypeName } from './msg-type'

/** Types the server sends with no body at all — see the gateway's ping loop. */
const BODILESS = [MsgType.PING, MsgType.PONG, MsgType.T_ACK, MsgType.RESERVED]

describe('hasBody', () => {
  /**
   * The read loop calls `hasBody` before decoding. A type wrongly reported as
   * bodiless silently drops its payload; one wrongly reported as having a body
   * throws on every keepalive.
   */
  it('reports a body for every type the client actually sends or receives', () => {
    const missing = (Object.entries(MsgType) as [string, number][])
      .filter(([, value]) => !BODILESS.includes(value as never))
      .filter(([, value]) => !hasBody(value))
      .map(([name]) => name)

    expect(missing).toEqual([])
  })

  it('reports no body for the liveness and ack types', () => {
    for (const type of BODILESS) expect(hasBody(type)).toBe(false)
  })

  it('reports no body for an unknown type', () => {
    expect(hasBody(200)).toBe(false)
  })
})

describe('encodeBody', () => {
  it('encodes to zero bytes for a null body', () => {
    // PING and friends go out with an empty payload; passing null must not need
    // the caller to special-case the type.
    expect(encodeBody(MsgType.PING, null).length).toBe(0)
    expect(encodeBody(MsgType.PING).length).toBe(0)
  })

  it('encodes a populated body to non-empty bytes', () => {
    expect(
      encodeBody(MsgType.AUTH, { username: 'alice', password: 'secret' }).length,
    ).toBeGreaterThan(0)
  })

  it('is deterministic', () => {
    const once = encodeBody(MsgType.AUTH, { username: 'alice', password: 'x' })
    const twice = encodeBody(MsgType.AUTH, { username: 'alice', password: 'x' })
    expect(Array.from(once)).toEqual(Array.from(twice))
  })

  it('omits proto3 scalar defaults, as the Go encoder does', () => {
    // An all-defaults message is zero bytes on the wire. If we emitted the
    // fields explicitly the frames would still parse, but a byte-for-byte
    // comparison against the Go client (which the interop tests do) would fail.
    expect(encodeBody(MsgType.AUTH, { username: '', password: '' }).length).toBe(0)
  })

  it('names the offending type when the type carries no body', () => {
    expect(() => encodeBody(MsgType.PING, { anything: 1 })).toThrow(/PING/)
  })

  it('names an unknown type by number', () => {
    expect(() => encodeBody(200, { a: 1 })).toThrow(/UNKNOWN\(200\)/)
  })

  it('rejects a field of the wrong type instead of writing a corrupt frame', () => {
    // protobufjs coerces liberally on its own; `verify` is what turns a
    // programming mistake into an error here rather than a decode failure on the
    // server, which is far harder to trace back to its call site.
    expect(() => encodeBody(MsgType.AUTH, { username: 42 })).toThrow(/AUTH/)
  })
})

describe('decodeBody', () => {
  it('round-trips a simple body', () => {
    const encoded = encodeBody(MsgType.AUTH, { username: 'alice', password: 'secret' })
    expect(
      decodeBody<{ username: string; password: string }>(MsgType.AUTH, encoded),
    ).toMatchObject({ username: 'alice', password: 'secret' })
  })

  it('round-trips unicode text', () => {
    const encoded = encodeBody(MsgType.SEND, { chatId: 'c1', text: 'Привет 🔐' })
    expect(decodeBody<{ text: string }>(MsgType.SEND, encoded).text).toBe('Привет 🔐')
  })

  it('materialises defaults so callers never read undefined', () => {
    // `defaults: true` is what lets the UI render `body.text` without a guard on
    // every access; without it an absent proto3 string comes back undefined.
    const decoded = decodeBody<Record<string, unknown>>(
      MsgType.AUTH,
      encodeBody(MsgType.AUTH, { username: 'alice' }),
    )
    expect(decoded.password).toBe('')
  })

  it('decodes an empty payload to an all-defaults object', () => {
    // A body the server omitted entirely must still decode, not throw.
    expect(() => decodeBody(MsgType.AUTH, new Uint8Array(0))).not.toThrow()
  })

  it('converts 64-bit fields to plain numbers, not Long instances', () => {
    // `longs: Number` — the UI compares and sorts `chatSeq`, and a Long object
    // would make `a > b` compare object identity and silently misorder history.
    const encoded = encodeBody(MsgType.READ, { chatId: 'c1', upToChatSeq: 42 })
    const decoded = decodeBody<{ upToChatSeq: number }>(MsgType.READ, encoded)
    expect(typeof decoded.upToChatSeq).toBe('number')
    expect(decoded.upToChatSeq).toBe(42)
  })

  it('preserves a large sequence number exactly', () => {
    const big = Number.MAX_SAFE_INTEGER
    const encoded = encodeBody(MsgType.READ, { chatId: 'c1', upToChatSeq: big })
    expect(decodeBody<{ upToChatSeq: number }>(MsgType.READ, encoded).upToChatSeq).toBe(big)
  })

  it('materialises repeated fields as arrays, not undefined', () => {
    // `arrays: true`; the chat list maps over this directly.
    const decoded = decodeBody<{ chats?: unknown[] }>(
      MsgType.CHATS,
      encodeBody(MsgType.CHATS, {}),
    )
    expect(Array.isArray(decoded.chats)).toBe(true)
  })

  it('names the type when it has no body', () => {
    expect(() => decodeBody(MsgType.PING, new Uint8Array(0))).toThrow(/PING/)
  })

  it('names an unknown type by number', () => {
    expect(() => decodeBody(200, new Uint8Array(0))).toThrow(/UNKNOWN\(200\)/)
  })

  it('throws rather than returning garbage for a malformed payload', () => {
    // A truncated frame must not produce a half-populated object the UI then
    // renders as a real message.
    const garbage = new Uint8Array([0xff, 0xff, 0xff, 0xff, 0xff])
    expect(() => decodeBody(MsgType.AUTH, garbage)).toThrow()
  })
})

describe('body type mapping', () => {
  /**
   * These four aliases are the ones a reader is most likely to "fix" into
   * separate messages. Each pair genuinely shares a body on the server side, and
   * splitting them would break decoding for the second type in the pair.
   */
  it('round-trips PIN, UNPIN and PIN_LIST through the same PinAction body', () => {
    const body = { chatId: 'c1', messageId: 'm1' }
    for (const type of [MsgType.PIN, MsgType.UNPIN, MsgType.PIN_LIST]) {
      const decoded = decodeBody<typeof body>(type, encodeBody(type, body))
      expect({ type: msgTypeName(type), decoded }).toEqual({
        type: msgTypeName(type),
        decoded: expect.objectContaining(body),
      })
    }
  })

  it('round-trips KEY_FETCH_ALL through the KeyFetch body', () => {
    const encoded = encodeBody(MsgType.KEY_FETCH_ALL, { userId: 'u1' })
    expect(decodeBody<{ userId: string }>(MsgType.KEY_FETCH_ALL, encoded).userId).toBe('u1')
  })

  it('round-trips DELIVERED through the ReadUpdate body', () => {
    const body = { chatId: 'c1', userId: 'u1', upToChatSeq: 7 }
    const decoded = decodeBody<typeof body>(
      MsgType.DELIVERED,
      encodeBody(MsgType.DELIVERED, body),
    )
    expect(decoded).toMatchObject(body)
  })

  it('encodes SECRET_SEND and SECRET_RECV identically', () => {
    // One SecretMsg body, two directions. A client that encoded them differently
    // would send frames its own peer could not read back.
    const body = { chatId: 'c1', toUserId: 'u2' }
    expect(Array.from(encodeBody(MsgType.SECRET_SEND, body))).toEqual(
      Array.from(encodeBody(MsgType.SECRET_RECV, body)),
    )
  })

  it('encodes the three CALL actions through one CallAction body', () => {
    const body = { callId: 'call-1' }
    const encoded = [MsgType.CALL_ACCEPT, MsgType.CALL_DECLINE, MsgType.CALL_HANGUP].map(
      (type) => Array.from(encodeBody(type, body)),
    )
    expect(encoded[1]).toEqual(encoded[0])
    expect(encoded[2]).toEqual(encoded[0])
  })

  it('uses Error for both ERROR and AUTH_ERR', () => {
    const body = { code: 1, message: 'nope' }
    expect(Array.from(encodeBody(MsgType.AUTH_ERR, body))).toEqual(
      Array.from(encodeBody(MsgType.ERROR, body)),
    )
  })
})
