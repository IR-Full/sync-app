package com.syncapp.messenger.presentation.chats

import androidx.annotation.StringRes
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.NotificationsOff
import androidx.compose.material.icons.filled.PushPin
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.syncapp.messenger.R
import com.syncapp.messenger.data.sync.MessageIngestor
import com.syncapp.messenger.domain.model.Chat
import com.syncapp.messenger.presentation.components.Avatar
import com.syncapp.messenger.presentation.components.ConnectionBanner
import com.syncapp.messenger.presentation.components.EmptyState
import com.syncapp.messenger.presentation.components.ErrorState
import com.syncapp.messenger.presentation.components.LoadingState
import com.syncapp.messenger.presentation.components.formatTimestamp
import com.syncapp.messenger.presentation.components.localized

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ChatListScreen(
    onOpenChat: (String) -> Unit,
    onNewChat: () -> Unit,
    onOpenSettings: () -> Unit,
    viewModel: ChatListViewModel = hiltViewModel(),
) {
    val chats by viewModel.chats.collectAsStateWithLifecycle()
    val archived by viewModel.archivedChats.collectAsStateWithLifecycle()
    val state by viewModel.state.collectAsStateWithLifecycle()
    val connection by viewModel.connection.collectAsStateWithLifecycle()
    val avatarUrls by viewModel.avatarUrls.collectAsStateWithLifecycle()

    // The archive is a MODE of this screen rather than a separate destination. It shares
    // the whole row renderer and the refresh, and the only difference is which query feeds
    // it — so a route of its own would be a second copy of everything below.
    var showArchived by rememberSaveable { mutableStateOf(false) }
    val visible = if (showArchived) archived else chats

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Text(
                        stringResource(
                            if (showArchived) R.string.chats_archived else R.string.chats_title,
                        ),
                    )
                },
                navigationIcon = {
                    // Only in the archive: on the main list there is nothing to go back to,
                    // and a disabled back arrow is worse than none.
                    if (showArchived) {
                        IconButton(onClick = { showArchived = false }) {
                            Icon(
                                Icons.AutoMirrored.Filled.ArrowBack,
                                contentDescription = stringResource(R.string.action_back),
                            )
                        }
                    }
                },
                actions = {
                    IconButton(onClick = onOpenSettings) {
                        Icon(Icons.Default.Settings, contentDescription = stringResource(R.string.settings_title))
                    }
                },
            )
        },
        floatingActionButton = {
            FloatingActionButton(onClick = onNewChat) {
                Icon(Icons.Default.Add, contentDescription = stringResource(R.string.new_chat_title))
            }
        },
    ) { padding ->
        Column(modifier = Modifier.fillMaxSize().padding(padding)) {
            ConnectionBanner(connection)

            PullToRefreshBox(
                isRefreshing = state.refreshing,
                onRefresh = viewModel::refresh,
                modifier = Modifier.fillMaxSize(),
            ) {
                when {
                    state.loading && visible.isEmpty() && !showArchived -> LoadingState()
                    // An error only takes over the screen when there is nothing to show;
                    // with a cache present the list stays usable and the banner explains
                    // the connection.
                    visible.isEmpty() && state.error != null -> ErrorState(
                        message = state.error!!.localized(),
                        onRetry = viewModel::refresh,
                    )
                    visible.isEmpty() -> EmptyState(
                        title = stringResource(
                            if (showArchived) {
                                R.string.chats_archived_empty
                            } else {
                                R.string.chats_empty_title
                            },
                        ),
                        subtitle = if (showArchived) {
                            ""
                        } else {
                            stringResource(R.string.chats_empty_subtitle)
                        },
                    )
                    else -> LazyColumn(modifier = Modifier.fillMaxSize()) {
                        // The entry point appears only when there is something in it. A
                        // permanent "Archived (0)" row is a control that does nothing.
                        if (!showArchived && archived.isNotEmpty()) {
                            item(key = "archived-entry") {
                                ArchiveEntry(
                                    count = archived.size,
                                    onClick = { showArchived = true },
                                )
                                HorizontalDivider()
                            }
                        }
                        items(visible, key = { it.id }) { chat ->
                            ChatRow(
                                chat = chat,
                                avatarUrl = avatarUrls[chat.peerAvatarRef],
                                onClick = { onOpenChat(chat.id) },
                                onMute = { duration -> viewModel.mute(chat, duration) },
                                onTogglePin = { viewModel.togglePin(chat) },
                                onSetArchived = { value -> viewModel.setArchived(chat, value) },
                            )
                            HorizontalDivider(modifier = Modifier.padding(start = 80.dp))
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun ArchiveEntry(count: Int, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = 16.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = stringResource(R.string.chats_archived_count, count),
            style = MaterialTheme.typography.titleSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun ChatRow(
    chat: Chat,
    avatarUrl: String?,
    onClick: () -> Unit,
    onMute: (Long?) -> Unit,
    onTogglePin: () -> Unit,
    onSetArchived: (Boolean) -> Unit,
) {
    val title = chat.title.ifEmpty { stringResource(R.string.chat_untitled, chat.id) }
    var menuOpen by remember { mutableStateOf(false) }
    val muted = chat.flags.isMuted()

    Row(
        modifier = Modifier
            .fillMaxWidth()
            .combinedClickable(onClick = onClick, onLongClick = { menuOpen = true })
            .padding(horizontal = 16.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Avatar(label = title, imageUrl = avatarUrl)
        Column(
            modifier = Modifier
                .weight(1f)
                .padding(start = 16.dp),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                // The lock is the only thing that marks a secret chat in this list, and
                // that is the design: it is a chat, and a row that looked like a different
                // kind of object is what made the old bottom-sheet version something
                // nobody opened twice.
                if (chat.kind.isEndToEnd) {
                    Icon(
                        Icons.Default.Lock,
                        contentDescription = stringResource(R.string.secret_badge),
                        tint = MaterialTheme.colorScheme.primary,
                        modifier = Modifier.size(14.dp).padding(end = 4.dp),
                    )
                }
                Text(
                    text = title,
                    style = MaterialTheme.typography.titleMedium,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f, fill = false),
                )
                if (muted) {
                    Icon(
                        Icons.Default.NotificationsOff,
                        contentDescription = stringResource(R.string.chats_mute),
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.size(14.dp).padding(start = 4.dp),
                    )
                }
                if (chat.flags.pinned) {
                    Icon(
                        Icons.Default.PushPin,
                        contentDescription = stringResource(R.string.chats_pin),
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.size(14.dp).padding(start = 4.dp),
                    )
                }
            }
            val preview = chat.lastMessage?.text.orEmpty()
            Text(
                text = when {
                    preview == MessageIngestor.ATTACHMENT_PREVIEW ->
                        stringResource(R.string.chat_preview_attachment)
                    preview.isEmpty() -> stringResource(R.string.chat_preview_empty)
                    else -> preview
                },
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Column(
            horizontalAlignment = Alignment.End,
            verticalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            Text(
                text = formatTimestamp(chat.lastMessage?.timestamp ?: 0),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            if (chat.unreadCount > 0) {
                Box(
                    modifier = Modifier
                        .clip(CircleShape)
                        // Muted chats keep the count but lose the accent colour. The badge
                        // is still information — a muted chat is not an ignored one — and
                        // it must not compete with the chats actually asking for attention.
                        .background(
                            if (muted) {
                                MaterialTheme.colorScheme.outline
                            } else {
                                MaterialTheme.colorScheme.primary
                            },
                        )
                        .padding(horizontal = 7.dp, vertical = 2.dp),
                ) {
                    Text(
                        text = if (chat.unreadCount > 99) "99+" else chat.unreadCount.toString(),
                        color = MaterialTheme.colorScheme.onPrimary,
                        style = MaterialTheme.typography.labelSmall,
                        fontWeight = FontWeight.Bold,
                    )
                }
            }
        }

        DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
            DropdownMenuItem(
                text = {
                    Text(
                        stringResource(
                            if (chat.flags.pinned) R.string.chats_unpin else R.string.chats_pin,
                        ),
                    )
                },
                onClick = {
                    menuOpen = false
                    onTogglePin()
                },
            )
            if (muted) {
                DropdownMenuItem(
                    text = { Text(stringResource(R.string.chats_unmute)) },
                    onClick = {
                        menuOpen = false
                        onMute(null)
                    },
                )
            } else {
                MUTE_PRESETS.forEach { preset ->
                    DropdownMenuItem(
                        text = { Text(stringResource(preset.labelRes)) },
                        onClick = {
                            menuOpen = false
                            onMute(preset.ms)
                        },
                    )
                }
            }
            HorizontalDivider()
            DropdownMenuItem(
                text = {
                    Text(
                        stringResource(
                            if (chat.flags.archived) {
                                R.string.chats_unarchive
                            } else {
                                R.string.chats_archive
                            },
                        ),
                    )
                },
                onClick = {
                    menuOpen = false
                    onSetArchived(!chat.flags.archived)
                },
            )
        }
    }
}

/** One entry in the mute submenu. */
private data class MutePreset(@param:StringRes val labelRes: Int, val ms: Long)

/**
 * Mute durations, in the order the menu offers them.
 *
 * Durations rather than a switch, because the protocol carries a DEADLINE — and it does
 * because "mute for eight hours" is what muting almost always means.
 *
 * "Forever" is ten years rather than a distant absolute instant: long enough to read as
 * forever, and safely inside the ceiling the server clamps a deadline to. Computing it
 * from an absolute date would also mean subtracting `now` somewhere, which drifts.
 */
private val MUTE_PRESETS = listOf(
    MutePreset(R.string.chats_mute_hour, 60L * 60 * 1000),
    MutePreset(R.string.chats_mute_eight_hours, 8L * 60 * 60 * 1000),
    MutePreset(R.string.chats_mute_week, 7L * 24 * 60 * 60 * 1000),
    MutePreset(R.string.chats_mute_forever, 10L * 365 * 24 * 60 * 60 * 1000),
)
