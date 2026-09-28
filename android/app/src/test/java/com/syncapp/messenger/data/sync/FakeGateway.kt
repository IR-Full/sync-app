package com.syncapp.messenger.data.sync

import com.syncapp.messenger.network.GatewayRequests
import com.syncapp.messenger.network.ReplyEnvelope
import com.syncapp.messenger.network.StreamReply
import com.syncapp.messenger.network.protocol.ProtocolException

/**
 * A scripted gateway.
 *
 * Records every request and answers from a queue the test fills in, so a test can
 * assert what went out — the message type, the cursor, the page size — as well as
 * what the component did with what came back. Both halves matter here: the sync
 * layer's job is largely "ask the right question and write down the answer".
 *
 * Only possible because the sync layer depends on [GatewayRequests] rather than
 * on the concrete gateway, which is `final` and takes an OkHttpClient.
 */
class FakeGateway : GatewayRequests {

    /** Every request that was made, in order. */
    data class Call(val type: Int, val body: Any?, val itemType: Int? = null)

    val calls = mutableListOf<Call>()
    val sent = mutableListOf<Call>()

    /** Replies handed out in order; the last one repeats once the queue drains. */
    private val envelopeReplies = ArrayDeque<Any?>()
    private val streamReplies = ArrayDeque<StreamReply>()

    /** Set to make every request throw instead of answering. */
    var failWith: Throwable? = null

    /**
     * Makes only the named message types fail.
     *
     * Needed because several of these components are deliberately best-effort in
     * ONE of their steps and strict in another — a chat-list sync must survive a
     * failing backfill but not a failing CHAT_LIST — and a fake that could only
     * fail everything at once could not tell those two apart.
     */
    private val failByType = mutableMapOf<Int, Throwable>()

    fun failType(type: Int, error: Throwable) {
        failByType[type] = error
    }

    /** Counts requests so a test can assert a loop stopped. */
    val requestCount get() = calls.size

    fun queueReply(body: Any?) {
        envelopeReplies.addLast(body)
    }

    fun queueStream(items: List<Any>, end: Any?) {
        streamReplies.addLast(StreamReply(items = items, end = end))
    }

    override fun send(type: Int, body: Any?) {
        sent += Call(type, body)
    }

    override suspend fun requestEnvelope(
        type: Int,
        body: Any?,
        timeoutMs: Long,
        skipReadyCheck: Boolean,
    ): ReplyEnvelope {
        calls += Call(type, body)
        throwIfConfigured(type)
        val reply = if (envelopeReplies.isEmpty()) null else envelopeReplies.removeFirst()
        return ReplyEnvelope(type = type, body = reply)
    }

    override suspend fun requestStream(
        type: Int,
        body: Any?,
        itemType: Int,
        timeoutMs: Long,
    ): StreamReply {
        calls += Call(type, body, itemType)
        throwIfConfigured(type)
        return if (streamReplies.isEmpty()) {
            StreamReply(items = emptyList(), end = null)
        } else {
            streamReplies.removeFirst()
        }
    }

    private fun throwIfConfigured(type: Int) {
        failByType[type]?.let { throw it }
        failWith?.let { throw it }
    }

    /** Convenience for the common "the gateway rejects this" case. */
    fun rejectWith(code: Int, message: String = "nope") {
        failWith = ProtocolException(code, message)
    }
}
