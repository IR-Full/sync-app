package com.synapse.messenger.network.protocol

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Body-codec tests.
 *
 * The bodies are protobuf per `server/proto/synapse/v1/body.proto`, so the things
 * worth asserting are the two proto3 rules a hand-mapped schema can silently get
 * wrong: default values are omitted on the wire, and missing fields decode to
 * defaults rather than failing.
 */
class BodyCodecTest {

    @Test
    fun `field numbers match the schema`() {
        // Auth.username is field 2, so a username-only body must be exactly
        // tag(2, LEN)=0x12, len, bytes.
        val encoded = BodyCodec.encode(MsgType.AUTH, Auth(username = "bob"))

        assertEquals(0x12, encoded[0].toInt() and 0xFF)
        assertEquals(3, encoded[1].toInt())
        assertEquals("bob", String(encoded.copyOfRange(2, 5)))
    }

    @Test
    fun `proto3 defaults are not written`() {
        // An all-defaults body must encode to zero bytes: the Go decoder walks every
        // field we send, and empty optionals are exactly what proto3 omits.
        assertEquals(0, BodyCodec.encode(MsgType.AUTH, Auth()).size)
        assertEquals(0, BodyCodec.encode(MsgType.TYPING, Typing()).size)
    }

    @Test
    fun `missing fields decode to defaults`() {
        // What the server actually sends: a WELCOME that omits every zero-valued
        // field. Every property must have a default or this throws.
        val welcome = BodyCodec.decode(MsgType.WELCOME, ByteArray(0))
        assertNull(welcome) // an empty payload is "no body", not an empty message

        val partial = BodyCodec.encode(MsgType.WELCOME, Welcome(heartbeatMs = 20_000))
        val decoded = BodyCodec.decode(MsgType.WELCOME, partial) as Welcome

        assertEquals(20_000, decoded.heartbeatMs)
        assertEquals("", decoded.sessionId)
        assertFalse(decoded.resumeSupported)
    }

    @Test
    fun `round-trips a message with a nested attachment`() {
        val original = NewMessage(
            messageId = "7213894612345",
            chatId = "812",
            senderId = "5511",
            chatSeq = 42,
            text = "hello",
            timestamp = 1_700_000_000_000,
            attachment = Attachment(
                kind = "image",
                mediaRef = "m123-abc",
                filename = "photo.jpg",
                mime = "image/jpeg",
                size = 2048,
                width = 800,
                height = 600,
            ),
            forward = ForwardOrigin(chatId = "99", messageId = "100", senderId = "101"),
            expiresAt = 1_700_000_060_000,
        )

        val decoded = BodyCodec.decode(MsgType.NEW, BodyCodec.encode(MsgType.NEW, original))

        assertEquals(original, decoded)
    }

    @Test
    fun `uint64 sequences survive large values`() {
        // chat_seq is uint64 on the wire and Long here; both are varints, and the
        // values in play are nowhere near the sign boundary.
        val ack = SendAck(chatSeq = 9_007_199_254_740_993L, timestamp = 1_700_000_000_000)
        val decoded = BodyCodec.decode(MsgType.SEND_ACK, BodyCodec.encode(MsgType.SEND_ACK, ack))

        assertEquals(ack, decoded)
    }

    @Test
    fun `bodiless types are known to carry nothing`() {
        assertFalse(BodyCodec.hasBody(MsgType.PING))
        assertFalse(BodyCodec.hasBody(MsgType.PONG))
        assertFalse(BodyCodec.hasBody(MsgType.T_ACK))
        assertTrue(BodyCodec.hasBody(MsgType.NEW))
    }

    @Test
    fun `auth error shares the error body shape`() {
        // The gateway answers a rejected AUTH with AUTH_ERR carrying an Error body,
        // not a type of its own.
        val error = ProtocolError(code = ErrorCode.UNAUTHENTICATED, message = "authentication failed")
        val decoded = BodyCodec.decode(MsgType.AUTH_ERR, BodyCodec.encode(MsgType.AUTH_ERR, error))

        assertEquals(error, decoded)
    }
}
