package com.syncapp.messenger.presentation

import com.syncapp.messenger.core.AppError
import com.syncapp.messenger.core.Outcome
import com.syncapp.messenger.domain.model.DeviceSession
import com.syncapp.messenger.domain.model.PrivacySettings
import com.syncapp.messenger.domain.model.Session
import com.syncapp.messenger.domain.model.Visibility
import com.syncapp.messenger.domain.repository.AuthRepository
import com.syncapp.messenger.domain.repository.ConnectionStatus
import com.syncapp.messenger.presentation.privacy.PrivacyField
import com.syncapp.messenger.presentation.privacy.PrivacyViewModel
import com.syncapp.messenger.presentation.sessions.SessionsViewModel
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/**
 * A recording auth repository.
 *
 * Hand-written for the same reason as the other fakes in this module: what these
 * two view models do is decide *which* call to make and what to believe about
 * the answer, so a double that records calls tests the thing that matters.
 */
private class FakeAuthRepository : AuthRepository {
    val calls = mutableListOf<String>()

    var sessionsResult: Outcome<List<DeviceSession>> = Outcome.Success(emptyList())
    var revokeResult: Outcome<Int> = Outcome.Success(1)
    var privacyResult: Outcome<PrivacySettings> = Outcome.Success(PrivacySettings())
    var setPrivacyResult: Outcome<PrivacySettings>? = null

    override val session: StateFlow<Session?> = MutableStateFlow(null)
    override val restored: StateFlow<Boolean> = MutableStateFlow(true)
    override val connection: StateFlow<ConnectionStatus> =
        MutableStateFlow(ConnectionStatus.ONLINE)

    override suspend fun login(username: String, password: String, totpCode: String) =
        error("not used")
    override suspend fun register(username: String, password: String) = error("not used")
    override suspend fun logout() {
        calls += "logout()"
    }

    override suspend fun listSessions(): Outcome<List<DeviceSession>> {
        calls += "listSessions()"
        return sessionsResult
    }

    override suspend fun revokeSession(sessionId: String): Outcome<Int> {
        calls += "revokeSession($sessionId)"
        return revokeResult
    }

    override suspend fun revokeOtherSessions(): Outcome<Int> {
        calls += "revokeOtherSessions()"
        return revokeResult
    }

    override suspend fun deleteAccount(password: String, reason: String): Outcome<Unit> {
        calls += "deleteAccount($password)"
        return Outcome.Success(Unit)
    }

    override suspend fun privacy(): Outcome<PrivacySettings> {
        calls += "privacy()"
        return privacyResult
    }

    override suspend fun setPrivacy(settings: PrivacySettings): Outcome<PrivacySettings> {
        calls += "setPrivacy($settings)"
        return setPrivacyResult ?: Outcome.Success(settings)
    }
}

private fun session(
    id: String,
    current: Boolean = false,
    createdAtMs: Long = 0,
) = DeviceSession(
    sessionId = id,
    deviceId = "d-$id",
    platform = "android",
    createdAtMs = createdAtMs,
    expiresAtMs = createdAtMs + 1,
    current = current,
)

class SessionsViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() {
        Dispatchers.setMain(dispatcher)
    }

    @After
    fun tearDown() {
        Dispatchers.resetMain()
    }

    @Test
    fun `loads the session list on creation`() = runTest(dispatcher) {
        val auth = FakeAuthRepository().apply {
            sessionsResult = Outcome.Success(listOf(session("a")))
        }

        val model = SessionsViewModel(auth)
        testScheduler.advanceUntilIdle()

        assertEquals(listOf("listSessions()"), auth.calls)
        assertEquals(1, model.state.value.sessions.size)
        assertEquals(false, model.state.value.loading)
    }

    /**
     * This device first, then newest. The session someone is looking for is
     * either "the phone in my hand" or "the thing that just signed in that I do
     * not recognise", and both are at the top of that order.
     */
    @Test
    fun `puts this device first and the newest next`() = runTest(dispatcher) {
        val auth = FakeAuthRepository().apply {
            sessionsResult = Outcome.Success(
                listOf(
                    session("old", createdAtMs = 1_000),
                    session("new", createdAtMs = 9_000),
                    session("here", current = true, createdAtMs = 5_000),
                ),
            )
        }

        val model = SessionsViewModel(auth)
        testScheduler.advanceUntilIdle()

        assertEquals(
            listOf("here", "new", "old"),
            model.state.value.sessions.map { it.sessionId },
        )
    }

    /**
     * The list is re-read rather than edited locally. The server decides what a
     * revoke actually did, and a list showing what we hoped for is exactly the
     * failure this screen exists to prevent — someone believing a lost device
     * was signed out when it was not.
     */
    @Test
    fun `re-reads the list after a revoke instead of removing the row`() = runTest(dispatcher) {
        val auth = FakeAuthRepository().apply {
            sessionsResult = Outcome.Success(listOf(session("a"), session("b")))
        }
        val model = SessionsViewModel(auth)
        testScheduler.advanceUntilIdle()
        auth.calls.clear()

        model.revoke("b")
        testScheduler.advanceUntilIdle()

        assertEquals(listOf("revokeSession(b)", "listSessions()"), auth.calls)
    }

    @Test
    fun `keeps the list when a revoke fails and reports the error`() = runTest(dispatcher) {
        val auth = FakeAuthRepository().apply {
            sessionsResult = Outcome.Success(listOf(session("a"), session("b")))
            revokeResult = Outcome.Failure(AppError.Offline)
        }
        val model = SessionsViewModel(auth)
        testScheduler.advanceUntilIdle()

        model.revoke("b")
        testScheduler.advanceUntilIdle()

        assertEquals(2, model.state.value.sessions.size)
        assertEquals(AppError.Offline, model.state.value.error)
        assertEquals(null, model.state.value.revoking)
    }

    @Test
    fun `sweeping other sessions leaves this one signed in`() = runTest(dispatcher) {
        val auth = FakeAuthRepository()
        val model = SessionsViewModel(auth)
        testScheduler.advanceUntilIdle()
        auth.calls.clear()

        model.revokeOthers()
        testScheduler.advanceUntilIdle()

        // Not logout(), and not revokeSession of our own id: "everywhere else"
        // is its own operation precisely so it cannot sign this device out.
        assertEquals(listOf("revokeOtherSessions()", "listSessions()"), auth.calls)
    }

    @Test
    fun `reports a failure to load`() = runTest(dispatcher) {
        val auth = FakeAuthRepository().apply {
            sessionsResult = Outcome.Failure(AppError.Timeout)
        }

        val model = SessionsViewModel(auth)
        testScheduler.advanceUntilIdle()

        assertEquals(AppError.Timeout, model.state.value.error)
        assertEquals(false, model.state.value.loading)
    }
}

class PrivacyViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() {
        Dispatchers.setMain(dispatcher)
    }

    @After
    fun tearDown() {
        Dispatchers.resetMain()
    }

    @Test
    fun `reads the settings on creation`() = runTest(dispatcher) {
        val auth = FakeAuthRepository().apply {
            privacyResult = Outcome.Success(PrivacySettings(lastSeen = Visibility.CONTACTS))
        }

        val model = PrivacyViewModel(auth)
        testScheduler.advanceUntilIdle()

        assertEquals(Visibility.CONTACTS, model.state.value.settings.lastSeen)
    }

    /**
     * All three go every time, matching the protocol: a partial update would
     * make "nobody" indistinguishable from "not set".
     */
    @Test
    fun `sends all three settings when one changes`() = runTest(dispatcher) {
        val auth = FakeAuthRepository().apply {
            privacyResult = Outcome.Success(
                PrivacySettings(
                    lastSeen = Visibility.EVERYONE,
                    avatar = Visibility.CONTACTS,
                    groups = Visibility.NOBODY,
                ),
            )
        }
        val model = PrivacyViewModel(auth)
        testScheduler.advanceUntilIdle()

        model.set(PrivacyField.LAST_SEEN, Visibility.NOBODY)
        testScheduler.advanceUntilIdle()

        assertTrue(
            "the untouched settings were not sent: ${auth.calls}",
            auth.calls.any {
                it.startsWith("setPrivacy") &&
                    it.contains("lastSeen=NOBODY") &&
                    it.contains("avatar=CONTACTS") &&
                    it.contains("groups=NOBODY")
            },
        )
    }

    /**
     * The state is the server's answer, not the request. Echoing the request
     * would tell someone a privacy setting took effect when it had not — the one
     * lie this screen must never tell.
     */
    @Test
    fun `shows what the server returned rather than what was asked for`() = runTest(dispatcher) {
        val auth = FakeAuthRepository().apply {
            setPrivacyResult = Outcome.Success(PrivacySettings(avatar = Visibility.CONTACTS))
        }
        val model = PrivacyViewModel(auth)
        testScheduler.advanceUntilIdle()

        model.set(PrivacyField.AVATAR, Visibility.NOBODY)
        testScheduler.advanceUntilIdle()

        assertEquals(Visibility.CONTACTS, model.state.value.settings.avatar)
    }

    @Test
    fun `leaves the setting alone when the write fails`() = runTest(dispatcher) {
        val auth = FakeAuthRepository().apply {
            privacyResult = Outcome.Success(PrivacySettings(groups = Visibility.EVERYONE))
            setPrivacyResult = Outcome.Failure(AppError.Offline)
        }
        val model = PrivacyViewModel(auth)
        testScheduler.advanceUntilIdle()

        model.set(PrivacyField.GROUPS, Visibility.NOBODY)
        testScheduler.advanceUntilIdle()

        assertEquals(Visibility.EVERYONE, model.state.value.settings.groups)
        assertEquals(AppError.Offline, model.state.value.error)
    }

    @Test
    fun `does not write when nothing changed`() = runTest(dispatcher) {
        val auth = FakeAuthRepository().apply {
            privacyResult = Outcome.Success(PrivacySettings(avatar = Visibility.NOBODY))
        }
        val model = PrivacyViewModel(auth)
        testScheduler.advanceUntilIdle()
        auth.calls.clear()

        model.set(PrivacyField.AVATAR, Visibility.NOBODY)
        testScheduler.advanceUntilIdle()

        assertEquals(emptyList<String>(), auth.calls)
    }
}

class VisibilityTest {

    @Test
    fun `round-trips every known value through the wire spelling`() {
        for (value in listOf(Visibility.EVERYONE, Visibility.CONTACTS, Visibility.NOBODY)) {
            assertEquals(value, Visibility.fromWire(value.wire))
        }
    }

    /**
     * The whole reason UNKNOWN exists. A value a newer server introduced must
     * not decode to EVERYONE — that would turn an upgrade on the server into a
     * privacy failure on every client that had not caught up.
     */
    @Test
    fun `an unfamiliar value is not read as everyone`() {
        assertEquals(Visibility.UNKNOWN, Visibility.fromWire("close-friends"))
    }

    @Test
    fun `an unfamiliar value sends as the strictest setting`() {
        assertEquals("nobody", Visibility.UNKNOWN.wire)
    }

    @Test
    fun `wire spellings are case-insensitive on the way in`() {
        assertEquals(Visibility.CONTACTS, Visibility.fromWire("Contacts"))
    }
}
