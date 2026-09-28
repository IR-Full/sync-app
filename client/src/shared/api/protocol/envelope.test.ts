import { describe, expect, it } from 'vitest'

import { EnvelopeError, decodeEnvelope, encodeEnvelope, type Envelope } from './envelope'

const empty = new Uint8Array(0)

function env(over: Partial<Envelope> = {}): Envelope {
  return { type: 1, seq: 0, ack: 0, requestId: 0, body: empty, ...over }
}

describe('encodeEnvelope', () => {
  it('writes the five header varints in the order the server reads them', () => {
    // Type · Seq · Ack · RequestID · len(Body) · Body — mirrors
    // server/pkg/wire/envelope.go. Values under 0x80 are one byte each, so the
    // layout is directly readable here.
    const encoded = encodeEnvelope(
      env({ type: 3, seq: 4, ack: 5, requestId: 6, body: new Uint8Array([0xff]) }),
    )
    expect(Array.from(encoded)).toEqual([3, 4, 5, 6, 1, 0xff])
  })

  it('encodes an empty body as a zero length with no payload', () => {
    // A protobuf message at its defaults encodes to nothing, and the gateway
    // sends exactly that for "empty" replies — so this is a normal frame.
    const encoded = encodeEnvelope(env({ type: 9 }))
    expect(Array.from(encoded)).toEqual([9, 0, 0, 0, 0])
  })

  it('uses multi-byte varints past 127', () => {
    const encoded = encodeEnvelope(env({ type: 128 }))
    // LEB128: 128 → 0x80 0x01
    expect(encoded[0]).toBe(0x80)
    expect(encoded[1]).toBe(0x01)
  })
})

describe('decodeEnvelope', () => {
  it('round-trips every header field', () => {
    const body = new Uint8Array([1, 2, 3, 4])
    const got = decodeEnvelope(
      encodeEnvelope(env({ type: 42, seq: 7, ack: 5, requestId: 99, body })),
    )

    expect(got.type).toBe(42)
    expect(got.seq).toBe(7)
    expect(got.ack).toBe(5)
    expect(got.requestId).toBe(99)
    expect(Array.from(got.body)).toEqual([1, 2, 3, 4])
  })

  it('round-trips an empty body', () => {
    const got = decodeEnvelope(encodeEnvelope(env({ type: 5 })))
    expect(got.body.length).toBe(0)
  })

  /**
   * Seq, Ack and RequestID are uint64 on the wire. JS bitwise operators truncate
   * to 32 bits, so a codec written with `>>>` corrupts any value past ~4.29e9 —
   * silently, and only on a long-lived connection.
   */
  it('survives values past the 32-bit boundary', () => {
    for (const value of [0xffffffff, 0x100000000, 0x1ffffffff, Number.MAX_SAFE_INTEGER]) {
      const got = decodeEnvelope(
        encodeEnvelope(env({ seq: value, ack: value, requestId: value })),
      )
      expect(got.seq).toBe(value)
      expect(got.ack).toBe(value)
      expect(got.requestId).toBe(value)
    }
  })

  it('rejects a truncated varint', () => {
    // A header byte with the continuation bit set and nothing after it.
    expect(() => decodeEnvelope(new Uint8Array([0x80]))).toThrow(EnvelopeError)
  })

  it('rejects an envelope whose body is shorter than its length claims', () => {
    const encoded = encodeEnvelope(env({ type: 1, body: new Uint8Array([1, 2, 3, 4]) }))
    expect(() => decodeEnvelope(encoded.subarray(0, encoded.length - 2))).toThrow(
      /truncated body/,
    )
  })

  it('rejects a varint that would exceed the safe integer range', () => {
    // Ten continuation bytes overflow any 64-bit value; decoding it as a JS
    // number would lose precision rather than fail, so the parser refuses.
    const hostile = new Uint8Array(12).fill(0xff)
    expect(() => decodeEnvelope(hostile)).toThrow(EnvelopeError)
  })

  it('rejects an empty payload', () => {
    expect(() => decodeEnvelope(empty)).toThrow(EnvelopeError)
  })

  it('returns a body view that does not alias past its declared length', () => {
    const encoded = encodeEnvelope(env({ type: 1, body: new Uint8Array([7, 7]) }))
    const padded = new Uint8Array(encoded.length + 4)
    padded.set(encoded)

    const got = decodeEnvelope(padded)
    // Trailing bytes after the declared body must not leak into it.
    expect(Array.from(got.body)).toEqual([7, 7])
  })
})
