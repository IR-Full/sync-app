package com.syncapp.messenger.presentation.security

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.syncapp.messenger.R
import com.syncapp.messenger.presentation.components.localized

/**
 * Password and two-factor settings.
 *
 * The password could not be changed by ANY path before this screen existed, which made a
 * leaked one a permanent compromise: revoking every session does not stop somebody who
 * knows the password from signing straight back in.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SecurityScreen(
    onBack: () -> Unit,
    viewModel: SecurityViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    var currentPassword by rememberSaveable { mutableStateOf("") }
    var newPassword by rememberSaveable { mutableStateOf("") }
    var confirmPassword by rememberSaveable { mutableStateOf("") }
    var code by rememberSaveable { mutableStateOf("") }
    var disablePassword by rememberSaveable { mutableStateOf("") }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.security_title)) },
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
            Text(
                text = stringResource(R.string.security_password),
                style = MaterialTheme.typography.titleMedium,
            )
            Spacer(Modifier.height(8.dp))

            OutlinedTextField(
                value = currentPassword,
                onValueChange = { currentPassword = it },
                label = { Text(stringResource(R.string.security_password_current)) },
                visualTransformation = PasswordVisualTransformation(),
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )
            OutlinedTextField(
                value = newPassword,
                onValueChange = { newPassword = it },
                label = { Text(stringResource(R.string.security_password_new)) },
                visualTransformation = PasswordVisualTransformation(),
                singleLine = true,
                modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
            )
            OutlinedTextField(
                value = confirmPassword,
                onValueChange = { confirmPassword = it },
                label = { Text(stringResource(R.string.security_password_confirm)) },
                visualTransformation = PasswordVisualTransformation(),
                isError = state.passwordMismatch,
                singleLine = true,
                modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
            )
            if (state.passwordMismatch) {
                Text(
                    text = stringResource(R.string.security_password_mismatch),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier.padding(top = 4.dp),
                )
            }
            state.sessionsRevoked?.let { revoked ->
                Text(
                    text = stringResource(R.string.security_password_changed, revoked),
                    style = MaterialTheme.typography.bodySmall,
                    modifier = Modifier.padding(top = 4.dp),
                )
            }
            Button(
                onClick = {
                    viewModel.changePassword(currentPassword, newPassword, confirmPassword)
                    currentPassword = ""
                    newPassword = ""
                    confirmPassword = ""
                },
                enabled = !state.busy &&
                    currentPassword.isNotBlank() &&
                    newPassword.isNotBlank() &&
                    confirmPassword.isNotBlank(),
                modifier = Modifier.padding(top = 12.dp),
            ) { Text(stringResource(R.string.security_password)) }

            Spacer(Modifier.height(16.dp))
            HorizontalDivider()
            Spacer(Modifier.height(16.dp))

            Text(
                text = stringResource(R.string.security_two_factor),
                style = MaterialTheme.typography.titleMedium,
            )

            if (state.loading) {
                CircularProgressIndicator(modifier = Modifier.padding(top = 12.dp))
            } else {
                val twoFactor = state.twoFactor
                Text(
                    text = when {
                        twoFactor == null -> stringResource(R.string.security_two_factor_unknown)
                        twoFactor.enabled -> stringResource(R.string.security_two_factor_on)
                        else -> stringResource(R.string.security_two_factor_off)
                    },
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(top = 4.dp),
                )

                if (twoFactor != null && twoFactor.enabled) {
                    Text(
                        text = stringResource(
                            R.string.security_recovery_left,
                            twoFactor.recoveryCodesLeft,
                        ),
                        style = MaterialTheme.typography.bodySmall,
                        // Down to the last two, this is the warning that matters most on
                        // the screen: losing the final code and the phone together is the
                        // state there is no way back from.
                        color = if (twoFactor.recoveryCodesLeft <= 2) {
                            MaterialTheme.colorScheme.error
                        } else {
                            MaterialTheme.colorScheme.onSurfaceVariant
                        },
                        modifier = Modifier.padding(top = 4.dp),
                    )

                    // Password AND code: somebody holding only a stolen session token must
                    // not be able to remove the factor that keeps them out of the next login.
                    OutlinedTextField(
                        value = disablePassword,
                        onValueChange = { disablePassword = it },
                        label = { Text(stringResource(R.string.security_password_current)) },
                        visualTransformation = PasswordVisualTransformation(),
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth().padding(top = 12.dp),
                    )
                    OutlinedTextField(
                        value = code,
                        onValueChange = { code = it },
                        label = { Text(stringResource(R.string.security_code)) },
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.NumberPassword),
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
                    )
                    OutlinedButton(
                        onClick = {
                            viewModel.disableTwoFactor(disablePassword, code)
                            disablePassword = ""
                            code = ""
                        },
                        enabled = !state.busy && disablePassword.isNotBlank() && code.isNotBlank(),
                        modifier = Modifier.padding(top = 12.dp),
                    ) { Text(stringResource(R.string.security_two_factor_disable)) }
                } else if (twoFactor != null) {
                    val setup = state.setup
                    if (setup == null) {
                        Button(
                            onClick = viewModel::beginTwoFactor,
                            enabled = !state.busy,
                            modifier = Modifier.padding(top = 12.dp),
                        ) { Text(stringResource(R.string.security_two_factor_enable)) }
                    } else {
                        // The key as TEXT, not only as a QR code. The authenticator is
                        // usually on THIS phone, which has no way to point a camera at its
                        // own screen — so typing the key is the path that actually works.
                        Text(
                            text = stringResource(R.string.security_setup_key),
                            style = MaterialTheme.typography.labelSmall,
                            modifier = Modifier.padding(top = 12.dp),
                        )
                        Text(
                            text = setup.secret,
                            style = MaterialTheme.typography.bodyMedium,
                            fontFamily = FontFamily.Monospace,
                        )
                        Text(
                            text = stringResource(R.string.security_scan_hint),
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(top = 8.dp),
                        )
                        OutlinedTextField(
                            value = code,
                            onValueChange = { code = it },
                            label = { Text(stringResource(R.string.security_code)) },
                            keyboardOptions = KeyboardOptions(
                                keyboardType = KeyboardType.NumberPassword,
                            ),
                            singleLine = true,
                            modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
                        )
                        Row(
                            horizontalArrangement = Arrangement.spacedBy(8.dp),
                            modifier = Modifier.padding(top = 12.dp),
                        ) {
                            Button(
                                onClick = {
                                    viewModel.confirmTwoFactor(code)
                                    code = ""
                                },
                                enabled = !state.busy && code.isNotBlank(),
                            ) { Text(stringResource(R.string.action_confirm)) }
                            TextButton(
                                onClick = {
                                    viewModel.cancelSetup()
                                    code = ""
                                },
                                enabled = !state.busy,
                            ) { Text(stringResource(R.string.action_cancel)) }
                        }
                    }
                }
            }

            state.error?.let { error ->
                Text(
                    text = error.localized(),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier.padding(top = 12.dp),
                )
            }
        }
    }

    if (state.recoveryCodes.isNotEmpty()) {
        RecoveryCodesDialog(
            codes = state.recoveryCodes,
            onDismiss = viewModel::dismissRecoveryCodes,
        )
    }
}

/**
 * The recovery codes, shown exactly once.
 *
 * A modal dialog with a deliberate acknowledgement rather than a line in the list,
 * because this is the only moment they exist in readable form — the server keeps argon2id
 * hashes. Somebody who scrolls past this has lost their way back in.
 */
@Composable
private fun RecoveryCodesDialog(codes: List<String>, onDismiss: () -> Unit) {
    val acknowledged = remember { mutableStateOf(false) }

    AlertDialog(
        onDismissRequest = {
            // Deliberately NOT dismissible by tapping outside until the box is checked.
            // An accidental tap here destroys the only copy of the codes.
            if (acknowledged.value) onDismiss()
        },
        title = { Text(stringResource(R.string.security_recovery_codes)) },
        text = {
            Column {
                codes.forEach { value ->
                    Text(
                        text = value,
                        style = MaterialTheme.typography.bodyMedium,
                        fontFamily = FontFamily.Monospace,
                    )
                }
                Text(
                    text = stringResource(R.string.security_recovery_hint),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(top = 12.dp),
                )
            }
        },
        confirmButton = {
            TextButton(
                onClick = {
                    acknowledged.value = true
                    onDismiss()
                },
            ) { Text(stringResource(R.string.security_recovery_saved)) }
        },
    )
}
