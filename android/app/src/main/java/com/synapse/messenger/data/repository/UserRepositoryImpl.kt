package com.synapse.messenger.data.repository

import com.synapse.messenger.core.Outcome
import com.synapse.messenger.core.runOutcome
import com.synapse.messenger.data.SessionHolder
import com.synapse.messenger.data.mapper.toDomain
import com.synapse.messenger.data.sync.PresenceTracker
import com.synapse.messenger.data.sync.ProfileFetcher
import com.synapse.messenger.database.SynapseDatabase
import com.synapse.messenger.domain.model.UserPresence
import com.synapse.messenger.domain.model.UserSummary
import com.synapse.messenger.domain.repository.UserRepository
import com.synapse.messenger.network.SynapseGateway
import com.synapse.messenger.network.protocol.ContactList
import com.synapse.messenger.network.protocol.ContactSync
import com.synapse.messenger.network.protocol.MsgType
import com.synapse.messenger.network.protocol.Profile
import com.synapse.messenger.network.protocol.ProfileSet
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

@Singleton
class UserRepositoryImpl @Inject constructor(
    private val gateway: SynapseGateway,
    private val database: SynapseDatabase,
    private val profiles: ProfileFetcher,
    private val presenceTracker: PresenceTracker,
    private val sessionHolder: SessionHolder,
) : UserRepository {

    private val users get() = database.userDao()

    override fun observeKnownUsers(): Flow<List<UserSummary>> =
        users.observeAll().map { rows -> rows.map { it.toDomain() } }

    override fun observeUser(userId: String): Flow<UserSummary?> =
        users.observeById(userId).map { it?.toDomain() }

    override fun observePresence(userId: String): Flow<UserPresence?> =
        presenceTracker.observe(userId)

    /**
     * Looks a person up by handle, or by id.
     *
     * `PROFILE_GET` accepts `"@username"`, so this is the lookup *and* the profile
     * read — and unlike the contact-add it used to be smuggled through, it has no
     * side effect: finding someone no longer files them in your address book. A
     * handle nobody holds comes back NOT_FOUND, which is how a typed one is validated.
     *
     * A block cuts both ways here: the gateway refuses the lookup in either
     * direction, so a blocked account cannot be found and yours cannot be inspected
     * by one you blocked.
     */
    override suspend fun fetchProfile(target: String): Outcome<UserSummary> = runOutcome {
        val trimmed = target.trim()
        require(trimmed.isNotEmpty()) { "empty profile target" }
        val normalized = if (trimmed.startsWith("@")) {
            "@" + trimmed.removePrefix("@").lowercase()
        } else {
            trimmed
        }
        profiles.fetch(normalized).toDomain()
    }

    /**
     * Publishes our own profile.
     *
     * Empty fields mean "leave as is" server-side — proto3 cannot distinguish absent
     * from empty — which is why removing the avatar is a flag rather than an empty
     * ref. The reply is the stored profile, so the local row is written from what the
     * server accepted rather than from what we asked for.
     */
    override suspend fun updateMyProfile(
        displayName: String?,
        avatarRef: String?,
        clearAvatar: Boolean,
    ): Outcome<UserSummary> = runOutcome {
        val updated: Profile = gateway.request(
            MsgType.PROFILE_SET,
            ProfileSet(
                displayName = displayName?.trim().orEmpty(),
                avatarRef = avatarRef.orEmpty(),
                clearAvatar = clearAvatar,
            ),
        )
        profiles.store(updated)
        updated.toDomain()
    }

    /**
     * Full contact sync.
     *
     * CONTACT_SYNC supports an incremental cursor, but this client asks for
     * everything: an address book is small, and a stale cursor after a reinstall
     * would silently hide contacts forever — the wrong trade for the bytes saved.
     *
     * What it contributes is the *private* label we gave someone; their public name
     * and avatar come from their profile, and neither writer may overwrite the other.
     */
    override suspend fun syncContacts(): Outcome<Unit> = runOutcome {
        val reply: ContactList = gateway.request(MsgType.CONTACT_SYNC, ContactSync(since = 0))
        for (contact in reply.contacts) {
            users.upsert(
                userId = contact.userId,
                name = contact.name.takeIf { it.isNotEmpty() },
                isContact = true,
                blocked = contact.blocked,
                updatedAt = contact.updatedAt,
            )
        }
    }

    /** Our own profile, for the settings screen. */
    override suspend fun refreshMyProfile(): Outcome<UserSummary> = runOutcome {
        val selfId = sessionHolder.currentUserId
        require(selfId.isNotEmpty()) { "not signed in" }
        // An empty target means "me" to the gateway, which avoids assuming the id we
        // hold is still the one the session belongs to.
        profiles.fetch("").toDomain()
    }
}
