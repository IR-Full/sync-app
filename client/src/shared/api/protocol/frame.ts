/**
 * Frame codec — mirrors `server/pkg/wire/frame.go`.
 *
 *   +--------+--------+--------+--------+--------------------+==================+
 *   | 0x53   | 0x43   | VER(1) | FLAGS  | LENGTH (4, BE)     | PAYLOAD (LENGTH) |
 *   |  'S'   |  'C'   |  0x01  | bits   | uint32 <= 16 MiB   | envelope bytes   |
 *   +--------+--------+--------+--------+--------------------+==================+
 *
 * On the WebSocket transport one binary WS message carries exactly one frame,
 * so there is no stream re-assembly to do here — `decodeFrame` takes a whole
 * message. The magic word lets the server cheaply reject garbage; we validate
 * it on the way in for the same reason.
 */

export const MAGIC_0 = 0x53 // 'S'
export const MAGIC_1 = 0x43 // 'C'
export const VERSION = 0x01
export const HEADER_SIZE = 8

/** Matches the server's parser bound; a larger length prefix is hostile input. */
export const MAX_PAYLOAD_SIZE = 16 << 20

/** Frame flag bits. */
export const Flag = {
  NONE: 0,
  /** payload is gzip-compressed */
  COMPRESSED: 1 << 0,
  /** payload is zstd-compressed (with a shared dictionary) */
  ZSTD: 1 << 1,
} as const

export class FrameError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'FrameError'
  }
}

/**
 * Wraps an envelope payload in a frame.
 *
 * The web client never sets a compression flag: it advertises neither
 * CapCompression nor CapZstd during the handshake, so neither side compresses
 * (see `caps.ts` for why). Outbound frames are therefore always raw.
 */
export function encodeFrame(payload: Uint8Array, flags: number = Flag.NONE): Uint8Array {
  if (payload.length > MAX_PAYLOAD_SIZE) {
    throw new FrameError(`payload too large: ${payload.length} > ${MAX_PAYLOAD_SIZE}`)
  }
  const frame = new Uint8Array(HEADER_SIZE + payload.length)
  frame[0] = MAGIC_0
  frame[1] = MAGIC_1
  frame[2] = VERSION
  frame[3] = flags
  new DataView(frame.buffer).setUint32(4, payload.length, false) // big-endian
  frame.set(payload, HEADER_SIZE)
  return frame
}

/** Unwraps one complete frame and returns its envelope payload. */
export function decodeFrame(frame: Uint8Array): Uint8Array {
  if (frame.length < HEADER_SIZE) {
    throw new FrameError('short frame')
  }
  if (frame[0] !== MAGIC_0 || frame[1] !== MAGIC_1) {
    throw new FrameError('bad magic')
  }
  if (frame[2] !== VERSION) {
    throw new FrameError(`unsupported framing version: got ${frame[2]}, want ${VERSION}`)
  }
  const flags = frame[3]
  const view = new DataView(frame.buffer, frame.byteOffset, frame.byteLength)
  const length = view.getUint32(4, false)
  if (length > MAX_PAYLOAD_SIZE) {
    throw new FrameError('payload too large')
  }
  if (frame.length < HEADER_SIZE + length) {
    throw new FrameError('truncated frame')
  }
  if (flags & (Flag.COMPRESSED | Flag.ZSTD)) {
    // Unreachable while we advertise no compression capability: the gateway
    // only sets these flags for peers that negotiated them. Fail loudly rather
    // than hand a compressed blob to the envelope parser.
    throw new FrameError(
      'received a compressed frame but no compression capability was negotiated',
    )
  }
  return frame.subarray(HEADER_SIZE, HEADER_SIZE + length)
}
