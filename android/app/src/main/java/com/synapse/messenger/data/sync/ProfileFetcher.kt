package com.synapse.messenger.data.sync

import android.util.Log
import androidx.room.withTransaction
import com.synapse.messenger.database.SynapseDatabase
import com.synapse.messenger.network.SynapseGateway
import com.synapse.messenger.network.protocol.MsgType
import com.synapse.messenger.network.protocol.Profile
import com.synapse.messenger.network.protocol.ProfileGet
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Reads public profiles and files them in the local directory.
 *
 * `PROFILE_GET` takes a user id **or** `"@username"`, which makes it both the
 * profile read and the handle lookup — the one request that turns a handle a user
 * typed into an account, without the side effects the client previously needed
 * (resolving a handle used to mean adding a contact).
 *
 * Shared rather than living in the user repository, because the chat list needs it
 * too: a chat-list page names a direct chat's peer by id only, so the name and
 * avatar of every conversation come from here.
 */
@Singleton
class ProfileFetcher @Inject constructor(
    private val gateway: SynapseGateway,
    private val database: SynapseDatabase,
) {
    private val users get() = database.userDao()
    private val chats get() = database.chatDao()

    /** Fetches and stores one profile. Throws on a protocol error — callers classify. */
    suspend fun fetch(target: String): Profile {
        val profile: Profile = gateway.request(MsgType.PROFILE_GET, ProfileGet(target = target.trim()))
        store(profile)
        return profile
    }

    suspend fun store(profile: Profile) {
        if (profile.userId.isEmpty()) return
        database.withTransaction {
            users.upsert(
                userId = profile.userId,
                username = profile.username.takeIf { it.isNotEmpty() },
                displayName = profile.displayName.takeIf { it.isNotEmpty() },
                avatarRef = profile.avatarRef.takeIf { it.isNotEmpty() },
            )
            // An avatar removal cannot travel through a COALESCE upsert, and a stale
            // picture is more visibly wrong than a missing one.
            if (profile.avatarRef.isEmpty()) users.clearAvatar(profile.userId)
            // A direct chat known only by peer id can now be opened by handle, which
            // is what makes "message @bob" find the conversation that already exists.
            if (profile.username.isNotEmpty()) {
                chats.setPeerUsername(profile.userId, profile.username)
            }
        }
    }

    /**
     * Fills in the people we can only name by id. Best-effort by design: a chat list
     * that renders a numeric id is far better than one that refuses to load because
     * one profile lookup failed.
     */
    suspend fun fetchMissing(userIds: Collection<String>) {
        for (id in userIds.distinct().take(MAX_PROFILE_FETCH)) {
            if (id.isEmpty()) continue
            runCatching { fetch(id) }
                .onFailure { Log.i(TAG, "profile $id not fetched: ${it.message}") }
        }
    }

    private companion object {
        const val TAG = "ProfileFetcher"

        /**
         * A cap, because this is one request per person: a user with hundreds of
         * unnamed peers must not turn a refresh into a hundred round trips. The rest
         * are picked up by the next sync.
         */
        const val MAX_PROFILE_FETCH = 40
    }
}
