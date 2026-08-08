package com.synapse.messenger.presentation.navigation

import android.net.Uri

/**
 * Routes.
 *
 * The chat route takes an OPTIONAL chat id and an optional peer handle, because a
 * direct chat genuinely may not have an id yet: the gateway creates it when the
 * first message addressed to `"@username"` lands. Encoding that in the route keeps
 * the "not yet resolved" case out of every screen below it.
 */
object Routes {
    const val AUTH = "auth"
    const val CHATS = "chats"
    const val NEW_CHAT = "new_chat"
    const val SETTINGS = "settings"

    const val CHAT_ARG_ID = "chatId"
    const val CHAT_ARG_PEER = "peer"
    const val CHAT = "chat?$CHAT_ARG_ID={$CHAT_ARG_ID}&$CHAT_ARG_PEER={$CHAT_ARG_PEER}"

    fun chatById(chatId: String): String = "chat?$CHAT_ARG_ID=${Uri.encode(chatId)}&$CHAT_ARG_PEER="

    fun chatByPeer(username: String): String =
        "chat?$CHAT_ARG_ID=&$CHAT_ARG_PEER=${Uri.encode(username.removePrefix("@"))}"

    /** Matches the deep link a push notification opens. */
    const val CHAT_DEEP_LINK = "synapse://chat/{$CHAT_ARG_ID}"
}
