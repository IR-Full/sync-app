package com.synapse.messenger.network.protocol

import java.io.ByteArrayOutputStream

/**
 * The envelope — mirrors `server/pkg/wire/envelope.go`.
 *
 * Five unsigned varints followed by the body:
 * `type, seq, ack, requestId, bodyLength, body[bodyLength]`.
 *
 *  - [seq] is our own per-connection monotonic counter, starting at 1 on each
 *    new socket (a resumed session continues the SERVER's numbering, not ours).
 *  - [ack] piggybacks the highest server [seq] we have seen, which is what the
 *    gateway uses to keep the connection's liveness fresh.
 *  - [requestId] correlates a reply to a request. Unsolicited server pushes
 *    always carry 0, which is the only thing distinguishing a backfilled
 *    history message from live fanout.
 */
data class Envelope(
    val type: Int,
    val seq: Long,
    val ack: Long,
    val requestId: Long,
    val body: ByteArray = ByteArray(0),
) {
    fun encode(): ByteArray {
        val out = ByteArrayOutputStream(16 + body.size)
        writeUvarint(out, type.toLong())
        writeUvarint(out, seq)
        writeUvarint(out, ack)
        writeUvarint(out, requestId)
        writeUvarint(out, body.size.toLong())
        out.write(body)
        return out.toByteArray()
    }

    // Data classes compare ByteArray by identity, which would make two equal
    // envelopes unequal; the codec tests rely on structural equality.
    override fun equals(other: Any?): Boolean {
        if (this === other) return true
        if (other !is Envelope) return false
        return type == other.type && seq == other.seq && ack == other.ack &&
            requestId == other.requestId && body.contentEquals(other.body)
    }

    override fun hashCode(): Int {
        var result = type
        result = 31 * result + seq.hashCode()
        result = 31 * result + ack.hashCode()
        result = 31 * result + requestId.hashCode()
        result = 31 * result + body.contentHashCode()
        return result
    }

    companion object {
        fun decode(bytes: ByteArray): Envelope {
            val cursor = Cursor(bytes)
            val type = cursor.readUvarint()
            val seq = cursor.readUvarint()
            val ack = cursor.readUvarint()
            val requestId = cursor.readUvarint()
            val bodyLength = cursor.readUvarint()
            if (bodyLength < 0 || bodyLength > bytes.size - cursor.offset) {
                throw EnvelopeException("short envelope: declared body $bodyLength bytes")
            }
            val body = bytes.copyOfRange(cursor.offset, cursor.offset + bodyLength.toInt())
            return Envelope(type.toInt(), seq, ack, requestId, body)
        }

        private fun writeUvarint(out: ByteArrayOutputStream, value: Long) {
            var v = value
            while (v and 0x7FL.inv() != 0L) {
                out.write(((v and 0x7F) or 0x80).toInt())
                v = v ushr 7
            }
            out.write(v.toInt())
        }

        private class Cursor(private val bytes: ByteArray) {
            var offset = 0
                private set

            fun readUvarint(): Long {
                var result = 0L
                var shift = 0
                while (true) {
                    if (offset >= bytes.size) throw EnvelopeException("truncated varint")
                    if (shift > 63) throw EnvelopeException("varint overflows 64 bits")
                    val b = bytes[offset++].toInt() and 0xFF
                    result = result or ((b and 0x7F).toLong() shl shift)
                    if (b and 0x80 == 0) return result
                    shift += 7
                }
            }
        }
    }
}

class EnvelopeException(message: String) : Exception(message)
