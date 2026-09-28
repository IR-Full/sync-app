package com.syncapp.messenger.data.sync

import android.util.Log
import androidx.room.withTransaction
import com.syncapp.messenger.database.SyncAppDatabase
import com.syncapp.messenger.database.entity.ChatEntity
import com.syncapp.messenger.network.GatewayRequests
import com.syncapp.messenger.network.request
import com.syncapp.messenger.network.protocol.ChatList
import com.syncapp.messenger.network.protocol.ChatSummary
import com.syncapp.messenger.network.protocol.Chats
import com.syncapp.messenger.network.protocol.MsgType
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Pulls the authoritative chat list, then makes the local cache agree with it.
 *
 * `CHAT_LIST` pages by keyset over (last_activity_at, chat_id) — both halves, because
 * the list is ordered by activity and a cursor naming only an id skips and repeats rows
 * as messages arrive. The summaries carry everything an entry needs: type, title, owner,
 * the peer of a two-party chat, this account's own mute/pin/archive flags, the newest
 * message itself, and the chat's `last_seq`.
 *
 * That last field is what makes this cheap. Comparing it against the newest sequence held
 * locally says exactly which chats moved since the last sync, so a refresh costs one
 * history request per *changed* chat instead of one per chat — and now that the newest
 * message rides along in the summary, an unchanged chat costs nothing at all.
 */
@Singleton
class ChatListSyncer @Inject constructor(
    private val gateway: GatewayRequests,
    private val database: SyncAppDatabase,
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
                    // The flags are the SERVER's now: it keeps them per member and sends
                    // them with every enumeration, so they overwrite rather than merge.
                    // Keeping the cached copy would silently revert a mute or an archive
                    // performed on another device.
                    mutedUntil = summary.mutedUntil,
                    pinned = summary.pinned,
                    archived = summary.archived,
                    lastActivityAt = summary.lastActivityAt,
                )
                // The preview, from the summary rather than from a per-chat HISTORY call.
                // It is guarded by the same `lastMessageSeq <= :seq` condition as every
                // other writer, so an enumeration arriving after a newer live message
                // does not roll the row back.
                summary.lastMessage?.let { newest ->
                    chats.updateLastMessage(
                        chatId = summary.chatId,
                        text = newest.text,
                        senderId = newest.senderId,
                        seq = newest.chatSeq,
                        timestamp = newest.timestamp,
                    )
                }
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
        // The other half of the cursor. The list is ordered by ACTIVITY, which reorders as
        // messages arrive, so a cursor naming only a chat id skips and repeats rows exactly
        // when the account is busy — which is when somebody is most likely to be looking.
        var afterActivity = 0L
        var pages = 0
        while (pages < MAX_PAGES) {
            val page: Chats = gateway.request(
                MsgType.CHAT_LIST,
                ChatList(
                    after = after,
                    limit = PAGE_SIZE,
                    afterActivity = afterActivity,
                    // Archived chats are still chats this cache has to know about. Omitting
                    // them would leave the archive empty on a fresh install — and
                    // un-unarchivable, since nothing local would name the row.
                    includeArchived = true,
                ),
            )
            all += page.chats
            pages++
            // The cursor is the last row's id; an empty one (or a short page) is the
            // end. A cursor that does NOT advance is the third ending, and the one
            // worth naming: without it a server handing back the same value would
            // cost MAX_PAGES identical round trips on every connect — bounded, but
            // twenty requests that learn nothing and twenty charges against the
            // user's rate limit. The web client has always guarded this.
            if (page.done || page.nextAfter.isEmpty() || page.nextAfter == after) break
            after = page.nextAfter
            afterActivity = page.nextAfterActivity
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
