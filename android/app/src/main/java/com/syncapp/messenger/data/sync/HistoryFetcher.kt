package com.syncapp.messenger.data.sync

import com.syncapp.messenger.database.SyncAppDatabase
import com.syncapp.messenger.network.GatewayRequests
import com.syncapp.messenger.network.request
import com.syncapp.messenger.network.protocol.History
import com.syncapp.messenger.network.protocol.HistoryOk
import com.syncapp.messenger.network.protocol.MsgType
import com.syncapp.messenger.network.protocol.NewMessage
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Paging over HISTORY, in one place.
 *
 * The reply is a *stream*, not a list: N NEW frames sharing the request's id,
 * terminated by HISTORY_OK carrying the next cursor. That convention — and the fact
 * that history walks backwards from a cursor while the UI reads forwards — is the
 * kind of protocol detail two repositories should not each re-derive.
 */
@Singleton
class HistoryFetcher @Inject constructor(
    private val gateway: GatewayRequests,
    private val database: SyncAppDatabase,
    private val ingestor: MessageIngestor,
) {
    data class Page(val messages: List<NewMessage>, val done: Boolean, val nextBefore: Long)

    private val chats get() = database.chatDao()
    private val messages get() = database.messageDao()

    /**
     * Fetches one page. [chatRef] may be `"@username"`: the gateway resolves it,
     * creating the direct chat if it does not exist — so only call it that way on an
     * explicit user action.
     */
    suspend fun page(chatRef: String, beforeSeq: Long, limit: Int): Page {
        val reply = gateway.requestStream(
            type = MsgType.HISTORY,
            body = History(chatId = chatRef, beforeSeq = beforeSeq, limit = limit),
            itemType = MsgType.NEW,
        )
        val items = reply.items.filterIsInstance<NewMessage>().sortedBy { it.chatSeq }
        val end = reply.end as? HistoryOk
        return Page(
            messages = items,
            // A short page means the server had nothing more to give.
            done = end?.done ?: true,
            nextBefore = end?.nextBefore ?: 0,
        )
    }

    /** Pulls the newest page of a chat and files it. */
    suspend fun refreshNewest(chatId: String, limit: Int = DEFAULT_PAGE) {
        val page = page(chatId, beforeSeq = 0, limit = limit)
        ingestor.ingestAll(page.messages)
        val oldest = messages.oldestSeq(chatId) ?: 0
        // A SHORT newest page is the informative one: the server returned fewer
        // rows than asked for, so this chat has no history older than what we now
        // hold. A full page means there is more below it.
        //
        // The previous expression, `!page.done || chat.hasMoreHistory`, had these
        // two branches the wrong way round: on a short page it kept the existing
        // assumption — `true` by default — so a small chat went on advertising
        // "load older" forever, and every attempt spent a round trip to be told
        // there was nothing.
        val chat = chats.findById(chatId)
        if (chat != null && page.messages.isNotEmpty()) {
            chats.updateHistoryCursor(chatId, oldest, hasMore = !page.done)
        }
    }

    /**
     * Pulls one page older than what we hold. Returns whether more remain.
     *
     * Backfill must not reorder the chat list: an older page is not new activity, so
     * the chat's "last message" is left alone.
     */
    suspend fun loadOlder(chatId: String, limit: Int = DEFAULT_PAGE): Boolean {
        val chat = chats.findById(chatId) ?: return false
        val before = chat.oldestLoadedSeq.takeIf { it > 0 } ?: messages.oldestSeq(chatId) ?: 0
        val page = page(chatId, beforeSeq = before, limit = limit)
        ingestor.ingestAll(page.messages, bumpChat = false)
        val oldest = messages.oldestSeq(chatId) ?: before
        val hasMore = !page.done && page.messages.isNotEmpty()
        chats.updateHistoryCursor(chatId, oldest, hasMore)
        return hasMore
    }

    companion object {
        const val DEFAULT_PAGE = 50
    }
}
