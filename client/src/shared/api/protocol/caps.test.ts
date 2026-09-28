import { describe, expect, it } from 'vitest'

import { CLIENT_CAPS, Cap, hasCap } from './caps'

/**
 * The bit values mirror `server/pkg/wire/constants.go`. They are a wire format,
 * not an enum: renumbering one would silently change what a capability MEANS to
 * an already-deployed peer, which is why the values are pinned here rather than
 * merely round-tripped.
 */
describe('Cap', () => {
  it('pins each bit to the value the server uses', () => {
    expect(Cap.COMPRESSION).toBe(1)
    expect(Cap.BATCHING).toBe(2)
    expect(Cap.RESUME).toBe(4)
    expect(Cap.SECRET_CHAT).toBe(8)
    expect(Cap.TYPING_SIGNALS).toBe(16)
    expect(Cap.ZSTD).toBe(32)
  })
})

describe('hasCap', () => {
  it('detects a set bit', () => {
    const negotiated = Cap.RESUME | Cap.TYPING_SIGNALS
    expect(hasCap(negotiated, Cap.RESUME)).toBe(true)
    expect(hasCap(negotiated, Cap.TYPING_SIGNALS)).toBe(true)
  })

  it('detects a clear bit', () => {
    const negotiated = Cap.RESUME | Cap.TYPING_SIGNALS
    expect(hasCap(negotiated, Cap.COMPRESSION)).toBe(false)
    expect(hasCap(negotiated, Cap.ZSTD)).toBe(false)
    expect(hasCap(negotiated, Cap.SECRET_CHAT)).toBe(false)
  })

  it('ignores bits it does not know', () => {
    // Forward compatibility: a newer gateway may set bits this build has never
    // heard of, and the intersection must still be readable.
    const withUnknownBits = Cap.RESUME | (1 << 20)
    expect(hasCap(withUnknownBits, Cap.RESUME)).toBe(true)
    expect(hasCap(withUnknownBits, Cap.COMPRESSION)).toBe(false)
  })

  it('reports nothing set for an empty bitset', () => {
    for (const cap of Object.values(Cap)) {
      expect(hasCap(0, cap)).toBe(false)
    }
  })
})

describe('CLIENT_CAPS', () => {
  it('advertises resume and typing', () => {
    expect(hasCap(CLIENT_CAPS, Cap.RESUME)).toBe(true)
    expect(hasCap(CLIENT_CAPS, Cap.TYPING_SIGNALS)).toBe(true)
  })

  /**
   * Compression is the one capability where advertising it wrongly BREAKS the
   * connection rather than degrading it: the gateway compresses only for peers
   * that negotiated it, and `decodeFrame` throws on a compressed frame because
   * the browser has no zstd decoder and the server's zstd uses a shared
   * dictionary we could not supply. So this assertion guards a working client,
   * not a preference.
   */
  it('advertises no compression, because inbound frames must stay raw', () => {
    expect(hasCap(CLIENT_CAPS, Cap.COMPRESSION)).toBe(false)
    expect(hasCap(CLIENT_CAPS, Cap.ZSTD)).toBe(false)
  })

  it('advertises batching, which the gateway acts on', () => {
    // The bit used to be reserved on both sides and advertised by nobody. It now
    // has a counterparty: a client that asks gets a backfill page as ONE frame
    // instead of up to a hundred NEW frames plus a terminator.
    expect(hasCap(CLIENT_CAPS, Cap.BATCHING)).toBe(true)
  })

  it('advertises nothing the client cannot honour', () => {
    // SECRET_CHAT stays off: the gateway declares the bit but no handler reads
    // it, so claiming it would be a promise with no counterparty.
    expect(hasCap(CLIENT_CAPS, Cap.SECRET_CHAT)).toBe(false)
  })
})
