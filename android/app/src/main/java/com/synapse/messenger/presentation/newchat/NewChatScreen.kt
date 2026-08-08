package com.synapse.messenger.presentation.newchat

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Switch
import androidx.compose.material3.Tab
import androidx.compose.material3.TabRow
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.synapse.messenger.R
import com.synapse.messenger.domain.model.UserSummary
import com.synapse.messenger.presentation.components.Avatar
import com.synapse.messenger.presentation.components.localized

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NewChatScreen(
    onBack: () -> Unit,
    onOpenPeer: (String) -> Unit,
    onOpenChat: (String) -> Unit,
    viewModel: NewChatViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val contacts by viewModel.contacts.collectAsStateWithLifecycle()

    LaunchedEffect(Unit) {
        viewModel.destinations.collect { destination ->
            when (destination) {
                is NewChatDestination.ByPeer -> onOpenPeer(destination.username)
                is NewChatDestination.ByChatId -> onOpenChat(destination.chatId)
            }
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.new_chat_title)) },
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
            TabRow(selectedTabIndex = state.tab.ordinal) {
                NewChatTab.entries.forEach { tab ->
                    Tab(
                        selected = state.tab == tab,
                        onClick = { viewModel.onTabChange(tab) },
                        text = {
                            Text(
                                stringResource(
                                    when (tab) {
                                        NewChatTab.DIRECT -> R.string.new_chat_tab_direct
                                        NewChatTab.GROUP -> R.string.new_chat_tab_group
                                        NewChatTab.JOIN -> R.string.new_chat_tab_join
                                    },
                                ),
                            )
                        },
                    )
                }
            }

            Column(modifier = Modifier.padding(16.dp)) {
                when (state.tab) {
                    NewChatTab.DIRECT -> DirectTab(
                        username = state.username,
                        busy = state.busy,
                        onUsernameChange = viewModel::onUsernameChange,
                        onSubmit = viewModel::findByUsername,
                    )
                    NewChatTab.GROUP -> GroupTab(
                        title = state.groupTitle,
                        members = state.groupMembers,
                        asChannel = state.asChannel,
                        busy = state.busy,
                        onTitleChange = viewModel::onGroupTitleChange,
                        onMembersChange = viewModel::onGroupMembersChange,
                        onAsChannelChange = viewModel::onAsChannelChange,
                        onSubmit = viewModel::createGroupChat,
                    )
                    NewChatTab.JOIN -> JoinTab(
                        code = state.inviteCode,
                        busy = state.busy,
                        onCodeChange = viewModel::onInviteCodeChange,
                        onSubmit = viewModel::join,
                    )
                }

                state.error?.let { error ->
                    Text(
                        text = error.localized(),
                        color = MaterialTheme.colorScheme.error,
                        style = MaterialTheme.typography.bodySmall,
                        modifier = Modifier.padding(top = 12.dp),
                    )
                }
            }

            if (state.tab == NewChatTab.DIRECT && contacts.isNotEmpty()) {
                HorizontalDivider()
                Text(
                    text = stringResource(R.string.new_chat_known_people),
                    style = MaterialTheme.typography.labelLarge,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(start = 16.dp, top = 12.dp, bottom = 4.dp),
                )
                LazyColumn {
                    items(contacts, key = { it.userId }) { contact ->
                        ContactRow(contact = contact, onClick = { viewModel.openContact(contact) })
                    }
                }
            }
        }
    }
}

@Composable
private fun DirectTab(
    username: String,
    busy: Boolean,
    onUsernameChange: (String) -> Unit,
    onSubmit: () -> Unit,
) {
    Column {
        OutlinedTextField(
            value = username,
            onValueChange = onUsernameChange,
            label = { Text(stringResource(R.string.new_chat_username_label)) },
            prefix = { Text("@") },
            singleLine = true,
            enabled = !busy,
            supportingText = { Text(stringResource(R.string.new_chat_username_hint)) },
            modifier = Modifier.fillMaxWidth(),
        )
        Spacer(Modifier.height(12.dp))
        SubmitButton(
            text = stringResource(R.string.new_chat_find),
            busy = busy,
            enabled = username.isNotBlank(),
            onClick = onSubmit,
        )
    }
}

@Composable
private fun GroupTab(
    title: String,
    members: String,
    asChannel: Boolean,
    busy: Boolean,
    onTitleChange: (String) -> Unit,
    onMembersChange: (String) -> Unit,
    onAsChannelChange: (Boolean) -> Unit,
    onSubmit: () -> Unit,
) {
    Column {
        OutlinedTextField(
            value = title,
            onValueChange = onTitleChange,
            label = { Text(stringResource(R.string.new_chat_group_title)) },
            singleLine = true,
            enabled = !busy,
            modifier = Modifier.fillMaxWidth(),
        )
        Spacer(Modifier.height(12.dp))
        OutlinedTextField(
            value = members,
            onValueChange = onMembersChange,
            label = { Text(stringResource(R.string.new_chat_group_members)) },
            enabled = !busy,
            supportingText = { Text(stringResource(R.string.new_chat_group_members_hint)) },
            modifier = Modifier.fillMaxWidth(),
        )
        Spacer(Modifier.height(12.dp))
        Row(verticalAlignment = Alignment.CenterVertically) {
            Switch(checked = asChannel, onCheckedChange = onAsChannelChange, enabled = !busy)
            Column(modifier = Modifier.padding(start = 12.dp)) {
                Text(stringResource(R.string.new_chat_as_channel))
                Text(
                    text = stringResource(R.string.new_chat_as_channel_hint),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        Spacer(Modifier.height(12.dp))
        SubmitButton(
            text = stringResource(R.string.new_chat_create),
            busy = busy,
            enabled = title.isNotBlank(),
            onClick = onSubmit,
        )
    }
}

@Composable
private fun JoinTab(
    code: String,
    busy: Boolean,
    onCodeChange: (String) -> Unit,
    onSubmit: () -> Unit,
) {
    Column {
        OutlinedTextField(
            value = code,
            onValueChange = onCodeChange,
            label = { Text(stringResource(R.string.new_chat_invite_label)) },
            singleLine = true,
            enabled = !busy,
            supportingText = { Text(stringResource(R.string.new_chat_invite_hint)) },
            modifier = Modifier.fillMaxWidth(),
        )
        Spacer(Modifier.height(12.dp))
        SubmitButton(
            text = stringResource(R.string.new_chat_join),
            busy = busy,
            enabled = code.isNotBlank(),
            onClick = onSubmit,
        )
    }
}

@Composable
private fun SubmitButton(text: String, busy: Boolean, enabled: Boolean, onClick: () -> Unit) {
    Button(onClick = onClick, enabled = enabled && !busy, modifier = Modifier.fillMaxWidth()) {
        if (busy) {
            CircularProgressIndicator(
                modifier = Modifier.size(18.dp),
                strokeWidth = 2.dp,
                color = MaterialTheme.colorScheme.onPrimary,
            )
        } else {
            Text(text)
        }
    }
}

@Composable
private fun ContactRow(contact: UserSummary, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = 16.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Avatar(label = contact.displayLabel, size = 40.dp)
        Column {
            Text(contact.displayLabel, style = MaterialTheme.typography.bodyLarge)
            contact.username?.let {
                Text(
                    text = "@$it",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}
