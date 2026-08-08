package com.synapse.messenger.network.protocol

/**
 * Envelope message types — mirrors `server/pkg/wire/constants.go`.
 *
 * The numbers are the protocol; never renumber them. Only the subset this client
 * actually speaks is listed: the protocol's extensibility rule is that a peer
 * which does not understand a type skips it, so omissions are safe.
 */
object MsgType {
    const val HELLO = 1
    const val WELCOME = 2
    const val AUTH = 3
    const val AUTH_OK = 4
    const val AUTH_ERR = 5
    const val PING = 6
    const val PONG = 7
    const val SEND = 8
    const val SEND_ACK = 9
    const val NEW = 10
    const val READ = 11
    const val READ_UPD = 12
    const val TYPING = 13
    const val PRESENCE = 14
    const val EDIT = 15
    const val DELETE = 16
    const val HISTORY = 17
    const val HISTORY_OK = 18

    const val MEDIA_INIT = 20
    const val MEDIA_TICKET = 21
    const val MEDIA_FETCH = 22
    const val MEDIA_URL = 23

    const val T_ACK = 30
    const val RESUME = 31
    const val RESUME_OK = 32
    const val ERROR = 40

    const val SEARCH = 60
    const val SEARCH_RESULTS = 61

    const val CONTACT_ADD = 96
    const val CONTACT_REMOVE = 97
    const val CONTACT_SYNC = 98
    const val CONTACT_LIST = 99
    const val BLOCK = 100

    const val JOIN = 117
    const val INVITES = 119
    const val CHAT_CREATE = 120
    const val CHAT_INFO = 121
    const val PUSH_TOKEN = 122

    fun name(type: Int): String = when (type) {
        HELLO -> "HELLO"
        WELCOME -> "WELCOME"
        AUTH -> "AUTH"
        AUTH_OK -> "AUTH_OK"
        AUTH_ERR -> "AUTH_ERR"
        PING -> "PING"
        PONG -> "PONG"
        SEND -> "SEND"
        SEND_ACK -> "SEND_ACK"
        NEW -> "NEW"
        READ -> "READ"
        READ_UPD -> "READ_UPD"
        TYPING -> "TYPING"
        PRESENCE -> "PRESENCE"
        EDIT -> "EDIT"
        DELETE -> "DELETE"
        HISTORY -> "HISTORY"
        HISTORY_OK -> "HISTORY_OK"
        MEDIA_INIT -> "MEDIA_INIT"
        MEDIA_TICKET -> "MEDIA_TICKET"
        MEDIA_FETCH -> "MEDIA_FETCH"
        MEDIA_URL -> "MEDIA_URL"
        T_ACK -> "T_ACK"
        RESUME -> "RESUME"
        RESUME_OK -> "RESUME_OK"
        ERROR -> "ERROR"
        SEARCH -> "SEARCH"
        SEARCH_RESULTS -> "SEARCH_RESULTS"
        CONTACT_ADD -> "CONTACT_ADD"
        CONTACT_REMOVE -> "CONTACT_REMOVE"
        CONTACT_SYNC -> "CONTACT_SYNC"
        CONTACT_LIST -> "CONTACT_LIST"
        BLOCK -> "BLOCK"
        JOIN -> "JOIN"
        INVITES -> "INVITES"
        CHAT_CREATE -> "CHAT_CREATE"
        CHAT_INFO -> "CHAT_INFO"
        PUSH_TOKEN -> "PUSH_TOKEN"
        else -> "UNKNOWN($type)"
    }
}
