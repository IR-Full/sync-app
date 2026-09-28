package com.syncapp.messenger.presentation.settings

import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.Logout
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.syncapp.messenger.BuildConfig
import com.syncapp.messenger.R
import com.syncapp.messenger.datastore.LanguageMode
import com.syncapp.messenger.datastore.ThemeMode
import com.syncapp.messenger.presentation.components.Avatar
import com.syncapp.messenger.presentation.components.localized

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(
    onBack: () -> Unit,
    onOpenSessions: () -> Unit,
    onOpenPrivacy: () -> Unit,
    onOpenSecurity: () -> Unit,
    onOpenPremium: () -> Unit,
    viewModel: SettingsViewModel = hiltViewModel(),
) {
    val settings by viewModel.settings.collectAsStateWithLifecycle()
    val session by viewModel.session.collectAsStateWithLifecycle()
    val profile by viewModel.profile.collectAsStateWithLifecycle()
    val avatarUrls by viewModel.avatarUrls.collectAsStateWithLifecycle()
    val editState by viewModel.editState.collectAsStateWithLifecycle()
    val context = LocalContext.current

    val profileName = profile?.displayName.orEmpty()
    var nameDraft by rememberSaveable(profileName) {
        mutableStateOf(profileName.ifEmpty { session?.username.orEmpty() })
    }
    var endpointDraft by rememberSaveable(settings.gatewayUrlOverride) {
        mutableStateOf(settings.gatewayUrlOverride ?: BuildConfig.GATEWAY_URL)
    }
    var confirmLogout by remember { mutableStateOf(false) }
    var confirmDelete by remember { mutableStateOf(false) }
    var deletePassword by remember { mutableStateOf("") }
    val deleteState by viewModel.deleteState.collectAsStateWithLifecycle()

    val pickAvatar = rememberLauncherForActivityResult(
        ActivityResultContracts.PickVisualMedia(),
    ) { uri: Uri? ->
        if (uri == null) return@rememberLauncherForActivityResult
        val bytes = runCatching {
            context.contentResolver.openInputStream(uri)?.use { it.readBytes() }
        }.getOrNull()
        if (bytes != null) {
            viewModel.setAvatar(bytes, context.contentResolver.getType(uri) ?: "image/jpeg")
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.settings_title)) },
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
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(16.dp),
        ) {
            // --- Profile
            Row(verticalAlignment = Alignment.CenterVertically) {
                Avatar(
                    label = profile?.displayLabel ?: session?.username.orEmpty(),
                    size = 64.dp,
                    imageUrl = avatarUrls[profile?.avatarRef],
                )
                Column(modifier = Modifier.padding(start = 16.dp)) {
                    Text(
                        text = session?.username?.let { "@$it" }.orEmpty(),
                        style = MaterialTheme.typography.titleMedium,
                    )
                    Text(
                        text = stringResource(R.string.settings_user_id, session?.userId.orEmpty()),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Row(
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                        modifier = Modifier.padding(top = 8.dp),
                    ) {
                        OutlinedButton(
                            enabled = !editState.saving,
                            onClick = {
                                pickAvatar.launch(
                                    PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly),
                                )
                            },
                        ) { Text(stringResource(R.string.settings_change_avatar)) }
                        if (profile?.avatarRef != null) {
                            TextButton(
                                onClick = viewModel::clearAvatar,
                                enabled = !editState.saving,
                            ) { Text(stringResource(R.string.settings_remove_avatar)) }
                        }
                    }
                }
            }

            Spacer(Modifier.height(12.dp))

            OutlinedTextField(
                value = nameDraft,
                onValueChange = { nameDraft = it },
                label = { Text(stringResource(R.string.settings_display_name)) },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )
            Button(
                onClick = { viewModel.setDisplayName(nameDraft) },
                enabled = !editState.saving && nameDraft.isNotBlank() && nameDraft != profileName,
                modifier = Modifier.padding(top = 8.dp),
            ) { Text(stringResource(R.string.action_save)) }

            editState.error?.let { error ->
                Text(
                    text = error.localized(),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier.padding(top = 8.dp),
                )
            }

            SectionDivider()

            // --- Appearance
            SectionTitle(stringResource(R.string.settings_theme))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                ThemeMode.entries.forEach { mode ->
                    FilterChip(
                        selected = settings.theme == mode,
                        onClick = { viewModel.setTheme(mode) },
                        label = {
                            Text(
                                stringResource(
                                    when (mode) {
                                        ThemeMode.SYSTEM -> R.string.settings_theme_system
                                        ThemeMode.LIGHT -> R.string.settings_theme_light
                                        ThemeMode.DARK -> R.string.settings_theme_dark
                                    },
                                ),
                            )
                        },
                    )
                }
            }

            Spacer(Modifier.height(16.dp))

            SectionTitle(stringResource(R.string.settings_language))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                LanguageMode.entries.forEach { mode ->
                    FilterChip(
                        selected = settings.language == mode,
                        onClick = { viewModel.setLanguage(mode) },
                        label = {
                            Text(
                                stringResource(
                                    when (mode) {
                                        LanguageMode.SYSTEM -> R.string.settings_language_system
                                        LanguageMode.RUSSIAN -> R.string.settings_language_ru
                                        LanguageMode.ENGLISH -> R.string.settings_language_en
                                    },
                                ),
                            )
                        },
                    )
                }
            }

            SectionDivider()

            // --- Notifications
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Column(modifier = Modifier.weight(1f)) {
                    Text(stringResource(R.string.settings_notifications))
                    Text(
                        text = stringResource(R.string.settings_notifications_hint),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Switch(
                    checked = settings.notificationsEnabled,
                    onCheckedChange = viewModel::setNotificationsEnabled,
                )
            }

            if (BuildConfig.ALLOW_ENDPOINT_OVERRIDE) {
                SectionDivider()
                SectionTitle(stringResource(R.string.settings_gateway))
                OutlinedTextField(
                    value = endpointDraft,
                    onValueChange = { endpointDraft = it },
                    label = { Text(stringResource(R.string.settings_gateway_url)) },
                    singleLine = true,
                    supportingText = { Text(stringResource(R.string.settings_gateway_hint)) },
                    modifier = Modifier.fillMaxWidth(),
                )
                Button(
                    onClick = { viewModel.setGatewayUrl(endpointDraft) },
                    modifier = Modifier.padding(top = 8.dp),
                ) { Text(stringResource(R.string.action_save)) }
                Text(
                    text = "${BuildConfig.ENVIRONMENT_NAME} · ${BuildConfig.VERSION_NAME}",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(top = 8.dp),
                )
            }

            SectionDivider()
            SectionTitle(stringResource(R.string.settings_account))

            // Security first in this section, and above the session list on purpose:
            // changing the password is what REVOKES those sessions, so reading downwards
            // matches the order somebody actually secures an account in.
            OutlinedButton(
                onClick = onOpenSecurity,
                modifier = Modifier.fillMaxWidth(),
            ) { Text(stringResource(R.string.security_title)) }

            OutlinedButton(
                onClick = onOpenPremium,
                modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
            ) { Text(stringResource(R.string.premium_title)) }

            OutlinedButton(
                onClick = onOpenPrivacy,
                modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
            ) { Text(stringResource(R.string.privacy_title)) }

            OutlinedButton(
                onClick = onOpenSessions,
                modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
            ) { Text(stringResource(R.string.sessions_title)) }

            SectionDivider()

            OutlinedButton(
                onClick = { confirmLogout = true },
                modifier = Modifier.fillMaxWidth(),
            ) {
                Icon(Icons.AutoMirrored.Filled.Logout, contentDescription = null)
                Text(
                    text = stringResource(R.string.settings_logout),
                    modifier = Modifier.padding(start = 8.dp),
                )
            }

            // Below logout and worded as what it is. An app that lets someone
            // create an account must let them destroy it in the app; burying it
            // in a support email is the pattern both store policies exist to
            // forbid.
            TextButton(
                onClick = {
                    deletePassword = ""
                    confirmDelete = true
                },
                modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
            ) {
                Text(
                    text = stringResource(R.string.settings_delete_account),
                    color = MaterialTheme.colorScheme.error,
                )
            }
        }
    }

    if (confirmDelete) {
        AlertDialog(
            onDismissRequest = { if (!deleteState.deleting) confirmDelete = false },
            title = { Text(stringResource(R.string.settings_delete_account)) },
            text = {
                Column {
                    Text(stringResource(R.string.settings_delete_account_confirm))
                    OutlinedTextField(
                        value = deletePassword,
                        onValueChange = { deletePassword = it },
                        label = { Text(stringResource(R.string.auth_password)) },
                        singleLine = true,
                        visualTransformation = PasswordVisualTransformation(),
                        enabled = !deleteState.deleting,
                        modifier = Modifier.fillMaxWidth().padding(top = 12.dp),
                    )
                    deleteState.error?.let { error ->
                        Text(
                            text = error.localized(),
                            color = MaterialTheme.colorScheme.error,
                            style = MaterialTheme.typography.bodySmall,
                            modifier = Modifier.padding(top = 8.dp),
                        )
                    }
                }
            },
            confirmButton = {
                TextButton(
                    onClick = { viewModel.deleteAccount(deletePassword) },
                    enabled = deletePassword.isNotEmpty() && !deleteState.deleting,
                ) {
                    Text(
                        text = stringResource(R.string.settings_delete_account),
                        color = MaterialTheme.colorScheme.error,
                    )
                }
            },
            dismissButton = {
                TextButton(
                    onClick = {
                        viewModel.clearDeleteError()
                        confirmDelete = false
                    },
                    enabled = !deleteState.deleting,
                ) { Text(stringResource(R.string.action_cancel)) }
            },
        )
    }

    if (confirmLogout) {
        AlertDialog(
            onDismissRequest = { confirmLogout = false },
            title = { Text(stringResource(R.string.settings_logout)) },
            text = { Text(stringResource(R.string.settings_logout_confirm)) },
            confirmButton = {
                TextButton(
                    onClick = {
                        confirmLogout = false
                        viewModel.logout()
                    },
                ) { Text(stringResource(R.string.settings_logout)) }
            },
            dismissButton = {
                TextButton(onClick = { confirmLogout = false }) {
                    Text(stringResource(R.string.action_cancel))
                }
            },
        )
    }
}

@Composable
private fun SectionTitle(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.labelLarge,
        color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.padding(bottom = 8.dp),
    )
}

@Composable
private fun SectionDivider() {
    HorizontalDivider(modifier = Modifier.padding(vertical = 20.dp))
}
