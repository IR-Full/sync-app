import { describe, expect, it } from 'vitest'

import {
  Flag,
  FrameError,
  HEADER_SIZE,
  MAGIC_0,
  MAGIC_1,
  MAX_PAYLOAD_SIZE,
  VERSION,
  decodeFrame,
  encodeFrame,
} from './frame'

/**
 * These assert against the byte layout in `server/pkg/wire/frame.go` rather than
 * round-tripping our own encoder. A round trip passes just as happily when both
 * halves of this client agree on the wrong thing — and the server is the side
 * that has to agree.
 */
describe('encodeFrame', () => {
  it('writes the header the server parses', () => {
    const frame = encodeFrame(new Uint8Array([0xaa, 0xbb]))

    expect(frame[0]).toBe(MAGIC_0) // 'S'
    expect(frame[1]).toBe(MAGIC_1) // 'C'
    expect(frame[2]).toBe(VERSION)
    expect(frame[3]).toBe(Flag.NONE)
    // Length is big-endian, which is the one byte order a little-endian machine
    // gets wrong silently.
    expect(Array.from(frame.slice(4, 8))).toEqual([0, 0, 0, 2])
    expect(Array.from(frame.slice(8))).toEqual([0xaa, 0xbb])
  })

  it('encodes an empty payload as a bare header', () => {
    const frame = encodeFrame(new Uint8Array(0))
    expect(frame.length).toBe(HEADER_SIZE)
    expect(Array.from(frame.slice(4, 8))).toEqual([0, 0, 0, 0])
  })

  it('writes a four-byte length for a payload past 255 bytes', () => {
    // The boundary where a single-byte length would still look correct.
    const frame = encodeFrame(new Uint8Array(300))
    expect(Array.from(frame.slice(4, 8))).toEqual([0, 0, 0x01, 0x2c]) // 300
  })

  it('refuses a payload past the server parser bound', () => {
    // The server caps the length before allocating, so sending more is a frame
    // it will reject after we have already paid to build it.
    const tooBig = new Uint8Array(MAX_PAYLOAD_SIZE + 1)
    expect(() => encodeFrame(tooBig)).toThrow(FrameError)
  })

  it('carries an explicit flag byte when given one', () => {
    const frame = encodeFrame(new Uint8Array([1]), Flag.COMPRESSED)
    expect(frame[3]).toBe(Flag.COMPRESSED)
  })
})

describe('decodeFrame', () => {
  it('round-trips a payload', () => {
    const payload = new Uint8Array([1, 2, 3, 4, 5])
    expect(Array.from(decodeFrame(encodeFrame(payload)))).toEqual([1, 2, 3, 4, 5])
  })

  it('round-trips an empty payload', () => {
    expect(decodeFrame(encodeFrame(new Uint8Array(0))).length).toBe(0)
  })

  it('rejects a frame shorter than the header', () => {
    expect(() => decodeFrame(new Uint8Array([MAGIC_0, MAGIC_1, VERSION]))).toThrow(
      /short frame/,
    )
  })

  it('rejects garbage that is not this protocol', () => {
    // The magic word is what lets both ends reject a port scan cheaply.
    const notOurs = new Uint8Array(HEADER_SIZE)
    notOurs[0] = 0x47 // 'G', as in an HTTP request
    notOurs[1] = 0x45 // 'E'
    expect(() => decodeFrame(notOurs)).toThrow(/bad magic/)
  })

  it('rejects a framing version it does not understand', () => {
    const frame = encodeFrame(new Uint8Array([1]))
    frame[2] = 0x02
    expect(() => decodeFrame(frame)).toThrow(/unsupported framing version/)
  })

  it('rejects a hostile length prefix before allocating', () => {
    const frame = new Uint8Array(HEADER_SIZE)
    frame[0] = MAGIC_0
    frame[1] = MAGIC_1
    frame[2] = VERSION
    new DataView(frame.buffer).setUint32(4, MAX_PAYLOAD_SIZE + 1, false)
    expect(() => decodeFrame(frame)).toThrow(/too large/)
  })

  it('rejects a frame whose payload is shorter than its length claims', () => {
    // A truncated message must fail rather than hand a partial body upward.
    const frame = encodeFrame(new Uint8Array([1, 2, 3, 4, 5]))
    expect(() => decodeFrame(frame.subarray(0, frame.length - 2))).toThrow(/truncated/)
  })

  it('refuses a compressed frame, because no compression was negotiated', () => {
    // The client advertises neither CapCompression nor CapZstd, so the gateway
    // never compresses for it. A compressed frame therefore means the two ends
    // disagree about the handshake — failing loudly beats handing a compressed
    // blob to the envelope parser.
    const frame = encodeFrame(new Uint8Array([1, 2, 3]), Flag.COMPRESSED)
    expect(() => decodeFrame(frame)).toThrow(/compression capability/)

    const zstd = encodeFrame(new Uint8Array([1, 2, 3]), Flag.ZSTD)
    expect(() => decodeFrame(zstd)).toThrow(/compression capability/)
  })

  it('reads a frame that sits at a non-zero offset in its buffer', () => {
    // A WebSocket message often arrives as a view into a larger buffer, and
    // reading the length through the wrong offset is the classic way to get a
    // plausible-but-wrong number.
    const payload = new Uint8Array([9, 8, 7])
    const frame = encodeFrame(payload)
    const padded = new Uint8Array(frame.length + 4)
    padded.set(frame, 4)
    const view = padded.subarray(4)

    expect(Array.from(decodeFrame(view))).toEqual([9, 8, 7])
  })

  it('accepts a frame with trailing bytes after the declared payload', () => {
    const frame = encodeFrame(new Uint8Array([1, 2]))
    const withTrailer = new Uint8Array(frame.length + 3)
    withTrailer.set(frame)
    expect(Array.from(decodeFrame(withTrailer))).toEqual([1, 2])
  })
})
