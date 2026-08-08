package com.synapse.messenger.network.protocol

import java.io.ByteArrayOutputStream
import java.util.zip.GZIPInputStream

/**
 * Frame codec — mirrors `server/pkg/wire/frame.go`.
 *
 * ```
 *  +--------+--------+--------+--------+--------------------+==================+
 *  | 0x53   | 0x43   | VER(1) | FLAGS  | LENGTH (4, BE)     | PAYLOAD (LENGTH) |
 *  |  'S'   |  'C'   |  0x01  | bits   | uint32 <= 16 MiB   | envelope bytes   |
 *  +--------+--------+--------+--------+--------------------+==================+
 * ```
 *
 * On the WebSocket transport one binary message carries exactly one frame, so
 * there is no stream re-assembly here — [decode] takes a whole message.
 *
 * Outbound frames are never compressed. The gateway does not require symmetry
 * (flags are per frame), and a phone's uplink carries short frames where gzip
 * would cost CPU for nothing. Inbound frames may be gzipped because we advertise
 * [Cap.COMPRESSION]; zstd cannot appear because we do not advertise it.
 */
object Frame {
    const val MAGIC_0 = 0x53 // 'S'
    const val MAGIC_1 = 0x43 // 'C'
    const val VERSION = 0x01
    const val HEADER_SIZE = 8

    /** Matches the server's parser bound; a larger length prefix is hostile input. */
    const val MAX_PAYLOAD_SIZE = 16 shl 20

    const val FLAG_NONE = 0
    const val FLAG_COMPRESSED = 1 shl 0 // gzip
    const val FLAG_ZSTD = 1 shl 1 // zstd + shared dictionary

    fun encode(payload: ByteArray, flags: Int = FLAG_NONE): ByteArray {
        require(payload.size <= MAX_PAYLOAD_SIZE) { "payload too large: ${payload.size}" }
        val frame = ByteArray(HEADER_SIZE + payload.size)
        frame[0] = MAGIC_0.toByte()
        frame[1] = MAGIC_1.toByte()
        frame[2] = VERSION.toByte()
        frame[3] = flags.toByte()
        val n = payload.size
        frame[4] = (n ushr 24).toByte()
        frame[5] = (n ushr 16).toByte()
        frame[6] = (n ushr 8).toByte()
        frame[7] = n.toByte()
        payload.copyInto(frame, HEADER_SIZE)
        return frame
    }

    /** Unwraps one complete frame and returns its (decompressed) envelope payload. */
    fun decode(frame: ByteArray): ByteArray {
        if (frame.size < HEADER_SIZE) throw FrameException("short frame: ${frame.size} bytes")
        if (frame[0].toInt() and 0xFF != MAGIC_0 || frame[1].toInt() and 0xFF != MAGIC_1) {
            throw FrameException("bad magic")
        }
        val version = frame[2].toInt() and 0xFF
        if (version != VERSION) throw FrameException("unsupported framing version $version, want $VERSION")

        val flags = frame[3].toInt() and 0xFF
        val length = ((frame[4].toInt() and 0xFF) shl 24) or
            ((frame[5].toInt() and 0xFF) shl 16) or
            ((frame[6].toInt() and 0xFF) shl 8) or
            (frame[7].toInt() and 0xFF)
        if (length < 0 || length > MAX_PAYLOAD_SIZE) throw FrameException("payload too large: $length")
        if (frame.size < HEADER_SIZE + length) throw FrameException("truncated frame")

        val payload = frame.copyOfRange(HEADER_SIZE, HEADER_SIZE + length)
        return when {
            flags and FLAG_ZSTD != 0 -> throw FrameException(
                "zstd frame received but CapZstd was never advertised",
            )
            flags and FLAG_COMPRESSED != 0 -> gunzip(payload)
            else -> payload
        }
    }

    private fun gunzip(compressed: ByteArray): ByteArray {
        val out = ByteArrayOutputStream(compressed.size * 3)
        GZIPInputStream(compressed.inputStream()).use { gz ->
            val buf = ByteArray(8 * 1024)
            var total = 0
            while (true) {
                val read = gz.read(buf)
                if (read <= 0) break
                total += read
                // Bound the output the same way the server does: a gzip bomb must not
                // become an OOM on a phone.
                if (total > MAX_PAYLOAD_SIZE) throw FrameException("decompressed payload too large")
                out.write(buf, 0, read)
            }
        }
        return out.toByteArray()
    }
}

class FrameException(message: String) : Exception(message)
