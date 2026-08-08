package com.synapse.messenger

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.synapse.messenger.datastore.AppSettings
import com.synapse.messenger.datastore.SettingsStore
import com.synapse.messenger.domain.model.Session
import com.synapse.messenger.domain.repository.AuthRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.stateIn

/**
 * Everything the shell needs before any screen exists: which theme and language to
 * render in, and whether there is a session at all.
 */
@HiltViewModel
class RootViewModel @Inject constructor(
    settingsStore: SettingsStore,
    authRepository: AuthRepository,
) : ViewModel() {

    val settings: StateFlow<AppSettings> = settingsStore.settings
        .stateIn(viewModelScope, SharingStarted.Eagerly, AppSettings())

    val session: StateFlow<Session?> = authRepository.session

    val restored: StateFlow<Boolean> = authRepository.restored
}
