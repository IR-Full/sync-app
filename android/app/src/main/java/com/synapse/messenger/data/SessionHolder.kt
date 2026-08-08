package com.synapse.messenger.data

import com.synapse.messenger.core.AppScope
import com.synapse.messenger.datastore.SessionStore
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn

/**
 * The current user id, hot and synchronous.
 *
 * Almost every query needs it — "is this message mine", "which unread are not
 * mine", "whose read receipt is this" — and awaiting a DataStore read inside a
 * Room Flow transform would make every list re-subscribe on each emission. Kept
 * eagerly warm instead, with the empty string standing for "logged out" so callers
 * never juggle a nullable id inside SQL.
 */
@Singleton
class SessionHolder @Inject constructor(
    sessionStore: SessionStore,
    @AppScope scope: CoroutineScope,
) {
    val userId: StateFlow<String> = sessionStore.session
        .map { it?.userId.orEmpty() }
        .stateIn(scope, SharingStarted.Eagerly, "")

    val currentUserId: String get() = userId.value
}
