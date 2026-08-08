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
  /** peer understands batched envelopes (reserved; not used yet) */
  BATCHING: 1 << 1,
  /** peer supports session resume */
  RESUME: 1 << 2,
  /** peer supports E2E secret chats */
  SECRET_CHAT: 1 << 3,
  /** peer wants typing/presence events */
  TYPING_SIGNALS: 1 << 4,
  /** peer understands zstd+dictionary frames */
  ZSTD: 1 << 5,
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
 * SECRET_CHAT is omitted because this client implements no Double Ratchet;
 * claiming the capability would invite ciphertext we cannot decrypt.
 */
export const CLIENT_CAPS = Cap.RESUME | Cap.TYPING_SIGNALS

export function hasCap(caps: number, cap: Cap): boolean {
  return (caps & cap) !== 0
}
