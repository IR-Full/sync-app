package com.syncapp.messenger.network.protocol

/**
 * Envelope message types — mirrors `server/pkg/wire/constants.go`.
 *
 * The numbers are the protocol; never renumber them. Every type the gateway
 * dispatches is listed, so a frame arriving here always has a name — the
 * protocol's rule is that an unknown type is skipped, and a skipped frame that
 * turns out to matter is the hardest kind of bug to see.
 *
 * Listed is not the same as used. Some of these the app sends and handles today;
 * others exist so an inbound frame decodes and can be logged rather than
 * silently dropped. Where a group is protocol-only, the comment says so.
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

    const val TRANSPORT_ACK = 30
    const val RESUME = 31
    const val RESUME_OK = 32
    const val ERROR = 40

    const val SEARCH = 60
    const val SEARCH_RESULTS = 61

    /** Owner/admin export of a chat's members and messages, streamed in pages. */
    const val CHAT_EXPORT = 70
    const val CHAT_EXPORT_RESULT = 71

    const val REACT = 80
    const val REACT_UPD = 81

    /** Replies under a root message, paged like HISTORY. */
    const val THREAD = 82
    const val THREAD_OK = 83

    // Polls. Protocol-level only in this client: no composer and no result view.
    const val POLL_CREATE = 84
    const val POLL_VOTE = 85
    const val POLL_CLOSE = 86
    const val POLL_STATE = 87

    // Call signalling. Protocol-level only: the media half is WebRTC, which this
    // client does not carry, and signalling without it would ring for nothing.
    const val CALL_INVITE = 90
    const val CALL_ACCEPT = 91
    const val CALL_DECLINE = 92
    const val CALL_HANGUP = 93
    const val CALL_STATE = 94
    const val CALL_SIGNAL = 95

    const val CONTACT_ADD = 96
    const val CONTACT_REMOVE = 97
    const val CONTACT_SYNC = 98
    const val CONTACT_LIST = 99
    const val BLOCK = 100

    /** Copies a message into another chat, keeping a note of where it came from. */
    const val FORWARD = 101

    // Scheduled sending.
    const val SCHEDULE = 102
    const val SCHEDULE_LIST = 103
    const val SCHEDULE_CANCEL = 104
    const val SCHEDULED = 105

    // Pins: shared per chat, and always delivered as the complete set.
    const val PIN = 106
    const val UNPIN = 107
    const val PIN_LIST = 108
    const val PINNED = 109

    // Drafts: private to the user, shared across that user's devices.
    const val DRAFT_SET = 110
    const val DRAFT_SYNC = 111
    const val DRAFTS = 112

    const val SET_USERNAME = 113
    const val INVITE_CREATE = 114
    const val INVITE_REVOKE = 115
    const val INVITE_LIST = 116
    const val JOIN = 117
    const val SET_ROLE = 118
    const val INVITES = 119
    const val CHAT_CREATE = 120
    const val CHAT_INFO = 121
    const val PUSH_TOKEN = 122

    /** Paged list of the chats the caller belongs to — the chat list's real source. */
    const val CHAT_LIST = 123
    const val CHATS = 124

    /** PROFILE_GET also takes `"@handle"`, which makes it the user lookup. */
    const val PROFILE_GET = 125
    const val PROFILE_SET = 126
    const val PROFILE = 127

    /**
     * A message of ours reached a recipient's device.
     *
     * Raised by the gateway that actually wrote the frame to their socket, so it
     * means the bytes left the server — not merely that a node was notified. The
     * body is a [ReadUpdate]: `(chat_id, user_id, up_to_chat_seq)` is exactly a
     * delivery cursor, and reusing a body under a distinct type is this protocol's
     * own convention (KEY_FETCH_ALL reuses KeyFetch, PIN/UNPIN/PIN_LIST reuse
     * PinAction).
     */
    const val DELIVERED = 128

    /**
     * Erases the account. Re-checks the password over an already-authenticated
     * socket, because a session token lives on the device and someone holding an
     * unlocked phone must not be able to destroy the account behind it.
     */
    const val ACCOUNT_DELETE = 129
    const val ACCOUNT_DELETED = 130

    // Session management. Before these existed, "log out" only discarded the
    // token on the device while the session stayed valid until it expired.
    const val SESSION_LIST = 131
    const val SESSIONS = 132
    const val SESSION_REVOKE = 133
    const val SESSION_REVOKED = 134

    /**
     * One backfill page in a single frame, for peers that negotiated batching.
     *
     * A client that does not understand it still receives the per-message NEW
     * stream, which is why the capability bit exists.
     */
    const val HISTORY_PAGE = 135

    // Who may see last-seen, the avatar, and who may add this user to groups.
    const val PRIVACY_GET = 136
    const val PRIVACY_SET = 137
    const val PRIVACY = 138

    /**
     * Durable secret chats.
     *
     * SECRET_SEND used to be a pure relay: the server published to whichever nodes
     * held the recipient and discarded the count, so a message sent while the peer
     * was offline was dropped — nothing stored, no push, and no reply to the sender,
     * which drew "sent" regardless. These four make it survivable: SECRET_ACK says
     * what the relay actually did, SECRET_SYNC collects what this device missed, and
     * SECRET_ACKED confirms so the server can drop the row.
     */
    const val SECRET_ACK = 139
    const val SECRET_SYNC = 140
    const val SECRET_SYNCED = 141
    const val SECRET_ACKED = 142

    /**
     * Per-member chat settings.
     *
     * `muted` existed in the server schema from its first migration with nothing
     * reading it and no message to set it, so muting a chat was impossible while
     * looking supported from every other angle. Pin and archive are the other two a
     * chat list needs and never had.
     */
    const val CHAT_FLAGS = 143
    const val CHAT_FLAGS_SET = 144

    /**
     * Account security.
     *
     * The password could not be CHANGED by any path, so a leaked one meant a
     * permanently lost account — revoking sessions does not stop whoever knows the
     * password from signing in again. And there was no second factor at all.
     */
    const val PASSWORD_CHANGE = 145
    const val PASSWORD_CHANGED = 146
    const val TOTP_SETUP = 147
    const val TOTP_SETUP_INFO = 148
    const val TOTP_CONFIRM = 149
    const val TOTP_DISABLE = 150
    const val TOTP_STATE = 151

    /**
     * Billing.
     *
     * SUBSCRIPTION is both a reply and a server PUSH: a subscription changes without
     * the client asking — a payment settles, a period lapses — and until the client
     * hears about it it goes on offering features the server has started refusing.
     */
    const val BILLING_PLANS = 152
    const val BILLING_OFFERS = 153
    const val BILLING_CHECKOUT = 154
    const val BILLING_PAYMENT = 155
    const val BILLING_STATUS = 156
    const val SUBSCRIPTION = 157
    const val BILLING_CANCEL = 158

    // Secret (end-to-end encrypted) chats. The server stores only public prekeys
    // and relays opaque ciphertext; it can derive no key and read no message.
    const val KEY_PUBLISH = 50
    const val KEY_FETCH = 51
    const val KEY_BUNDLE = 52
    const val SECRET_SEND = 53
    const val SECRET_RECV = 54

    /** Fetches EVERY device bundle for a user — secret chats are per device. */
    const val KEY_FETCH_ALL = 55
    const val KEY_BUNDLES = 56

    /**
     * Answers KEY_PUBLISH with what the directory now holds for this device: prekeys
     * left, the age of the stored signed prekey, and how many of the keys just sent
     * were kept.
     *
     * It is the only channel for that. One-time prekeys are consumed by PEERS fetching
     * bundles, so a device cannot watch its own balance fall - the local pool shrinks
     * only when a message decrypts with a key, which misses every fetch that never
     * became a message. Without this the directory empties while the device believes it
     * is full, and X3DH silently drops from four Diffie-Hellmans to three for every
     * session started afterwards.
     */
    const val KEY_STATE = 159

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
        // "T_ACK", not "TRANSPORT_ACK". The name is the server's to choose — it is
        // what `MsgType.String()` in server/pkg/wire/types.go returns, and what the
        // gateway writes into its logs and metrics labels. This client said
        // TRANSPORT_ACK, so the one type whose whole job is invisible bookkeeping was
        // also the one that could not be correlated across a client and server log.
        // The web and iOS ports already agreed with the server.
        TRANSPORT_ACK -> "T_ACK"
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
        CHAT_LIST -> "CHAT_LIST"
        CHATS -> "CHATS"
        PROFILE_GET -> "PROFILE_GET"
        PROFILE_SET -> "PROFILE_SET"
        PROFILE -> "PROFILE"
        DELIVERED -> "DELIVERED"
        CHAT_EXPORT -> "CHAT_EXPORT"
        CHAT_EXPORT_RESULT -> "CHAT_EXPORT_RESULT"
        REACT -> "REACT"
        REACT_UPD -> "REACT_UPD"
        THREAD -> "THREAD"
        THREAD_OK -> "THREAD_OK"
        POLL_CREATE -> "POLL_CREATE"
        POLL_VOTE -> "POLL_VOTE"
        POLL_CLOSE -> "POLL_CLOSE"
        POLL_STATE -> "POLL_STATE"
        CALL_INVITE -> "CALL_INVITE"
        CALL_ACCEPT -> "CALL_ACCEPT"
        CALL_DECLINE -> "CALL_DECLINE"
        CALL_HANGUP -> "CALL_HANGUP"
        CALL_STATE -> "CALL_STATE"
        CALL_SIGNAL -> "CALL_SIGNAL"
        FORWARD -> "FORWARD"
        SCHEDULE -> "SCHEDULE"
        SCHEDULE_LIST -> "SCHEDULE_LIST"
        SCHEDULE_CANCEL -> "SCHEDULE_CANCEL"
        SCHEDULED -> "SCHEDULED"
        PIN -> "PIN"
        UNPIN -> "UNPIN"
        PIN_LIST -> "PIN_LIST"
        PINNED -> "PINNED"
        DRAFT_SET -> "DRAFT_SET"
        DRAFT_SYNC -> "DRAFT_SYNC"
        DRAFTS -> "DRAFTS"
        SET_USERNAME -> "SET_USERNAME"
        INVITE_CREATE -> "INVITE_CREATE"
        INVITE_REVOKE -> "INVITE_REVOKE"
        INVITE_LIST -> "INVITE_LIST"
        SET_ROLE -> "SET_ROLE"
        ACCOUNT_DELETE -> "ACCOUNT_DELETE"
        ACCOUNT_DELETED -> "ACCOUNT_DELETED"
        SESSION_LIST -> "SESSION_LIST"
        SESSIONS -> "SESSIONS"
        SESSION_REVOKE -> "SESSION_REVOKE"
        SESSION_REVOKED -> "SESSION_REVOKED"
        HISTORY_PAGE -> "HISTORY_PAGE"
        PRIVACY_GET -> "PRIVACY_GET"
        PRIVACY_SET -> "PRIVACY_SET"
        PRIVACY -> "PRIVACY"
        KEY_PUBLISH -> "KEY_PUBLISH"
        KEY_FETCH -> "KEY_FETCH"
        KEY_BUNDLE -> "KEY_BUNDLE"
        SECRET_SEND -> "SECRET_SEND"
        SECRET_RECV -> "SECRET_RECV"
        KEY_FETCH_ALL -> "KEY_FETCH_ALL"
        KEY_BUNDLES -> "KEY_BUNDLES"
        KEY_STATE -> "KEY_STATE"
        SECRET_ACK -> "SECRET_ACK"
        SECRET_SYNC -> "SECRET_SYNC"
        SECRET_SYNCED -> "SECRET_SYNCED"
        SECRET_ACKED -> "SECRET_ACKED"
        CHAT_FLAGS -> "CHAT_FLAGS"
        CHAT_FLAGS_SET -> "CHAT_FLAGS_SET"
        PASSWORD_CHANGE -> "PASSWORD_CHANGE"
        PASSWORD_CHANGED -> "PASSWORD_CHANGED"
        TOTP_SETUP -> "TOTP_SETUP"
        TOTP_SETUP_INFO -> "TOTP_SETUP_INFO"
        TOTP_CONFIRM -> "TOTP_CONFIRM"
        TOTP_DISABLE -> "TOTP_DISABLE"
        TOTP_STATE -> "TOTP_STATE"
        BILLING_PLANS -> "BILLING_PLANS"
        BILLING_OFFERS -> "BILLING_OFFERS"
        BILLING_CHECKOUT -> "BILLING_CHECKOUT"
        BILLING_PAYMENT -> "BILLING_PAYMENT"
        BILLING_STATUS -> "BILLING_STATUS"
        SUBSCRIPTION -> "SUBSCRIPTION"
        BILLING_CANCEL -> "BILLING_CANCEL"
        else -> "UNKNOWN($type)"
    }
}
