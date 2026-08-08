package com.synapse.messenger.network.protocol

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Envelope-layer tests, against the layout in `server/pkg/wire/envelope.go`:
 * five unsigned varints (`type, seq, ack, requestId, bodyLength`) then the body.
 */
class EnvelopeTest {

    @Test
    fun `small values encode as single-byte varints in field order`() {
        val envelope = Envelope(type = 8, seq = 3, ack = 2, requestId = 7, body = byteArrayOf(42))
        val encoded = envelope.encode()

        assertArrayEquals(byteArrayOf(8, 3, 2, 7, 1, 42), encoded)
    }

    @Test
    fun `multi-byte varints use continuation bits`() {
        // 300 = 0xAC 0x02 as an unsigned varint; message types above 127 (CHAT_CREATE
        // is 120, PUSH_TOKEN 122, and the block grows) make this path real.
        val encoded = Envelope(type = 300, seq = 0, ack = 0, requestId = 0).encode()

        assertEquals(0xAC, encoded[0].toInt() and 0xFF)
        assertEquals(0x02, encoded[1].toInt() and 0xFF)
    }

    @Test
    fun `round-trips a realistic frame`() {
        val body = ByteArray(200) { (it % 251).toByte() }
        val original = Envelope(
            type = MsgType.NEW,
            seq = 1_000_000,
            ack = 999_999,
            requestId = 12_345,
            body = body,
        )

        assertEquals(original, Envelope.decode(original.encode()))
    }

    @Test
    fun `a bodiless envelope decodes to an empty body`() {
        // PING and PONG carry no body at all.
        val decoded = Envelope.decode(Envelope(MsgType.PONG, 5, 4, 0).encode())

        assertEquals(MsgType.PONG, decoded.type)
        assertEquals(0, decoded.body.size)
    }

    @Test
    fun `rejects a declared body longer than the payload`() {
        // type=1, seq=0, ack=0, req=0, bodyLen=9 with nothing following.
        val hostile = byteArrayOf(1, 0, 0, 0, 9)
        assertTrue(runCatching { Envelope.decode(hostile) }.exceptionOrNull() is EnvelopeException)
    }

    @Test
    fun `rejects a truncated varint`() {
        // A continuation bit set on the last byte: the stream ends mid-varint.
        val hostile = byteArrayOf(0x80.toByte())
        assertTrue(runCatching { Envelope.decode(hostile) }.exceptionOrNull() is EnvelopeException)
    }
}
