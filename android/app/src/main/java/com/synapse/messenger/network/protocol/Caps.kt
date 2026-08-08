package com.synapse.messenger.network.protocol

/**
 * Capability bitset negotiated in HELLO/WELCOME — mirrors
 * `server/pkg/wire/constants.go`. The agreed set is the intersection; unknown
 * bits are ignored on both sides, so the bitset may grow. Never renumber a bit.
 */
object Cap {
    /** peer understands gzip-compressed frames */
    const val COMPRESSION = 1 shl 0

    /** peer understands batched envelopes (reserved on the server; unused) */
    const val BATCHING = 1 shl 1

    /** peer supports session resume */
    const val RESUME = 1 shl 2

    /** peer supports E2E secret chats (X3DH + Double Ratchet) */
    const val SECRET_CHAT = 1 shl 3

    /** peer wants typing/presence events */
    const val TYPING_SIGNALS = 1 shl 4

    /** peer understands zstd frames compressed against the server's shared dictionary */
    const val ZSTD = 1 shl 5

    /**
     * What this client advertises.
     *
     * COMPRESSION is advertised: the gateway gzips outbound frames over 256 bytes
     * for peers that negotiated it, which is worth real bytes on a mobile link,
     * and [Frame] gunzips inbound frames. ZSTD is not — the server compresses
     * against a shared raw dictionary we have no copy of, so a zstd frame would
     * be undecodable here.
     *
     * SECRET_CHAT is omitted because this client implements no Double Ratchet;
     * claiming it would invite ciphertext we cannot decrypt.
     */
    const val CLIENT_CAPS = COMPRESSION or RESUME or TYPING_SIGNALS

    fun has(caps: Int, cap: Int): Boolean = (caps and cap) != 0
}
