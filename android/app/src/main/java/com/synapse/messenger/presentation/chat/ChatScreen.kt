package com.synapse.messenger.presentation.chat

import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.AttachFile
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Snackbar
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil.compose.AsyncImage
import com.synapse.messenger.R
import com.synapse.messenger.domain.model.AttachmentKind
import com.synapse.messenger.domain.model.ChatKind
import com.synapse.messenger.domain.model.Message
import com.synapse.messenger.domain.model.MessageStatus
import com.synapse.messenger.presentation.components.Avatar
import com.synapse.messenger.presentation.components.ConnectionBanner
import com.synapse.messenger.presentation.components.EmptyState
import com.synapse.messenger.presentation.components.MessageStatusIcon
import com.synapse.messenger.presentation.components.formatClock
import com.synapse.messenger.presentation.components.formatTimestamp
import com.synapse.messenger.presentation.components.localized
import kotlinx.coroutines.flow.filter

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ChatScreen(
    onBack: () -> Unit,
    viewModel: ChatViewModel = hiltViewModel(),
) {
    val chat by viewModel.chat.collectAsStateWithLifecycle()
    val chatKey by viewModel.chatKey.collectAsStateWithLifecycle()
    val messages by viewModel.messages.collectAsStateWithLifecycle()
    val typing by viewModel.typingUsers.collectAsStateWithLifecycle()
    val presence by viewModel.peerPresence.collectAsStateWithLifecycle()
    val labels by viewModel.senderLabels.collectAsStateWithLifecycle()
    val mediaUrls by viewModel.mediaUrls.collectAsStateWithLifecycle()
    val state by viewModel.state.collectAsStateWithLifecycle()
    val input by viewModel.input.collectAsStateWithLifecycle()
    val connection by viewModel.connection.collectAsStateWithLifecycle()

    val listState = rememberLazyListState()
    val context = LocalContext.current

    val pickImage = rememberLauncherForActivityResult(
        ActivityResultContracts.PickVisualMedia(),
    ) { uri: Uri? ->
        if (uri == null) return@rememberLauncherForActivityResult
        // Read the bytes here: the upload ticket is signed for an exact size, so the
        // content has to be materialised before MEDIA_INIT can be asked for.
        val resolver = context.contentResolver
        val bytes = runCatching { resolver.openInputStream(uri)?.use { it.readBytes() } }.getOrNull()
        if (bytes != null) {
            viewModel.sendAttachment(
                bytes = bytes,
                filename = uri.lastPathSegment?.substringAfterLast('/') ?: "image",
                mime = resolver.getType(uri) ?: "image/jpeg",
            )
        }
    }

    // New messages should land in view, but only when the user is already at the
    // bottom: yanking the list while they read older messages is worse than a
    // missed autoscroll.
    LaunchedEffect(messages.size) {
        if (messages.isEmpty()) return@LaunchedEffect
        val atBottom = listState.firstVisibleItemIndex <= 1
        if (atBottom) listState.animateScrollToItem(0)
    }

    LaunchedEffect(chatKey, messages.size) { viewModel.markVisibleRead() }

    // Pagination: the list is reversed, so "the end of the list" is the oldest
    // message on screen.
    LaunchedEffect(listState, messages.size) {
        snapshotFlow { listState.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: 0 }
            .filter { index -> messages.isNotEmpty() && index >= messages.size - 3 }
            .collect { viewModel.loadOlderMessages() }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    val title = chat?.title?.takeIf { it.isNotEmpty() }
                        ?: chatKey.removePrefix("@").let { "@$it" }
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Avatar(
                            label = title,
                            size = 36.dp,
                            imageUrl = mediaUrls[chat?.peerAvatarRef],
                        )
                        Column(modifier = Modifier.padding(start = 12.dp)) {
                            Text(text = title, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            // Typing wins over presence: it is the more specific and more
                            // perishable fact. Neither line appears when neither is known.
                            when {
                                typing.isNotEmpty() -> Text(
                                    text = stringResource(R.string.chat_typing),
                                    style = MaterialTheme.typography.labelSmall,
                                    color = MaterialTheme.colorScheme.primary,
                                )
                                presence != null -> Text(
                                    text = if (presence!!.online) {
                                        stringResource(R.string.presence_online)
                                    } else {
                                        stringResource(
                                            R.string.presence_last_seen,
                                            formatTimestamp(presence!!.lastSeenMs),
                                        )
                                    },
                                    style = MaterialTheme.typography.labelSmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
                        }
                    }
                },
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
                .imePadding(),
        ) {
            ConnectionBanner(connection)

            Box(modifier = Modifier.weight(1f)) {
                if (messages.isEmpty()) {
                    EmptyState(
                        title = stringResource(R.string.chat_empty_title),
                        subtitle = stringResource(R.string.chat_empty_subtitle),
                    )
                } else {
                    LazyColumn(
                        state = listState,
                        reverseLayout = true,
                        modifier = Modifier.fillMaxSize(),
                        contentPadding = androidx.compose.foundation.layout.PaddingValues(
                            horizontal = 12.dp,
                            vertical = 8.dp,
                        ),
                        verticalArrangement = Arrangement.spacedBy(6.dp),
                    ) {
                        // Reversed so newest sits at the bottom without a scroll jump on
                        // every arrival.
                        items(messages.asReversed(), key = { it.id }) { message ->
                            MessageBubble(
                                message = message,
                                senderLabel = labels[message.senderId],
                                showSender = chat?.kind != ChatKind.DIRECT && !message.isOutgoing,
                                mediaUrl = message.attachment?.mediaRef?.let { mediaUrls[it] },
                                onNeedMedia = viewModel::requestMedia,
                                onRetry = { viewModel.retry(message.id) },
                            )
                        }
                        if (state.loadingOlder) {
                            item {
                                Box(
                                    modifier = Modifier.fillMaxWidth().padding(12.dp),
                                    contentAlignment = Alignment.Center,
                                ) {
                                    CircularProgressIndicator(modifier = Modifier.size(20.dp), strokeWidth = 2.dp)
                                }
                            }
                        }
                    }
                }
            }

            state.error?.let { error ->
                Snackbar(
                    modifier = Modifier.padding(8.dp),
                    action = {
                        Text(
                            text = stringResource(R.string.action_dismiss),
                            modifier = Modifier
                                .clickable { viewModel.dismissError() }
                                .padding(8.dp),
                        )
                    },
                ) { Text(error.localized()) }
            }

            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .background(MaterialTheme.colorScheme.surfaceVariant)
                    .padding(horizontal = 8.dp, vertical = 6.dp),
                verticalAlignment = Alignment.Bottom,
            ) {
                IconButton(
                    onClick = {
                        pickImage.launch(
                            PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly),
                        )
                    },
                ) {
                    Icon(
                        Icons.Default.AttachFile,
                        contentDescription = stringResource(R.string.chat_attach),
                    )
                }
                OutlinedTextField(
                    value = input,
                    onValueChange = viewModel::onInputChange,
                    placeholder = { Text(stringResource(R.string.chat_input_hint)) },
                    modifier = Modifier
                        .weight(1f)
                        .heightIn(max = 140.dp),
                    maxLines = 5,
                )
                IconButton(onClick = viewModel::send, enabled = input.isNotBlank()) {
                    Icon(
                        Icons.AutoMirrored.Filled.Send,
                        contentDescription = stringResource(R.string.chat_send),
                        tint = if (input.isNotBlank()) {
                            MaterialTheme.colorScheme.primary
                        } else {
                            MaterialTheme.colorScheme.onSurfaceVariant
                        },
                    )
                }
            }
        }
    }
}

@Composable
private fun MessageBubble(
    message: Message,
    senderLabel: String?,
    showSender: Boolean,
    mediaUrl: String?,
    onNeedMedia: (String) -> Unit,
    onRetry: () -> Unit,
) {
    val attachment = message.attachment
    LaunchedEffect(attachment?.mediaRef) {
        val ref = attachment?.mediaRef
        if (ref != null && attachment.kind == AttachmentKind.IMAGE) onNeedMedia(ref)
    }

    val alignment = if (message.isOutgoing) Alignment.CenterEnd else Alignment.CenterStart
    val bubbleColor = when {
        message.status == MessageStatus.FAILED -> MaterialTheme.colorScheme.errorContainer
        message.isOutgoing -> MaterialTheme.colorScheme.primaryContainer
        else -> MaterialTheme.colorScheme.surfaceVariant
    }
    val shape = RoundedCornerShape(
        topStart = 16.dp,
        topEnd = 16.dp,
        bottomStart = if (message.isOutgoing) 16.dp else 4.dp,
        bottomEnd = if (message.isOutgoing) 4.dp else 16.dp,
    )

    Box(modifier = Modifier.fillMaxWidth(), contentAlignment = alignment) {
        Column(
            modifier = Modifier
                .widthIn(max = 300.dp)
                .clip(shape)
                .background(bubbleColor)
                .clickable(enabled = message.status == MessageStatus.FAILED, onClick = onRetry)
                .padding(horizontal = 12.dp, vertical = 8.dp),
        ) {
            if (showSender && senderLabel != null) {
                Text(
                    text = senderLabel,
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.primary,
                )
            }

            if (message.forwardedFrom != null) {
                Text(
                    text = stringResource(R.string.chat_forwarded),
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }

            when {
                message.deleted -> Text(
                    text = stringResource(R.string.chat_message_deleted),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                attachment != null && attachment.kind == AttachmentKind.IMAGE -> {
                    if (mediaUrl != null) {
                        AsyncImage(
                            model = mediaUrl,
                            contentDescription = attachment.filename,
                            contentScale = ContentScale.FillWidth,
                            modifier = Modifier
                                .fillMaxWidth()
                                .heightIn(max = 260.dp)
                                .clip(RoundedCornerShape(12.dp)),
                        )
                    } else {
                        Box(
                            modifier = Modifier.fillMaxWidth().heightIn(min = 120.dp),
                            contentAlignment = Alignment.Center,
                        ) {
                            CircularProgressIndicator(modifier = Modifier.size(20.dp), strokeWidth = 2.dp)
                        }
                    }
                    if (message.text.isNotEmpty()) {
                        Text(
                            text = message.text,
                            style = MaterialTheme.typography.bodyLarge,
                            modifier = Modifier.padding(top = 6.dp),
                        )
                    }
                }
                attachment != null -> Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier.padding(vertical = 4.dp),
                ) {
                    Icon(Icons.Default.AttachFile, contentDescription = null, modifier = Modifier.size(18.dp))
                    Text(
                        text = attachment.filename.ifEmpty { attachment.kind.name.lowercase() },
                        style = MaterialTheme.typography.bodyMedium,
                        modifier = Modifier.padding(start = 6.dp),
                    )
                }
                else -> Text(text = message.text, style = MaterialTheme.typography.bodyLarge)
            }

            Row(
                modifier = Modifier
                    .align(Alignment.End)
                    .padding(top = 2.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(4.dp),
            ) {
                if (message.edited) {
                    Text(
                        text = stringResource(R.string.chat_edited),
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Text(
                    text = formatClock(message.createdAt),
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                if (message.isOutgoing) {
                    MessageStatusIcon(message.status)
                }
                if (message.status == MessageStatus.FAILED) {
                    Icon(
                        Icons.Default.Refresh,
                        contentDescription = stringResource(R.string.action_retry),
                        tint = MaterialTheme.colorScheme.error,
                        modifier = Modifier.size(14.dp),
                    )
                }
            }
        }
    }
}
