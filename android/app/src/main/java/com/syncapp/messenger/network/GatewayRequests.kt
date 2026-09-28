package com.syncapp.messenger.network

import com.syncapp.messenger.network.protocol.MsgType

/**
 * The request/response surface of the gateway, as the sync layer sees it.
 *
 * [SyncAppGateway] owns a great deal more — a socket, a reconnect loop, a
 * heartbeat watchdog, a push channel — and none of that is interesting to a
 * component whose job is "ask for a page of history and file it". Depending on
 * the whole class also made those components untestable: it is `final` (Kotlin's
 * default) and takes an `OkHttpClient` in its constructor, so there was no way to
 * exercise `HistoryFetcher` or `ChatListSyncer` without a real HTTP stack.
 *
 * The same separation already exists on the server, where `message.Chats` and
 * `fanout.Chats` are narrow interfaces rather than the concrete services — for
 * the same two reasons: the caller states what it needs, and a test can supply it.
 */
interface GatewayRequests {

    /**
     * Fire-and-forget. For the types the gateway answers only on failure —
     * READ, TYPING, EDIT, DELETE — a silent success is the protocol's design.
     */
    fun send(type: Int, body: Any?)

    /**
     * Sends a request and waits for its correlated reply. ERROR frames throw
     * [ProtocolException]; any other correlated type resolves, so a caller that
     * accepts several reply shapes can inspect [ReplyEnvelope.type].
     */
    suspend fun requestEnvelope(
        type: Int,
        body: Any?,
        timeoutMs: Long = SyncAppGateway.REQUEST_TIMEOUT_MS,
        skipReadyCheck: Boolean = false,
    ): ReplyEnvelope

    /**
     * Sends a request whose reply is a page: N frames of [itemType] followed by a
     * terminator, all sharing our requestId.
     *
     * This is how HISTORY works — the gateway replays stored messages as ordinary
     * NEW frames so a client's normal ingest path handles them, then closes the
     * page with HISTORY_OK carrying the next cursor.
     */
    suspend fun requestStream(
        type: Int,
        body: Any?,
        itemType: Int,
        timeoutMs: Long = SyncAppGateway.REQUEST_TIMEOUT_MS,
    ): StreamReply
}

/**
 * [GatewayRequests.requestEnvelope] with the reply body cast to the expected shape.
 *
 * An extension rather than an interface member because it is `inline reified`,
 * which Kotlin does not permit on an interface. Call sites are unaffected —
 * `gateway.request<Chats>(...)` resolves to this either way — and any
 * implementation of the interface, including a test double, gets it for free.
 */
suspend inline fun <reified T> GatewayRequests.request(
    type: Int,
    body: Any?,
    timeoutMs: Long = SyncAppGateway.REQUEST_TIMEOUT_MS,
): T {
    // The handshake runs before the connection is READY by definition, so those
    // three types are exempt from the readiness gate.
    val handshake = type == MsgType.HELLO || type == MsgType.AUTH || type == MsgType.RESUME
    val reply = requestEnvelope(type, body, timeoutMs, skipReadyCheck = handshake)
    return reply.body as? T
        ?: throw UnexpectedReplyException(type, reply.type)
}
