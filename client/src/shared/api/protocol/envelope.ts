/**
 * Envelope codec — mirrors `server/pkg/wire/envelope.go`.
 *
 * Header field order, all LEB128 unsigned varints:
 *
 *   Type · Seq · Ack · RequestID · len(Body) · Body
 *
 * Why each field matters to us:
 *   Seq       per-connection monotonic sender sequence — ordering + gap detection
 *             + the anchor for RESUME.
 *   Ack       highest contiguous Seq the sender has processed, piggybacked on
 *             every frame so an idle client rarely needs an explicit T_ACK.
 *   RequestID correlates a reply with its request independently of ordering,
 *             which is what lets many requests be in flight over one socket.
 *             It is also how we tell a HISTORY page (echoes our RequestID) from
 *             live fanout (RequestID 0).
 */

export interface Envelope {
  type: number
  seq: number
  ack: number
  requestId: number
  body: Uint8Array
}

export class EnvelopeError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'EnvelopeError'
  }
}

/** Byte length of `value` when LEB128-encoded. */
function varintLength(value: number): number {
  let n = 1
  let v = Math.floor(value)
  while (v >= 0x80) {
    v = Math.floor(v / 0x80)
    n++
  }
  return n
}

/**
 * Writes a LEB128 varint, returning the new offset.
 *
 * Division rather than `>>>` shifting is deliberate: sequence numbers and
 * request ids are uint64 on the wire, and JS bitwise operators truncate to 32
 * bits, which would silently corrupt any value past 4 294 967 295.
 */
function writeVarint(out: Uint8Array, offset: number, value: number): number {
  let v = Math.floor(value)
  while (v >= 0x80) {
    out[offset++] = (v % 0x80) | 0x80
    v = Math.floor(v / 0x80)
  }
  out[offset++] = v
  return offset
}

function readVarint(buf: Uint8Array, offset: number): [value: number, next: number] {
  let value = 0
  let shift = 1
  let pos = offset
  for (;;) {
    if (pos >= buf.length) throw new EnvelopeError('short envelope: truncated varint')
    const byte = buf[pos++]
    value += (byte & 0x7f) * shift
    if ((byte & 0x80) === 0) break
    shift *= 0x80
    if (shift > Number.MAX_SAFE_INTEGER) {
      throw new EnvelopeError('varint exceeds safe integer range')
    }
  }
  return [value, pos]
}

export function encodeEnvelope(envelope: Envelope): Uint8Array {
  const { type, seq, ack, requestId, body } = envelope
  const size =
    varintLength(type) +
    varintLength(seq) +
    varintLength(ack) +
    varintLength(requestId) +
    varintLength(body.length) +
    body.length

  const out = new Uint8Array(size)
  let offset = 0
  offset = writeVarint(out, offset, type)
  offset = writeVarint(out, offset, seq)
  offset = writeVarint(out, offset, ack)
  offset = writeVarint(out, offset, requestId)
  offset = writeVarint(out, offset, body.length)
  out.set(body, offset)
  return out
}

export function decodeEnvelope(payload: Uint8Array): Envelope {
  const [type, afterType] = readVarint(payload, 0)
  const [seq, afterSeq] = readVarint(payload, afterType)
  const [ack, afterAck] = readVarint(payload, afterSeq)
  const [requestId, afterRequestId] = readVarint(payload, afterAck)
  const [bodyLength, offset] = readVarint(payload, afterRequestId)

  if (payload.length - offset < bodyLength) {
    throw new EnvelopeError('short envelope: truncated body')
  }
  return {
    type,
    seq,
    ack,
    requestId,
    body: payload.subarray(offset, offset + bodyLength),
  }
}
