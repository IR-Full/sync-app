package com.syncapp.messenger.presentation.secret

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.syncapp.messenger.R

/**
 * The end-to-end encrypted side channel with one peer.
 *
 * A sheet rather than a screen, deliberately: it sits beside the ordinary
 * conversation instead of replacing it, because the two are different things and
 * the difference matters. Nothing here is stored on the server, nothing syncs to
 * this person's other devices, and none of it survives a logout.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SecretChatSheet(
    peerUserId: String,
    peerLabel: String,
    onDismiss: () -> Unit,
    viewModel: SecretChatViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    var input by remember { mutableStateOf("") }

    LaunchedEffect(peerUserId) { viewModel.open(peerUserId) }

    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(Modifier.padding(horizontal = 16.dp).padding(bottom = 24.dp)) {
            Row(
                Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(stringResource(R.string.secret_title, peerLabel))
                TextButton(onClick = viewModel::loadSafetyNumbers) {
                    Text(stringResource(R.string.secret_verify))
                }
            }
            Text(
                stringResource(R.string.secret_explainer),
                Modifier.padding(bottom = 12.dp),
            )

            LazyColumn(
                Modifier.heightIn(min = 120.dp, max = 320.dp).fillMaxWidth(),
                verticalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                items(state.messages, key = { it.id }) { message ->
                    Row(
                        Modifier.fillMaxWidth(),
                        horizontalArrangement =
                            if (message.outgoing) Arrangement.End else Arrangement.Start,
                    ) {
                        Text(
                            // A message that could not be opened is shown as such
                            // rather than hidden: silence would read as "they
                            // never wrote", which is a worse lie than an error.
                            if (message.failed) {
                                stringResource(R.string.secret_undecryptable)
                            } else {
                                message.text
                            },
                            Modifier
                                .background(
                                    androidx.compose.material3.MaterialTheme.colorScheme
                                        .surfaceVariant,
                                    RoundedCornerShape(12.dp),
                                )
                                .padding(horizontal = 10.dp, vertical = 6.dp),
                        )
                    }
                }
            }

            state.error?.let { error ->
                Text(
                    stringResource(
                        when (error) {
                            SecretError.NO_DEVICES -> R.string.secret_no_devices
                            SecretError.IDENTITY_CHANGED -> R.string.secret_identity_changed
                            SecretError.UNKNOWN -> R.string.error_generic
                        },
                    ),
                    Modifier.padding(top = 8.dp),
                )
            }

            Row(
                Modifier.fillMaxWidth().padding(top = 12.dp),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                OutlinedTextField(
                    value = input,
                    onValueChange = { input = it },
                    modifier = Modifier.weight(1f),
                    singleLine = true,
                )
                Button(
                    onClick = {
                        viewModel.send(input)
                        input = ""
                    },
                    enabled = input.isNotBlank() && !state.sending,
                ) {
                    Text(stringResource(R.string.chat_send))
                }
            }
        }
    }

    if (state.safety.isNotEmpty() || state.loadingSafety) {
        SafetySheet(
            devices = state.safety,
            loading = state.loadingSafety,
            peerLabel = peerLabel,
            onAccept = viewModel::acceptIdentity,
            onDismiss = viewModel::dismissSafety,
        )
    }
}
