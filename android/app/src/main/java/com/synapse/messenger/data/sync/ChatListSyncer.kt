package com.synapse.messenger.data.sync

import android.util.Log
import androidx.room.withTransaction
import com.synapse.messenger.database.SynapseDatabase
import com.synapse.messenger.database.entity.ChatEntity
import com.synapse.messenger.network.SynapseGateway
import com.synapse.messenger.network.protocol.ChatList
import com.synapse.messenger.network.protocol.ChatSummary
import com.synapse.messenger.network.protocol.Chats
import com.synapse.messenger.network.protocol.MsgType
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Pulls the authoritative chat list, then makes the local cache agree with it.
 *
 * `CHAT_LIST` pages by keyset over chat id and answers with summaries that carry
 * everything an entry needs — type, title, owner, the peer of a direct chat, and
 * the chat's newest `last_seq`. That last field is what makes this cheap: comparing
 * it against the newest sequence we hold says exactly which chats moved since the
 * last sync, so a refresh costs one history request per *changed* chat instead of
 * one per chat.
 */
@Singleton
class ChatListSyncer @Inject constructor(
    private val gateway: SynapseGateway,
    private val database: SynapseDatabase,
    private val history: HistoryFetcher,
    private val profiles: ProfileFetcher,
) {
    private val chats get() = database.chatDao()
    private val messages get() = database.messageDao()
    private val users get() = database.userDao()

    /** Throws on a protocol or transport failure; callers classify it. */
    suspend fun sync() {
        val summaries = fetchAllPages()

        for (summary in summaries) {
            if (summary.chatId.isEmpty()) continue
            database.withTransaction {
                chats.insertIgnore(
                    ChatEntity(
                        chatId = summary.chatId,
                        type = summary.type,
                        title = summary.title,
                        peerUserId = summary.peerId.takeIf { it.isNotEmpty() },
                        ownerId = summary.ownerId.takeIf { it.isNotEmpty() },
                        createdAt = System.currentTimeMillis(),
                    ),
                )
                chats.applySummary(
                    chatId = summary.chatId,
                    type = summary.type,
                    title = summary.title,
                    ownerId = summary.ownerId.takeIf { it.isNotEmpty() },
                    peerUserId = summary.peerId.takeIf { it.isNotEmpty() },
                )
                // Record the peer as a person before their profile arrives, so the row
                // exists to be filled in and the chat can be labelled by id meanwhile.
                if (summary.peerId.isNotEmpty()) users.upsert(userId = summary.peerId)
            }
        }

        // Backfill only what moved. A chat we have never opened has no local seq at
        // all, which is the "fresh install" case this whole message type exists for.
        for (summary in summaries) {
            val localNewest = messages.newestSeq(summary.chatId) ?: 0
            if (summary.lastSeq > localNewest) {
                runCatching { history.refreshNewest(summary.chatId) }
                    .onFailure { Log.i(TAG, "backfill of ${summary.chatId} failed: ${it.message}") }
            }
        }

        // Names and avatars for everyone we can currently only call by id.
        profiles.fetchMissing(users.idsWithoutProfile())
    }

    private suspend fun fetchAllPages(): List<ChatSummary> {
        val all = mutableListOf<ChatSummary>()
        var after = ""
        var pages = 0
        while (pages < MAX_PAGES) {
            val page: Chats = gateway.request(
                MsgType.CHAT_LIST,
                ChatList(after = after, limit = PAGE_SIZE),
            )
            all += page.chats
            pages++
            // The cursor is the last row's id; an empty one (or a short page) is the end.
            if (page.done || page.nextAfter.isEmpty()) break
            after = page.nextAfter
        }
        return all
    }

    private companion object {
        const val TAG = "ChatListSyncer"

        /** The gateway caps a page at 200 and charges the budget to the user, not the socket. */
        const val PAGE_SIZE = 100

        /** A guard against a cursor that stops advancing; 20 pages is 2000 chats. */
        const val MAX_PAGES = 20
    }
}
