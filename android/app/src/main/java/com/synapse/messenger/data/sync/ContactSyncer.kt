package com.synapse.messenger.data.sync

import android.util.Log
import com.synapse.messenger.core.Outcome
import com.synapse.messenger.domain.repository.UserRepository
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Pulls the address book after a connection comes up.
 *
 * Wrapped rather than called directly so a failure here never surfaces as a user
 * error: contacts are supporting data (they supply names for ids), and a chat list
 * that renders numeric ids is far better than one that refuses to load.
 */
@Singleton
class ContactSyncer @Inject constructor(
    private val userRepository: UserRepository,
) {
    suspend fun syncQuietly() {
        when (val outcome = userRepository.syncContacts()) {
            is Outcome.Failure -> Log.i(TAG, "contact sync skipped: ${outcome.error}")
            is Outcome.Success -> Unit
        }
    }

    private companion object {
        const val TAG = "ContactSyncer"
    }
}
