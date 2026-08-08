package com.synapse.messenger.network.protocol

import java.io.ByteArrayOutputStream
import java.util.zip.GZIPOutputStream
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Frame-layer tests.
 *
 * These assert against byte layouts taken from `server/pkg/wire/frame.go`, not
 * against our own encoder: a round-trip test would pass just as happily if both
 * sides of this client agreed on the wrong thing.
 */
class FrameTest {

    @Test
    fun `header matches the wire layout`() {
        val payload = byteArrayOf(1, 2, 3)
        val frame = Frame.encode(payload)

        assertEquals(0x53, frame[0].toInt() and 0xFF) // 'S'
        assertEquals(0x43, frame[1].toInt() and 0xFF) // 'C'
        assertEquals(1, frame[2].toInt()) // version
        assertEquals(0, frame[3].toInt()) // flags
        // Length is big-endian uint32.
        assertEquals(0, frame[4].toInt())
        assertEquals(0, frame[5].toInt())
        assertEquals(0, frame[6].toInt())
        assertEquals(3, frame[7].toInt())
        assertEquals(Frame.HEADER_SIZE + 3, frame.size)
    }

    @Test
    fun `length above one byte is encoded big-endian`() {
        val payload = ByteArray(300) { it.toByte() }
        val frame = Frame.encode(payload)

        assertEquals(1, frame[6].toInt()) // 300 = 0x012C
        assertEquals(0x2C, frame[7].toInt() and 0xFF)
        assertArrayEquals(payload, Frame.decode(frame))
    }

    @Test
    fun `decodes a gzip-compressed frame`() {
        // The gateway compresses outbound frames over 256 bytes once the client
        // advertises CapCompression, so this is a frame we will really receive.
        val payload = ByteArray(512) { 'a'.code.toByte() }
        val compressed = ByteArrayOutputStream().also { out ->
            GZIPOutputStream(out).use { it.write(payload) }
        }.toByteArray()

        val frame = ByteArray(Frame.HEADER_SIZE + compressed.size)
        frame[0] = Frame.MAGIC_0.toByte()
        frame[1] = Frame.MAGIC_1.toByte()
        frame[2] = Frame.VERSION.toByte()
        frame[3] = Frame.FLAG_COMPRESSED.toByte()
        frame[6] = (compressed.size ushr 8).toByte()
        frame[7] = compressed.size.toByte()
        compressed.copyInto(frame, Frame.HEADER_SIZE)

        assertArrayEquals(payload, Frame.decode(frame))
    }

    @Test
    fun `rejects a zstd frame we never negotiated`() {
        // The server only sets this flag for a peer that advertised CapZstd, and it
        // compresses against a shared dictionary we have no copy of — failing loudly
        // beats handing garbage to the envelope parser.
        val frame = Frame.encode(byteArrayOf(1, 2, 3)).also { it[3] = Frame.FLAG_ZSTD.toByte() }
        val error = runCatching { Frame.decode(frame) }.exceptionOrNull()
        assertTrue(error is FrameException)
    }

    @Test
    fun `rejects bad magic, version and truncation`() {
        val good = Frame.encode(byteArrayOf(9))

        val badMagic = good.copyOf().also { it[0] = 0x58 }
        assertTrue(runCatching { Frame.decode(badMagic) }.exceptionOrNull() is FrameException)

        val badVersion = good.copyOf().also { it[2] = 0x02 }
        assertTrue(runCatching { Frame.decode(badVersion) }.exceptionOrNull() is FrameException)

        val truncated = good.copyOf(good.size - 1)
        assertTrue(runCatching { Frame.decode(truncated) }.exceptionOrNull() is FrameException)
    }
}
