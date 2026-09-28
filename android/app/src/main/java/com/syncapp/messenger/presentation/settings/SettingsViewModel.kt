package com.syncapp.messenger.presentation.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.syncapp.messenger.core.AppError
import com.syncapp.messenger.core.Outcome
import com.syncapp.messenger.data.media.MediaUrlCache
import com.syncapp.messenger.datastore.AppSettings
import com.syncapp.messenger.datastore.LanguageMode
import com.syncapp.messenger.datastore.SettingsStore
import com.syncapp.messenger.datastore.ThemeMode
import com.syncapp.messenger.domain.model.Session
import com.syncapp.messenger.domain.model.UserSummary
import com.syncapp.messenger.domain.repository.AuthRepository
import com.syncapp.messenger.domain.repository.ConnectionStatus
import com.syncapp.messenger.domain.repository.MediaRepository
import com.syncapp.messenger.domain.repository.UserRepository
import com.syncapp.messenger.domain.usecase.LogoutUseCase
import com.syncapp.messenger.push.PushTokenRegistrar
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.onEach
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

data class ProfileEditState(
    val saving: Boolean = false,
    val error: AppError? = null,
)

/** The account-deletion dialog's own state, kept apart from the profile form. */
data class DeleteAccountState(
    val deleting: Boolean = false,
    val error: AppError? = null,
)

@HiltViewModel
class SettingsViewModel @Inject constructor(
    private val settingsStore: SettingsStore,
    private val userRepository: UserRepository,
    private val mediaRepository: MediaRepository,
    private val pushTokens: PushTokenRegistrar,
    private val logoutUseCase: LogoutUseCase,
    private val mediaUrls: MediaUrlCache,
    private val authRepository: AuthRepository,
) : ViewModel() {

    private val _deleteState = MutableStateFlow(DeleteAccountState())
    val deleteState: StateFlow<DeleteAccountState> = _deleteState.asStateFlow()

    /**
     * Erases the account.
     *
     * The password goes to the server, which re-checks it even though this
     * connection is already authenticated: a session token lives on this device,
     * and someone holding an unlocked phone must not be able to destroy the
     * account behind it with two taps.
     *
     * On success the repository tears the local state down as well, which is
     * what routes the app back to the login screen — there is no account to
     * return to, and a UI that stayed on the settings screen of a deleted
     * account would be showing something that no longer exists.
     */
    fun deleteAccount(password: String) {
        if (password.isEmpty()) return
        viewModelScope.launch {
            _deleteState.update { it.copy(deleting = true, error = null) }
            when (val result = authRepository.deleteAccount(password)) {
                is Outcome.Success -> _deleteState.update { it.copy(deleting = false) }
                is Outcome.Failure -> _deleteState.update {
                    it.copy(deleting = false, error = result.error)
                }
            }
        }
    }

    fun clearDeleteError() {
        _deleteState.update { it.copy(error = null) }
    }

    val settings: StateFlow<AppSettings> = settingsStore.settings
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), AppSettings())

    val session: StateFlow<Session?> = authRepository.session

    val connection: StateFlow<ConnectionStatus> = authRepository.connection

    /**
     * Our own profile, from the local directory — which the AUTH_OK identity, a
     * PROFILE_GET and any PROFILE mirrored from another device all write into. So the
     * screen shows the same name everyone else sees, not a device-local copy.
     */
    val profile: StateFlow<UserSummary?> = session
        .flatMapLatest { current ->
            if (current == null) flowOf(null) else userRepository.observeUser(current.userId)
        }
        .onEach { mediaUrls.request(it?.avatarRef) }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), null)

    val avatarUrls: StateFlow<Map<String, String>> = mediaUrls.urls

    private val _editState = MutableStateFlow(ProfileEditState())
    val editState: StateFlow<ProfileEditState> = _editState.asStateFlow()

    init {
        // The stored identity can be stale (a name changed on another device while this
        // one was offline), so ask once on open.
        viewModelScope.launch { userRepository.refreshMyProfile() }
    }

    fun setTheme(mode: ThemeMode) = viewModelScope.launch { settingsStore.setTheme(mode) }

    fun setLanguage(mode: LanguageMode) = viewModelScope.launch { settingsStore.setLanguage(mode) }

    /**
     * Notifications are turned off at the source: an empty PUSH_TOKEN clears the
     * device row server-side, so the server stops dispatching instead of this device
     * discarding what it already paid to receive.
     */
    fun setNotificationsEnabled(enabled: Boolean) = viewModelScope.launch {
        settingsStore.setNotificationsEnabled(enabled)
        if (enabled) pushTokens.sync() else pushTokens.clear()
    }

    /** Publishes the name. The gateway mirrors it to our other devices. */
    fun setDisplayName(name: String) {
        if (name.isBlank() || _editState.value.saving) return
        _editState.update { it.copy(saving = true, error = null) }
        viewModelScope.launch {
            val outcome = userRepository.updateMyProfile(displayName = name)
            _editState.update {
                it.copy(saving = false, error = (outcome as? Outcome.Failure)?.error)
            }
        }
    }

    /**
     * Uploads an avatar and publishes the reference.
     *
     * The picture goes through the ordinary media pipeline — MEDIA_INIT, a signed
     * PUT, then a `media_ref` — so an avatar is stored, served and garbage-collected
     * exactly like any attachment, and the profile only ever carries the reference.
     */
    fun setAvatar(bytes: ByteArray, mime: String) {
        if (_editState.value.saving) return
        _editState.update { it.copy(saving = true, error = null) }
        viewModelScope.launch {
            val uploaded = mediaRepository.upload(bytes, AVATAR_FILENAME, mime)
            val error = when (uploaded) {
                is Outcome.Failure -> uploaded.error
                is Outcome.Success ->
                    (userRepository.updateMyProfile(avatarRef = uploaded.value.mediaRef)
                        as? Outcome.Failure)?.error
            }
            _editState.update { it.copy(saving = false, error = error) }
        }
    }

    fun clearAvatar() {
        _editState.update { it.copy(saving = true, error = null) }
        viewModelScope.launch {
            val outcome = userRepository.updateMyProfile(clearAvatar = true)
            _editState.update {
                it.copy(saving = false, error = (outcome as? Outcome.Failure)?.error)
            }
        }
    }

    fun setGatewayUrl(url: String) = viewModelScope.launch { settingsStore.setGatewayUrl(url) }

    fun logout() = viewModelScope.launch { logoutUseCase() }

    private companion object {
        const val AVATAR_FILENAME = "avatar.jpg"
    }
}
