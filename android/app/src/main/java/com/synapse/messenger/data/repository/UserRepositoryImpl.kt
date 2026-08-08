package com.synapse.messenger.data.repository

import com.synapse.messenger.core.Outcome
import com.synapse.messenger.core.runOutcome
import com.synapse.messenger.data.mapper.toDomain
import com.synapse.messenger.database.SynapseDatabase
import com.synapse.messenger.domain.model.UserSummary
import com.synapse.messenger.domain.repository.UserRepository
import com.synapse.messenger.network.SynapseGateway
import com.synapse.messenger.network.protocol.Block
import com.synapse.messenger.network.protocol.ContactAdd
import com.synapse.messenger.network.protocol.ContactList
import com.synapse.messenger.network.protocol.ContactSync
import com.synapse.messenger.network.protocol.MsgType
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

@Singleton
class UserRepositoryImpl @Inject constructor(
    private val gateway: SynapseGateway,
    private val database: SynapseDatabase,
) : UserRepository {

    private val users get() = database.userDao()

    override fun observeKnownUsers(): Flow<List<UserSummary>> =
        users.observeAll().map { rows -> rows.map { it.toDomain() } }

    /**
     * Resolves `@username` to a user id, and records the contact.
     *
     * This is the protocol's only user lookup. CONTACT_ADD is the single request
     * that accepts a username and answers with an id, so "search for a person" and
     * "add a contact" are necessarily the same call — a username that does not exist
     * comes back as NOT_FOUND, which is also how this client validates one.
     *
     * The username is stored locally because it never comes back: CONTACT_SYNC
     * returns ids and our own private labels, never a handle.
     */
    override suspend fun addContact(username: String, name: String?): Outcome<UserSummary> =
        runOutcome {
            val handle = username.trim().removePrefix("@").lowercase()
            require(handle.isNotEmpty()) { "empty username" }

            val reply: ContactList = gateway.request(
                MsgType.CONTACT_ADD,
                ContactAdd(target = "@$handle", name = name?.trim().orEmpty()),
            )
            val contact = reply.contacts.firstOrNull()
                ?: throw IllegalStateException("contact add returned nothing")
            users.upsert(
                userId = contact.userId,
                username = handle,
                name = contact.name.takeIf { it.isNotEmpty() },
                isContact = true,
                blocked = contact.blocked,
                updatedAt = contact.updatedAt,
            )
            contact.toDomain(username = handle)
        }

    /**
     * Full contact sync.
     *
     * CONTACT_SYNC supports an incremental cursor, but this client asks for
     * everything: an address book is small, and a stale cursor after a reinstall
     * would silently hide contacts forever — the wrong trade for the bytes saved.
     */
    override suspend fun syncContacts(): Outcome<Unit> = runOutcome {
        val reply: ContactList = gateway.request(MsgType.CONTACT_SYNC, ContactSync(since = 0))
        for (contact in reply.contacts) {
            users.upsert(
                userId = contact.userId,
                // Never null out a username we know: the server has none to give back.
                username = null,
                name = contact.name.takeIf { it.isNotEmpty() },
                isContact = true,
                blocked = contact.blocked,
                updatedAt = contact.updatedAt,
            )
        }
    }

    /**
     * Blocks or unblocks. A block stops traffic in BOTH directions server-side: the
     * blocked party cannot message, and cannot read replies by reopening the chat
     * either, so nothing local needs to enforce it.
     */
    override suspend fun setBlocked(userIdOrUsername: String, blocked: Boolean): Outcome<Unit> =
        runOutcome {
            val target = userIdOrUsername.trim().let {
                if (it.startsWith("@")) "@" + it.removePrefix("@").lowercase() else it
            }
            gateway.request<ContactList>(MsgType.BLOCK, Block(target = target, blocked = blocked))
            val userId = target.removePrefix("@").let { handle ->
                if (target.startsWith("@")) users.findByUsername(handle)?.userId else target
            }
            if (userId != null) {
                users.upsert(userId = userId, blocked = blocked, isContact = false)
            }
        }
}
