/**
 * Capability bitset negotiated in HELLO/WELCOME — mirrors
 * `server/pkg/wire/constants.go`.
 *
 * The agreed set is the intersection of what we advertise and what the gateway
 * supports; unknown bits are ignored on both sides, so the bitset can grow
 * without breaking either peer. Never renumber a bit — only append.
 */
export const Cap = {
  /** peer understands gzip-compressed frames */
  COMPRESSION: 1 << 0,
  /** peer understands a whole history page in one frame (HISTORY_PAGE) */
  BATCHING: 1 << 1,
  /** peer supports session resume */
  RESUME: 1 << 2,
  /** peer supports E2E secret chats */
  SECRET_CHAT: 1 << 3,
  /** peer wants typing/presence events */
  TYPING_SIGNALS: 1 << 4,
  /** peer understands zstd+dictionary frames */
  ZSTD: 1 << 5,
  /**
   * peer speaks the DURABLE secret-chat protocol: it acts on SECRET_ACK, asks for
   * what it missed with SECRET_SYNC, and confirms with SECRET_ACKED.
   *
   * It also selects the BINARY ratchet payload. One bit for both because they
   * shipped together in the same client release, and a separate bit for the
   * encoding would have described a client that never existed.
   */
  SECRET_QUEUE: 1 << 6,
} as const

export type Cap = (typeof Cap)[keyof typeof Cap]

/**
 * What this web client advertises.
 *
 * Compression is deliberately NOT advertised. The gateway compresses an
 * outbound frame only when the peer negotiated a compression capability, so
 * omitting both bits guarantees every frame we receive is raw — which matters
 * because the browser has no zstd decoder (`DecompressionStream` supports
 * gzip/deflate only, and the server's zstd path uses a shared dictionary we
 * could not supply anyway). Advertising CapCompression alone would work via
 * `DecompressionStream('gzip')`, but it buys little for chat-sized frames and
 * costs an async hop on every inbound frame.
 *
 * SECRET_CHAT is not advertised either, and that one needs explaining because
 * this client DOES implement the Double Ratchet (`shared/lib/e2e`, verified
 * against the server's own crypto by `npm run test:secret`). The gateway
 * declares `CapSecretChat` but never reads it: no handler gates on the bit, so
 * `KEY_PUBLISH` and `SECRET_SEND` are served regardless. Advertising it would
 * therefore change nothing today — and claiming a capability the peer does not
 * check is a promise with no counterparty. If the gateway ever starts enforcing
 * it, this is the line to change.
 */
/**
 * BATCHING is advertised because the gateway now acts on it: a client that asks
 * gets a backfill page as ONE frame instead of up to a hundred NEW frames plus
 * a terminator. Unlike SECRET_CHAT below, this bit has a counterparty — omitting
 * it means the server keeps streaming, which is still correct but a hundred
 * times more frames for the same page.
 */
/**
 * SECRET_QUEUE is advertised, and unlike SECRET_CHAT above this bit HAS a
 * counterparty: the gateway negotiates it, gates SECRET_ACK on it, and uses it to
 * decide whether to send the ratchet payload as raw bytes or as base64. Omitting
 * it would mean no delivery receipts for secret messages, no way to collect what
 * arrived while the tab was closed, and a third more bytes on every one.
 */
export const CLIENT_CAPS = Cap.RESUME | Cap.TYPING_SIGNALS | Cap.BATCHING | Cap.SECRET_QUEUE

export function hasCap(caps: number, cap: Cap): boolean {
  return (caps & cap) !== 0
}
