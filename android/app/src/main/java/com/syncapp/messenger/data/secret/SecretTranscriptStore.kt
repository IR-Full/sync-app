package com.syncapp.messenger.data.secret

import com.syncapp.messenger.datastore.SecretKeyStore
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/**
 * Secret-chat transcripts, which exist only here.
 *
 * The server relays ciphertext and stores nothing, so there is no history to
 * backfill and no sync between a person's own devices: what this phone received
 * is the whole of what this phone knows. That is a property of the mode, not a
 * missing feature, and the UI is arranged to say so rather than imply a history
 * that would silently be incomplete.
 *
 * Kept in memory with a write-through to [SecretKeyStore], which encrypts under
 * the AndroidKeyStore — the same protection the ratchet state gets, for the same
 * reason: plaintext at rest would undo the point of never letting the server see
 * it.
 */
@kotlinx.serialization.Serializable
data class SecretMessageRow(
    val id: String,
    val peerId: String,
    val text: String,
    val timestamp: Long,
    val outgoing: Boolean,
    /** set when decryption failed, so the failure is visible rather than silent */
    val failed: Boolean = false,
)

@Singleton
class SecretTranscriptStore @Inject constructor(
    private val keys: SecretKeyStore,
) {
    private val state = MutableStateFlow<Map<String, List<SecretMessageRow>>>(emptyMap())

    /**
     * Serialises appends.
     *
     * Two arriving messages would otherwise read-modify-write the same map
     * concurrently and one would be lost — and unlike an ordinary chat there is
     * no server copy to notice the gap against.
     */
    private val lock = Mutex()
    private var loadedFor: String? = null

    val transcripts: StateFlow<Map<String, List<SecretMessageRow>>> = state.asStateFlow()

    fun conversation(peerId: String): List<SecretMessageRow> = state.value[peerId].orEmpty()

    /** Loads the stored transcripts for an account, once per session. */
    suspend fun load(ownerId: String) = lock.withLock {
        if (loadedFor == ownerId) return@withLock
        state.value = keys.transcripts()
        loadedFor = ownerId
    }

    suspend fun append(peerId: String, text: String, outgoing: Boolean, failed: Boolean = false) =
        lock.withLock {
            val row = SecretMessageRow(
                id = UUID.randomUUID().toString(),
                peerId = peerId,
                text = text,
                timestamp = System.currentTimeMillis(),
                outgoing = outgoing,
                failed = failed,
            )
            val next = state.value.toMutableMap()
            next[peerId] = next[peerId].orEmpty() + row
            state.value = next
            keys.saveTranscripts(next)
        }

    /** Everything here belongs to the account that just left. */
    suspend fun clear() = lock.withLock {
        state.value = emptyMap()
        loadedFor = null
    }
}
