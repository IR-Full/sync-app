package com.syncapp.messenger.presentation.premium

import android.content.Intent
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
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.core.net.toUri
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.syncapp.messenger.R
import com.syncapp.messenger.domain.model.PaymentMethod
import com.syncapp.messenger.presentation.components.localized
import java.text.DateFormat
import java.util.Date

/**
 * The Premium screen.
 *
 * What the tier grants is read from the ENTITLEMENTS, never from the plan name. That is
 * not tidiness: a deployment with no billing service reports the plan as `free` while
 * granting everything, so a screen that gated on `plan == "premium"` would hide every
 * feature on exactly the installation where they are all available.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PremiumScreen(
    onBack: () -> Unit,
    viewModel: PremiumViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.premium_title)) },
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
            Row(
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text(
                    text = stringResource(R.string.premium_plan),
                    style = MaterialTheme.typography.bodyMedium,
                    modifier = Modifier.weight(1f),
                )
                Text(
                    text = if (state.entitlements.isPremium) {
                        stringResource(R.string.premium_plan_premium)
                    } else {
                        stringResource(R.string.premium_plan_free)
                    },
                    style = MaterialTheme.typography.titleSmall,
                    color = if (state.entitlements.isPremium) {
                        MaterialTheme.colorScheme.primary
                    } else {
                        MaterialTheme.colorScheme.onSurfaceVariant
                    },
                )
            }

            if (state.entitlements.periodEndMs > 0) {
                Text(
                    text = stringResource(
                        R.string.premium_until,
                        DateFormat.getDateInstance().format(Date(state.entitlements.periodEndMs)),
                    ),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(top = 4.dp),
                )
            }
            if (state.entitlements.cancelAtPeriodEnd) {
                Text(
                    text = stringResource(R.string.premium_cancelling),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier.padding(top = 4.dp),
                )
            }

            Text(
                text = stringResource(R.string.premium_subtitle),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 8.dp),
            )

            Spacer(Modifier.height(16.dp))
            HorizontalDivider()
            Spacer(Modifier.height(16.dp))

            val offers = state.offers
            when {
                offers == null -> CircularProgressIndicator()
                state.paymentsUnavailable -> Text(
                    text = stringResource(R.string.premium_unavailable),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                else -> offers.forEach { offer ->
                    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 12.dp)) {
                        Column(modifier = Modifier.padding(16.dp)) {
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                Text(
                                    text = offer.priceText(),
                                    style = MaterialTheme.typography.titleMedium,
                                    modifier = Modifier.weight(1f),
                                )
                                Text(
                                    // A plural rather than a format string: "1 days" is
                                    // the kind of wrong that ships, and Russian needs
                                    // three forms where English needs two.
                                    text = pluralStringResource(
                                        R.plurals.premium_period,
                                        offer.periodDays,
                                        offer.periodDays,
                                    ),
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
                            Row(
                                horizontalArrangement = Arrangement.spacedBy(8.dp),
                                modifier = Modifier.padding(top = 12.dp),
                            ) {
                                // One button per method the SERVER offered for this plan
                                // and country — not every method this build can draw. A
                                // button for a method unavailable here fails only after
                                // somebody has committed to paying.
                                offer.methods.forEach { method ->
                                    Button(
                                        onClick = { viewModel.checkout(offer.plan, method) },
                                        enabled = !state.busy,
                                    ) {
                                        Text(
                                            when (method) {
                                                PaymentMethod.SBP ->
                                                    stringResource(R.string.premium_method_sbp)
                                                PaymentMethod.CARD ->
                                                    stringResource(R.string.premium_method_card)
                                            },
                                        )
                                    }
                                }
                            }
                        }
                    }
                }
            }

            if (state.entitlements.isPremium && !state.entitlements.cancelAtPeriodEnd) {
                OutlinedButton(
                    onClick = viewModel::cancel,
                    enabled = !state.busy,
                    modifier = Modifier.padding(top = 8.dp),
                ) { Text(stringResource(R.string.premium_cancel)) }
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

    state.intent?.let { intent ->
        PaymentDialog(
            payUrl = intent.payUrl,
            qrPayload = intent.qrPayload,
            onDismiss = viewModel::dismissIntent,
        )
    }
}

/**
 * Where the user finishes paying.
 *
 * The two shapes are not interchangeable and that difference is the whole content of this
 * dialog. `payUrl` is FOLLOWED in a browser; an SBP `qrPayload` is DISPLAYED for a bank
 * app to read. Opening a payload as a URL produces a dead link, and rendering a redirect
 * URL as a payload produces a code that leads to a web page instead of to a payment.
 */
@Composable
private fun PaymentDialog(payUrl: String, qrPayload: String, onDismiss: () -> Unit) {
    val context = LocalContext.current

    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.premium_title)) },
        text = {
            Column {
                if (qrPayload.isNotEmpty()) {
                    Text(
                        text = stringResource(R.string.premium_qr),
                        style = MaterialTheme.typography.bodyMedium,
                    )
                    // The payload as selectable text rather than a rendered code.
                    // Rendering one needs a QR encoder this project does not depend on,
                    // and a payload that can be copied into a bank app works today.
                    Text(
                        text = qrPayload,
                        style = MaterialTheme.typography.bodySmall,
                        fontFamily = FontFamily.Monospace,
                        modifier = Modifier.padding(top = 8.dp),
                    )
                }
            }
        },
        confirmButton = {
            if (payUrl.isNotEmpty()) {
                TextButton(
                    onClick = {
                        context.startActivity(Intent(Intent.ACTION_VIEW, payUrl.toUri()))
                        onDismiss()
                    },
                ) { Text(stringResource(R.string.premium_pay)) }
            } else {
                TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_close)) }
            }
        },
        dismissButton = if (payUrl.isNotEmpty()) {
            { TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_close)) } }
        } else {
            null
        },
    )
}
