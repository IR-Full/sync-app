package com.synapse.messenger.network.protocol

import java.util.UUID
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicLong
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import okio.ByteString.Companion.toByteString
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
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

            // --- AUTH (register), with the display name the gateway honours only here
            val username = "androidtest" + System.currentTimeMillis()
            val auth = session.request<AuthOk>(
                MsgType.AUTH,
                Auth(
                    username = username,
                    password = "secret123",
                    register = true,
                    displayName = "Android Codec",
                ),
            )
            assertTrue("user id assigned", auth.userId.isNotEmpty())
            assertTrue("bearer token issued", auth.token.isNotEmpty())
            assertTrue("resume token issued", auth.resumeToken.isNotEmpty())
            // AUTH_OK carries the identity itself, which is what makes a token login on
            // a later launch self-sufficient.
            assertEquals(username, auth.username)
            assertEquals("Android Codec", auth.displayName)

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
            val historyPage = session.stream(
                MsgType.HISTORY,
                History(chatId = ack.chatId, beforeSeq = 0, limit = 20),
                itemType = MsgType.NEW,
                endType = MsgType.HISTORY_OK,
            )
            val messages = historyPage.items.map { it as NewMessage }
            assertEquals(1, messages.size)
            assertEquals("hello from the android codec", messages.first().text)
            assertEquals(ack.messageId, messages.first().messageId)
            val end = historyPage.end as HistoryOk
            assertTrue("short page means done", end.done)

            // --- READ is answered only on failure; silence is success.
            session.write(MsgType.READ, Read(chatId = ack.chatId, upToChatSeq = ack.chatSeq), 0)

            // --- CHAT_LIST: the enumeration a fresh install depends on. The chat we
            // just created must be in it, described well enough to render a row.
            val chatPage = session.request<Chats>(MsgType.CHAT_LIST, ChatList(limit = 50))
            val row = chatPage.chats.firstOrNull { it.chatId == ack.chatId }
            assertNotNull("the new chat appears in CHAT_LIST", row)
            assertEquals("direct", row!!.type)
            assertEquals("owner is the caller", auth.userId, row.ownerId)
            // peer_id is what finally gives a 1:1 chat something to be named after.
            assertTrue("peer id filled for a direct chat", row.peerId.isNotEmpty())
            assertTrue("last_seq tells us what to backfill", row.lastSeq >= ack.chatSeq)
            assertTrue("my_role reported", row.myRole.isNotEmpty())
            assertTrue("a single page is the last one", chatPage.done)

            // --- PROFILE_GET by handle: the user lookup, with no side effects.
            val peerProfile = session.request<Profile>(MsgType.PROFILE_GET, ProfileGet(target = "@$peer"))
            assertEquals(peer, peerProfile.username)
            assertEquals(row.peerId, peerProfile.userId)

            // An empty target means "me".
            val me = session.request<Profile>(MsgType.PROFILE_GET, ProfileGet())
            assertEquals(auth.userId, me.userId)
            assertEquals("Android Codec", me.displayName)

            // --- PROFILE_SET: our own name, and an avatar through the media pipeline.
            val renamed = session.request<Profile>(
                MsgType.PROFILE_SET,
                ProfileSet(displayName = "Renamed From Android"),
            )
            assertEquals("Renamed From Android", renamed.displayName)
            // Empty fields mean "leave as is", so the username must survive a name-only set.
            assertEquals(username, renamed.username)

            val ticket = session.request<MediaTicket>(
                MsgType.MEDIA_INIT,
                MediaInit(filename = "avatar.jpg", contentType = "image/jpeg", size = AVATAR.size.toLong()),
            )
            uploadAvatar(client, ticket.uploadUrl)
            val withAvatar = session.request<Profile>(
                MsgType.PROFILE_SET,
                ProfileSet(avatarRef = ticket.mediaRef),
            )
            assertEquals(ticket.mediaRef, withAvatar.avatarRef)
            assertEquals("a name-less set leaves the name alone", "Renamed From Android", withAvatar.displayName)

            // Clearing is a flag, because proto3 cannot tell an absent ref from an empty one.
            val cleared = session.request<Profile>(MsgType.PROFILE_SET, ProfileSet(clearAvatar = true))
            assertEquals("", cleared.avatarRef)

            // The avatar ref resolves to a signed URL like any other media.
            val url = session.request<MediaUrl>(MsgType.MEDIA_FETCH, MediaFetch(mediaRef = ticket.mediaRef))
            assertTrue("signed download URL", url.downloadUrl.startsWith("http"))

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

    /**
     * A delivery receipt reaches the sender once the recipient's device really has the
     * message — the step between "durably stored" and "read", which had no source at
     * all until the gateway started reporting its own writes.
     */
    @Test
    fun `a message written to a recipient socket is reported back to the sender`() {
        assumeTrue("set SYNAPSE_TEST_WS to run", url != null)

        val client = OkHttpClient.Builder().readTimeout(0, TimeUnit.SECONDS).build()
        val sender = Session(client, url!!)
        var peer: Session? = null
        try {
            sender.request<Welcome>(
                MsgType.HELLO,
                Hello(
                    clientVersion = "android-test/0.1",
                    deviceId = "sender-" + UUID.randomUUID(),
                    platform = "android",
                ),
            )
            val me = sender.request<AuthOk>(
                MsgType.AUTH,
                Auth(
                    username = "androidsend" + System.currentTimeMillis(),
                    password = "secret123",
                    register = true,
                ),
            )

            // The recipient must be CONNECTED: an offline one gets a push notification,
            // not a delivery receipt, and that difference is the whole point of the signal.
            val peerName = "androidrecv" + System.currentTimeMillis()
            peer = connectPeer(client, url!!, peerName, register = true)

            val ack = sender.request<SendAck>(
                MsgType.SEND,
                Send(
                    chatId = "@" + peerName,
                    dedupKey = UUID.randomUUID().toString(),
                    text = "are you there",
                ),
            )

            val frame = sender.awaitPush(MsgType.DELIVERED, timeoutMs = 15_000)
            assertNotNull("no DELIVERED frame arrived for a connected recipient", frame)
            val receipt = BodyCodec.decode(MsgType.DELIVERED, frame!!.body) as ReadUpdate
            assertEquals(ack.chatId, receipt.chatId)
            // The receipt names WHO received it, which is what makes it usable in a group.
            assertTrue("receipt names a recipient", receipt.userId.isNotEmpty())
            assertTrue("receipt is not about the sender", receipt.userId != me.userId)
            // It is a cursor, not a message id: a client keeps the maximum it has seen.
            assertTrue(
                "cursor covers the message (" + receipt.upToChatSeq + " vs " + ack.chatSeq + ")",
                receipt.upToChatSeq >= ack.chatSeq,
            )
        } finally {
            peer?.close()
            sender.close()
            client.dispatcher.executorService.shutdown()
        }
    }

    /**
     * Presence reaches the peer of a direct chat.
     *
     * The audience is the design decision being pinned here: online state goes to the
     * people a user has 1:1 chats with, so it only travels once such a chat exists —
     * which the first message creates.
     */
    @Test
    fun `presence transitions reach a direct chat peer`() {
        assumeTrue("set SYNAPSE_TEST_WS to run", url != null)

        val client = OkHttpClient.Builder().readTimeout(0, TimeUnit.SECONDS).build()
        val watcher = Session(client, url!!)
        var peer: Session? = null
        try {
            watcher.request<Welcome>(
                MsgType.HELLO,
                Hello(
                    clientVersion = "android-test/0.1",
                    deviceId = "watcher-" + UUID.randomUUID(),
                    platform = "android",
                ),
            )
            watcher.request<AuthOk>(
                MsgType.AUTH,
                Auth(
                    username = "androidwatch" + System.currentTimeMillis(),
                    password = "secret123",
                    register = true,
                ),
            )

            // Create the direct chat that makes the watcher an interested party.
            val peerName = "androidseen" + System.currentTimeMillis()
            registerPeer(client, url!!, peerName)
            watcher.request<SendAck>(
                MsgType.SEND,
                Send(
                    chatId = "@" + peerName,
                    dedupKey = UUID.randomUUID().toString(),
                    text = "hello",
                ),
            )

            // Now the peer connects: the online transition must reach the watcher.
            peer = connectPeer(client, url!!, peerName, register = false)
            val online = watcher.awaitPush(MsgType.PRESENCE, timeoutMs = 15_000)
            assertNotNull("no PRESENCE frame arrived for a direct-chat peer", online)
            val onlineBody = BodyCodec.decode(MsgType.PRESENCE, online!!.body) as Presence
            assertTrue("presence reports the peer online", onlineBody.online)
            assertTrue("presence names the peer", onlineBody.userId.isNotEmpty())

            // And the offline transition when they go away.
            peer.close()
            peer = null
            val offline = watcher.awaitPush(MsgType.PRESENCE, timeoutMs = 20_000)
            assertNotNull("no PRESENCE frame arrived when the peer disconnected", offline)
            val offlineBody = BodyCodec.decode(MsgType.PRESENCE, offline!!.body) as Presence
            assertEquals(onlineBody.userId, offlineBody.userId)
            assertFalse("presence reports the peer offline", offlineBody.online)
            assertTrue("offline carries a last-seen stamp", offlineBody.lastSeenMs > 0)
        } finally {
            peer?.close()
            watcher.close()
            client.dispatcher.executorService.shutdown()
        }
    }

    /**
     * PUTs the avatar bytes to the signed URL. The upload handler holds the body to
     * exactly the size that was signed, so this sends the same array MEDIA_INIT declared.
     */
    private fun uploadAvatar(client: OkHttpClient, uploadUrl: String) {
        val request = Request.Builder()
            .url(uploadUrl)
            .put(AVATAR.toRequestBody("image/jpeg".toMediaType()))
            .build()
        client.newCall(request).execute().use { response ->
            assertTrue("avatar upload accepted: ${response.code}", response.isSuccessful)
        }
    }

    /** A peer has to exist before "@name" can resolve to a chat. */
    private fun registerPeer(client: OkHttpClient, url: String, username: String) {
        connectPeer(client, url, username, register = true).close()
    }

    /**
     * Opens an authenticated session for another account and leaves it open.
     *
     * Delivery receipts and presence are only observable while the other side is
     * really connected: one is raised by the gateway that writes to their socket, the
     * other by their connection coming and going.
     */
    private fun connectPeer(
        client: OkHttpClient,
        url: String,
        username: String,
        register: Boolean,
    ): Session {
        val peer = Session(client, url)
        peer.request<Welcome>(
            MsgType.HELLO,
            Hello(
                clientVersion = "android-test/0.1",
                deviceId = "peer-$username",
                platform = "android",
            ),
        )
        peer.request<AuthOk>(
            MsgType.AUTH,
            Auth(username = username, password = "secret123", register = register),
        )
        return peer
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

    private companion object {
        /** Any bytes will do: the media service stores blobs, it does not decode them. */
        val AVATAR = ByteArray(64) { it.toByte() }
    }
}
