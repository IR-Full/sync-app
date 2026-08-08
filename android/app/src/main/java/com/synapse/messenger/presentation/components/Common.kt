package com.synapse.messenger.presentation.components

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CloudOff
import androidx.compose.material.icons.filled.Done
import androidx.compose.material.icons.filled.DoneAll
import androidx.compose.material.icons.filled.ErrorOutline
import androidx.compose.material.icons.filled.Schedule
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import coil.compose.AsyncImage
import com.synapse.messenger.R
import com.synapse.messenger.domain.model.MessageStatus
import com.synapse.messenger.domain.repository.ConnectionStatus
import java.text.SimpleDateFormat
import java.util.Calendar
import java.util.Date
import java.util.Locale
import kotlin.math.absoluteValue

@Composable
fun LoadingState(modifier: Modifier = Modifier) {
    Box(modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        CircularProgressIndicator()
    }
}

@Composable
fun EmptyState(
    title: String,
    subtitle: String? = null,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier.fillMaxSize().padding(32.dp),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(title, style = MaterialTheme.typography.titleMedium, textAlign = TextAlign.Center)
        if (subtitle != null) {
            Text(
                subtitle,
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center,
                modifier = Modifier.padding(top = 8.dp),
            )
        }
    }
}

@Composable
fun ErrorState(
    message: String,
    onRetry: (() -> Unit)? = null,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier.fillMaxSize().padding(32.dp),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Icon(
            Icons.Default.ErrorOutline,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.error,
            modifier = Modifier.size(40.dp),
        )
        Text(
            message,
            style = MaterialTheme.typography.bodyLarge,
            textAlign = TextAlign.Center,
            modifier = Modifier.padding(top = 12.dp),
        )
        if (onRetry != null) {
            Button(onClick = onRetry, modifier = Modifier.padding(top = 16.dp)) {
                Text(stringResource(R.string.action_retry))
            }
        }
    }
}

/**
 * A thin strip while the connection is not usable.
 *
 * It says "connecting", not "no internet": the protocol reconnects with backoff and
 * resumes a dropped session, so the app is frequently in a state that is neither
 * broken nor ready — and the user can keep typing through it either way.
 */
@Composable
fun ConnectionBanner(status: ConnectionStatus, modifier: Modifier = Modifier) {
    AnimatedVisibility(visible = status != ConnectionStatus.ONLINE, modifier = modifier) {
        val offline = status == ConnectionStatus.OFFLINE
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .background(
                    if (offline) MaterialTheme.colorScheme.errorContainer
                    else MaterialTheme.colorScheme.secondaryContainer,
                )
                .padding(horizontal = 16.dp, vertical = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            if (offline) {
                Icon(Icons.Default.CloudOff, contentDescription = null, modifier = Modifier.size(16.dp))
            } else {
                CircularProgressIndicator(modifier = Modifier.size(14.dp), strokeWidth = 2.dp)
            }
            Text(
                text = stringResource(
                    if (offline) R.string.connection_offline else R.string.connection_connecting,
                ),
                style = MaterialTheme.typography.labelMedium,
                modifier = Modifier.padding(start = 8.dp),
            )
        }
    }
}

/**
 * Delivery ticks.
 *
 * Two states, not three: the gateway acknowledges durable persistence and relays
 * read receipts, but fanout to a device is not acknowledged at all — so there is no
 * honest "delivered" to draw between them.
 */
@Composable
fun MessageStatusIcon(status: MessageStatus, modifier: Modifier = Modifier) {
    val tint = when (status) {
        MessageStatus.FAILED -> MaterialTheme.colorScheme.error
        MessageStatus.READ -> MaterialTheme.colorScheme.primary
        else -> MaterialTheme.colorScheme.onSurfaceVariant
    }
    val icon = when (status) {
        MessageStatus.PENDING -> Icons.Default.Schedule
        MessageStatus.SENT -> Icons.Default.Done
        MessageStatus.READ -> Icons.Default.DoneAll
        MessageStatus.FAILED -> Icons.Default.ErrorOutline
    }
    val description = stringResource(
        when (status) {
            MessageStatus.PENDING -> R.string.status_pending
            MessageStatus.SENT -> R.string.status_sent
            MessageStatus.READ -> R.string.status_read
            MessageStatus.FAILED -> R.string.status_failed
        },
    )
    Icon(icon, contentDescription = description, tint = tint, modifier = modifier.size(14.dp))
}

/**
 * An avatar.
 *
 * Initials are the primary rendering, not a fallback: the protocol has no avatar
 * concept at all, so the only image that can ever appear here is one this device
 * chose for itself (see the profile screen).
 */
@Composable
fun Avatar(
    label: String,
    modifier: Modifier = Modifier,
    size: Dp = 48.dp,
    imagePath: String? = null,
) {
    val palette = listOf(
        Color(0xFF1F6FEB), Color(0xFF00796B), Color(0xFF8E24AA),
        Color(0xFFEF6C00), Color(0xFF546E7A), Color(0xFFC2185B),
    )
    val background = palette[label.hashCode().absoluteValue % palette.size]
    Box(
        modifier = modifier.size(size).clip(CircleShape).background(background),
        contentAlignment = Alignment.Center,
    ) {
        if (imagePath != null) {
            AsyncImage(
                model = imagePath,
                contentDescription = null,
                modifier = Modifier.fillMaxSize(),
            )
        } else {
            Text(
                text = initialsOf(label),
                color = Color.White,
                style = MaterialTheme.typography.titleMedium,
            )
        }
    }
}

private fun initialsOf(label: String): String {
    val cleaned = label.removePrefix("@").trim()
    if (cleaned.isEmpty()) return "?"
    val parts = cleaned.split(' ', '.', '_', '-').filter { it.isNotEmpty() }
    return when {
        parts.size >= 2 -> "${parts[0].first()}${parts[1].first()}".uppercase()
        else -> cleaned.take(2).uppercase()
    }
}

/** Clock time for today, a date for anything older — the usual messenger rule. */
fun formatTimestamp(timestamp: Long, locale: Locale = Locale.getDefault()): String {
    if (timestamp <= 0) return ""
    val now = Calendar.getInstance()
    val then = Calendar.getInstance().apply { timeInMillis = timestamp }
    val sameDay = now.get(Calendar.YEAR) == then.get(Calendar.YEAR) &&
        now.get(Calendar.DAY_OF_YEAR) == then.get(Calendar.DAY_OF_YEAR)
    val pattern = if (sameDay) "HH:mm" else "d MMM"
    return SimpleDateFormat(pattern, locale).format(Date(timestamp))
}

fun formatClock(timestamp: Long, locale: Locale = Locale.getDefault()): String {
    if (timestamp <= 0) return ""
    return SimpleDateFormat("HH:mm", locale).format(Date(timestamp))
}
