package com.syncapp.messenger.presentation.privacy

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Snackbar
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.syncapp.messenger.R
import com.syncapp.messenger.domain.model.Visibility
import com.syncapp.messenger.presentation.components.localized

/**
 * Three settings, each with three values.
 *
 * Three rather than one overall level because they are read on different paths
 * and answer different questions: presence fanout asks about last seen, a
 * profile read asks about the avatar, and a group add asks whether the actor may
 * add this account at all. Collapsing them into "private / public" would make
 * the common case — visible to contacts, invisible to strangers — unsayable.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PrivacyScreen(
    onBack: () -> Unit,
    viewModel: PrivacyViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.privacy_title)) },
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
        Column(
            Modifier
                .fillMaxSize()
                .padding(padding)
                .verticalScroll(rememberScrollState()),
        ) {
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

            if (state.loading) {
                CircularProgressIndicator(Modifier.padding(16.dp))
                return@Column
            }

            PrivacySection(
                title = stringResource(R.string.privacy_last_seen),
                explainer = stringResource(R.string.privacy_last_seen_hint),
                selected = state.settings.lastSeen,
                enabled = !state.saving,
                onSelect = { viewModel.set(PrivacyField.LAST_SEEN, it) },
            )
            HorizontalDivider()
            PrivacySection(
                title = stringResource(R.string.privacy_avatar),
                explainer = stringResource(R.string.privacy_avatar_hint),
                selected = state.settings.avatar,
                enabled = !state.saving,
                onSelect = { viewModel.set(PrivacyField.AVATAR, it) },
            )
            HorizontalDivider()
            PrivacySection(
                title = stringResource(R.string.privacy_groups),
                explainer = stringResource(R.string.privacy_groups_hint),
                selected = state.settings.groups,
                enabled = !state.saving,
                onSelect = { viewModel.set(PrivacyField.GROUPS, it) },
            )
        }
    }
}

@Composable
private fun PrivacySection(
    title: String,
    explainer: String,
    selected: Visibility,
    enabled: Boolean,
    onSelect: (Visibility) -> Unit,
) {
    Column(Modifier.padding(vertical = 12.dp)) {
        Text(
            title,
            Modifier.padding(horizontal = 16.dp),
            style = MaterialTheme.typography.titleSmall,
        )
        Text(
            explainer,
            Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )

        for (option in listOf(Visibility.EVERYONE, Visibility.CONTACTS, Visibility.NOBODY)) {
            Row(
                Modifier
                    .fillMaxWidth()
                    .selectable(
                        selected = option == selected,
                        enabled = enabled,
                        onClick = { onSelect(option) },
                    )
                    .padding(horizontal = 16.dp, vertical = 8.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                RadioButton(selected = option == selected, onClick = null, enabled = enabled)
                Text(
                    stringResource(
                        when (option) {
                            Visibility.EVERYONE -> R.string.privacy_everyone
                            Visibility.CONTACTS -> R.string.privacy_contacts
                            else -> R.string.privacy_nobody
                        },
                    ),
                    Modifier.padding(start = 12.dp),
                )
            }
        }

        // A value the server sent that this build has no name for. Shown rather
        // than silently normalised: the setting is in force, we simply cannot
        // say what it is, and rendering it as "everyone" would be a privacy
        // failure introduced by being out of date.
        if (selected == Visibility.UNKNOWN) {
            Text(
                stringResource(R.string.privacy_unknown_value),
                Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
    }
}
