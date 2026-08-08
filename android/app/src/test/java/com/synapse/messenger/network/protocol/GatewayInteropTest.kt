package com.synapse.messenger.network.protocol

import java.util.UUID
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicLong
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import okio.ByteString.Companion.toByteString
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test

/**
 * Wire-compatibility test against a REAL gateway.
 *
 * Every other test in this package asserts that our codec matches what we read in
 * the server's source. This one asserts that the server agrees, which is a
 * different claim: a mis-numbered protobuf field or a varint written the wrong way
 * round would satisfy a round-trip test and fail here.
 *
 * It exercises the whole handshake the app performs — HELLO/WELCOME with capability
 * negotiation, AUTH with registration, SEND with a dedup key, the streamed HISTORY
 * page, and the PING the gateway sends on its heartbeat.
 *
 * Skipped unless a gateway is running (mirroring the server's own convention of
 * skipping infra-dependent tests):
 *
 * ```bash
 * go run ./cmd/server                                   # terminal 1, in-memory
 * SYNAPSE_TEST_WS=ws://localhost:8080/ws ./gradlew test  # terminal 2
 * ```
 */
class GatewayInteropTest {

    private val url: String? = System.getenv("SYNAPSE_TEST_WS")

    @Test
    fun `handshake, auth, send and history against a live gateway`() {
        assumeTrue("set SYNAPSE_TEST_WS to run", url != null)

        val client = OkHttpClient.Builder()
            .readTimeout(0, TimeUnit.SECONDS)
            .build()
        val session = Session(client, url!!)

        try {
            // --- HELLO / WELCOME
            val welcome = session.request<Welcome>(
                MsgType.HELLO,
                Hello(
                    clientVersion = "android-test/0.1",
                    deviceId = "test-device-" + UUID.randomUUID(),
                    platform = "android",
                    caps = Cap.CLIENT_CAPS,
                ),
            )
            assertTrue("server version", welcome.serverVersion.isNotEmpty())
            assertTrue("heartbeat advertised", welcome.heartbeatMs > 0)
            assertTrue("resume offered", welcome.resumeSupported)
            // We advertise gzip but not zstd, so the agreed set must contain neither
            // more than we asked for nor a compression we cannot decode.
            assertEquals(0, welcome.caps and Cap.ZSTD)
            assertTrue(Cap.has(welcome.caps, Cap.COMPRESSION))

            // --- AUTH (register)
            val username = "androidtest" + System.currentTimeMillis()
            val auth = session.request<AuthOk>(
                MsgType.AUTH,
                Auth(username = username, password = "secret123", register = true),
            )
            assertTrue("user id assigned", auth.userId.isNotEmpty())
            assertTrue("bearer token issued", auth.token.isNotEmpty())
            assertTrue("resume token issued", auth.resumeToken.isNotEmpty())

            // --- SEND to a second account, addressed by @username
            val peer = "androidpeer" + System.currentTimeMillis()
            registerPeer(client, url!!, peer)

            val dedupKey = UUID.randomUUID().toString()
            val ack = session.request<SendAck>(
                MsgType.SEND,
                Send(chatId = "@$peer", dedupKey = dedupKey, text = "hello from the android codec"),
            )
            assertEquals(dedupKey, ack.dedupKey)
            assertTrue("chat resolved from @username", ack.chatId.isNotEmpty())
            assertTrue("server assigned a seq", ack.chatSeq > 0)
            assertTrue("server stamped a timestamp", ack.timestamp > 0)

            // Re-sending the SAME dedup key must resolve to the same message: this is
            // what makes the offline outbox safe to retry.
            val retry = session.request<SendAck>(
                MsgType.SEND,
                Send(chatId = ack.chatId, dedupKey = dedupKey, text = "hello from the android codec"),
            )
            assertTrue("retry reported as duplicate", retry.duplicate)
            assertEquals(ack.messageId, retry.messageId)

            // --- HISTORY: N NEW frames sharing our request id, then HISTORY_OK
            val page = session.stream(
                MsgType.HISTORY,
                History(chatId = ack.chatId, beforeSeq = 0, limit = 20),
                itemType = MsgType.NEW,
                endType = MsgType.HISTORY_OK,
            )
            val messages = page.items.map { it as NewMessage }
            assertEquals(1, messages.size)
            assertEquals("hello from the android codec", messages.first().text)
            assertEquals(ack.messageId, messages.first().messageId)
            val end = page.end as HistoryOk
            assertTrue("short page means done", end.done)

            // --- READ is answered only on failure; silence is success.
            session.write(MsgType.READ, Read(chatId = ack.chatId, upToChatSeq = ack.chatSeq), 0)

            // --- The gateway PINGs on its heartbeat; answering is what keeps the
            // connection alive past the idle timeout.
            val ping = session.awaitPush(MsgType.PING, timeoutMs = welcome.heartbeatMs * 2L)
            assertNotNull("gateway heartbeat received", ping)
            session.write(MsgType.PONG, null, 0)
        } finally {
            session.close()
            client.dispatcher.executorService.shutdown()
        }
    }

    /** A peer has to exist before "@name" can resolve to a chat. */
    private fun registerPeer(client: OkHttpClient, url: String, username: String) {
        val peer = Session(client, url)
        try {
            peer.request<Welcome>(
                MsgType.HELLO,
                Hello(clientVersion = "android-test/0.1", deviceId = "peer-$username", platform = "android"),
            )
            peer.request<AuthOk>(
                MsgType.AUTH,
                Auth(username = username, password = "secret123", register = true),
            )
        } finally {
            peer.close()
        }
    }

    /**
     * A minimal protocol client, deliberately independent of [com.synapse.messenger.network.SynapseGateway]:
     * it shares only the codec, so this test measures the codec rather than the
     * connection state machine wrapped around it.
     */
    private class Session(client: OkHttpClient, url: String) {
        private val frames = LinkedBlockingQueue<Envelope>()
        private val seq = AtomicLong(0)
        private val ack = AtomicLong(0)
        private val requestId = AtomicLong(0)
        private val socket: WebSocket

        init {
            val opened = LinkedBlockingQueue<Boolean>()
            socket = client.newWebSocket(
                Request.Builder().url(url).build(),
                object : WebSocketListener() {
                    override fun onOpen(webSocket: WebSocket, response: Response) {
                        opened.offer(true)
                    }

                    override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
                        val envelope = Envelope.decode(Frame.decode(bytes.toByteArray()))
                        if (envelope.seq > ack.get()) ack.set(envelope.seq)
                        frames.offer(envelope)
                    }

                    override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                        opened.offer(false)
                    }
                },
            )
            check(opened.poll(10, TimeUnit.SECONDS) == true) { "websocket did not open" }
        }

        fun write(type: Int, body: Any?, id: Long) {
            val envelope = Envelope(
                type = type,
                seq = seq.incrementAndGet(),
                ack = ack.get(),
                requestId = id,
                body = BodyCodec.encode(type, body),
            )
            check(socket.send(Frame.encode(envelope.encode()).toByteString())) { "send failed" }
        }

        inline fun <reified T> request(type: Int, body: Any?): T {
            val id = requestId.incrementAndGet()
            write(type, body, id)
            val reply = await(id) ?: error("no reply to ${MsgType.name(type)}")
            if (reply.type == MsgType.ERROR || reply.type == MsgType.AUTH_ERR) {
                val error = BodyCodec.decode(reply.type, reply.body) as? ProtocolError
                error("${MsgType.name(type)} rejected: ${error?.code} ${error?.message}")
            }
            return BodyCodec.decode(reply.type, reply.body) as T
        }

        fun stream(type: Int, body: Any?, itemType: Int, endType: Int): StreamResult {
            val id = requestId.incrementAndGet()
            write(type, body, id)
            val items = mutableListOf<Any>()
            while (true) {
                val reply = await(id) ?: error("stream for ${MsgType.name(type)} never terminated")
                val decoded = BodyCodec.decode(reply.type, reply.body)
                when (reply.type) {
                    itemType -> if (decoded != null) items += decoded
                    endType -> return StreamResult(items, decoded)
                    MsgType.ERROR -> error("stream rejected: $decoded")
                    else -> Unit
                }
            }
        }

        /** Waits for a frame correlated to [id], ignoring unsolicited pushes. */
        fun await(id: Long, timeoutMs: Long = 15_000): Envelope? {
            val deadline = System.currentTimeMillis() + timeoutMs
            while (System.currentTimeMillis() < deadline) {
                val envelope = frames.poll(500, TimeUnit.MILLISECONDS) ?: continue
                if (envelope.requestId == id) return envelope
                if (envelope.type == MsgType.PING) write(MsgType.PONG, null, 0)
            }
            return null
        }

        fun awaitPush(type: Int, timeoutMs: Long): Envelope? {
            val deadline = System.currentTimeMillis() + timeoutMs
            while (System.currentTimeMillis() < deadline) {
                val envelope = frames.poll(500, TimeUnit.MILLISECONDS) ?: continue
                if (envelope.type == type) return envelope
            }
            return null
        }

        fun close() = socket.cancel()
    }

    private class StreamResult(val items: List<Any>, val end: Any?)
}
