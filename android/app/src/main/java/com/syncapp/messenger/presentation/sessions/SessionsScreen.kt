package com.syncapp.messenger.presentation.sessions

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Snackbar
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.syncapp.messenger.R
import com.syncapp.messenger.domain.model.DeviceSession
import com.syncapp.messenger.presentation.components.formatTimestamp
import com.syncapp.messenger.presentation.components.localized

/**
 * Every device signed in to this account, and the way to sign one out.
 *
 * The screen exists because "log out" used to be a local gesture: the device
 * forgot its token while the session stayed valid on the server until it
 * expired. A phone that was lost rather than logged out kept access, and nothing
 * could see that or stop it.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SessionsScreen(
    onBack: () -> Unit,
    viewModel: SessionsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    var confirmRevokeOthers by remember { mutableStateOf(false) }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.sessions_title)) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(
                            Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = stringResource(R.string.action_back),
                        )
                    }
                },
            )
        },
    ) { padding ->
        Column(Modifier.fillMaxSize().padding(padding)) {
            Text(
                stringResource(R.string.sessions_explainer),
                Modifier.padding(horizontal = 16.dp, vertical = 12.dp),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )

            state.error?.let { error ->
                Snackbar(
                    modifier = Modifier.padding(8.dp),
                    action = {
                        TextButton(onClick = viewModel::clearError) {
                            Text(stringResource(R.string.action_dismiss))
                        }
                    },
                ) { Text(error.localized()) }
            }

            if (state.loading && state.sessions.isEmpty()) {
                CircularProgressIndicator(Modifier.padding(16.dp))
            }

            LazyColumn(Modifier.weight(1f)) {
                items(state.sessions, key = { it.sessionId }) { session ->
                    SessionRow(
                        session = session,
                        busy = state.revoking == session.sessionId,
                        onRevoke = { viewModel.revoke(session.sessionId) },
                    )
                    HorizontalDivider()
                }
            }

            // Offered only when there is something to sweep. A button that ends
            // "every other session" on an account with one session does nothing
            // and says nothing, which reads as the button being broken.
            if (state.sessions.count { !it.current } > 0) {
                OutlinedButton(
                    onClick = { confirmRevokeOthers = true },
                    modifier = Modifier.fillMaxWidth().padding(16.dp),
                ) { Text(stringResource(R.string.sessions_revoke_others)) }
            }
        }
    }

    if (confirmRevokeOthers) {
        AlertDialog(
            onDismissRequest = { confirmRevokeOthers = false },
            title = { Text(stringResource(R.string.sessions_revoke_others)) },
            text = { Text(stringResource(R.string.sessions_revoke_others_confirm)) },
            confirmButton = {
                TextButton(
                    onClick = {
                        confirmRevokeOthers = false
                        viewModel.revokeOthers()
                    },
                ) { Text(stringResource(R.string.sessions_revoke)) }
            },
            dismissButton = {
                TextButton(onClick = { confirmRevokeOthers = false }) {
                    Text(stringResource(R.string.action_cancel))
                }
            },
        )
    }
}

@Composable
private fun SessionRow(
    session: DeviceSession,
    busy: Boolean,
    onRevoke: () -> Unit,
) {
    Row(
        Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 12.dp),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(
                session.platform.ifEmpty { stringResource(R.string.sessions_unknown_platform) },
                style = MaterialTheme.typography.bodyLarge,
            )
            Text(
                stringResource(R.string.sessions_since, formatTimestamp(session.createdAtMs)),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            if (session.current) {
                Text(
                    stringResource(R.string.sessions_this_device),
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.primary,
                )
            }
        }

        when {
            busy -> CircularProgressIndicator(Modifier.padding(horizontal = 12.dp))
            // No button on this device's own row. Ending it is a logout, and
            // logout lives in settings where it means what it says; offering it
            // here as one entry in a list of devices invites the tap that signs
            // a person out of the phone they are holding.
            session.current -> Unit
            else -> TextButton(onClick = onRevoke) {
                Text(
                    stringResource(R.string.sessions_revoke),
                    color = MaterialTheme.colorScheme.error,
                )
            }
        }
    }
}
