package com.syncapp.messenger.presentation.secret

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.syncapp.messenger.R
import com.syncapp.messenger.data.secret.DeviceSafety

/**
 * The out-of-band verification screen.
 *
 * Two jobs, and only the second is optional. It shows the safety number so two
 * people can compare it over a channel the server does not control — the only
 * thing that catches a directory serving each side a different identity key. And
 * it is where a CHANGED key gets resolved: pinning refuses to send to a device
 * whose identity moved, so this is the way out of that, by a human deciding
 * rather than the app guessing.
 *
 * The wording avoids "verified". Comparing digits proves the keys match right
 * now; it does not make anything trustworthy in general, and a green checkmark
 * saying otherwise would be the most misleading thing on the screen.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SafetySheet(
    devices: List<DeviceSafety>,
    loading: Boolean,
    peerLabel: String,
    onAccept: (DeviceSafety) -> Unit,
    onDismiss: () -> Unit,
) {
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(Modifier.padding(horizontal = 16.dp).padding(bottom = 24.dp)) {
            Text(stringResource(R.string.safety_title, peerLabel))
            Text(
                stringResource(R.string.safety_explainer),
                Modifier.padding(vertical = 8.dp),
                style = MaterialTheme.typography.bodySmall,
            )

            if (loading) {
                CircularProgressIndicator(Modifier.padding(vertical = 12.dp))
            }

            LazyColumn(
                Modifier.heightIn(max = 400.dp).fillMaxWidth(),
                verticalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                items(devices, key = { it.deviceId }) { device ->
                    Column(Modifier.fillMaxWidth()) {
                        Text(
                            stringResource(
                                when {
                                    device.hasChanged -> R.string.safety_changed
                                    device.isPinned -> R.string.safety_pinned
                                    else -> R.string.safety_unpinned
                                },
                            ),
                            style = MaterialTheme.typography.labelMedium,
                            color = if (device.hasChanged) {
                                MaterialTheme.colorScheme.error
                            } else {
                                MaterialTheme.colorScheme.onSurfaceVariant
                            },
                        )
                        // Monospace: the number exists to be read aloud or compared
                        // character by character, and a proportional font makes both
                        // harder than they need to be.
                        Text(
                            device.number,
                            fontFamily = FontFamily.Monospace,
                            style = MaterialTheme.typography.bodyMedium,
                        )
                        if (!device.isPinned) {
                            Button(
                                onClick = { onAccept(device) },
                                modifier = Modifier.padding(top = 4.dp),
                            ) {
                                Text(
                                    stringResource(
                                        if (device.hasChanged) {
                                            R.string.safety_accept_change
                                        } else {
                                            R.string.safety_pin
                                        },
                                    ),
                                )
                            }
                        }
                        if (device.hasChanged) {
                            Text(
                                stringResource(R.string.safety_changed_hint),
                                style = MaterialTheme.typography.bodySmall,
                            )
                        }
                    }
                }
            }
        }
    }
}
