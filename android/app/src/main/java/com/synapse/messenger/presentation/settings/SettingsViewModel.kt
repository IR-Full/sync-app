package com.synapse.messenger.presentation.settings

import android.content.Context
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.synapse.messenger.core.IoDispatcher
import com.synapse.messenger.datastore.AppSettings
import com.synapse.messenger.datastore.LanguageMode
import com.synapse.messenger.datastore.SettingsStore
import com.synapse.messenger.datastore.ThemeMode
import com.synapse.messenger.domain.model.Session
import com.synapse.messenger.domain.repository.AuthRepository
import com.synapse.messenger.domain.repository.ConnectionStatus
import com.synapse.messenger.domain.usecase.LogoutUseCase
import com.synapse.messenger.push.PushTokenRegistrar
import dagger.hilt.android.lifecycle.HiltViewModel
import java.io.File
import javax.inject.Inject
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

@HiltViewModel
class SettingsViewModel @Inject constructor(
    private val settingsStore: SettingsStore,
    private val pushTokens: PushTokenRegistrar,
    private val logoutUseCase: LogoutUseCase,
    private val context: Context,
    @param:IoDispatcher private val io: CoroutineDispatcher,
    authRepository: AuthRepository,
) : ViewModel() {

    val settings: StateFlow<AppSettings> = settingsStore.settings
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), AppSettings())

    val session: StateFlow<Session?> = authRepository.session

    val connection: StateFlow<ConnectionStatus> = authRepository.connection

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

    /**
     * The display name is local.
     *
     * `User.DisplayName` exists in the server's model but no protocol message reads
     * or writes it, so this cannot travel to anyone else — it labels this device's own
     * profile screen only. Same for the avatar, which the protocol has no concept of
     * at all.
     */
    fun setDisplayName(name: String) = viewModelScope.launch {
        settingsStore.setDisplayName(name)
    }

    fun setAvatar(bytes: ByteArray) = viewModelScope.launch {
        val previous = settings.value.avatarPath
        val path = withContext(io) {
            // A new file name per pick, rather than overwriting one: Coil keys its
            // cache on the model, so a stable path would keep showing the old image.
            val file = File(context.filesDir, "$AVATAR_PREFIX${System.currentTimeMillis()}.jpg")
            file.writeBytes(bytes)
            previous?.let { old -> File(old).takeIf { it.exists() && it != file }?.delete() }
            file.absolutePath
        }
        settingsStore.setAvatarPath(path)
    }

    fun setGatewayUrl(url: String) = viewModelScope.launch { settingsStore.setGatewayUrl(url) }

    fun logout() = viewModelScope.launch { logoutUseCase() }

    private companion object {
        const val AVATAR_PREFIX = "avatar_"
    }
}
