import Foundation

/// Envelope message types, mirroring `server/pkg/wire/constants.go` exactly.
///
/// The numbering is deliberately sparse and grouped by feature block (20s media,
/// 50s secret chats, 60s search, 80s message-layer product features, 90s calls,
/// 100s+ social/membership), so new features get a block instead of squeezing in
/// next to unrelated types. Direction is documented, not encoded — the wire
/// format is symmetric.
public enum MsgType: UInt16, Sendable, CaseIterable {
    // Handshake & capability negotiation.
    case hello = 1        // C→S
    case welcome = 2      // S→C

    // Authentication.
    case auth = 3         // C→S
    case authOK = 4       // S→C
    case authErr = 5      // S→C

    // Liveness.
    case ping = 6
    case pong = 7

    // Messaging.
    case send = 8         // C→S
    case sendAck = 9      // S→C
    case new = 10         // S→C (live fanout *and* history replay)
    case read = 11        // C→S
    case readUpd = 12     // S→C
    case typing = 13      // both
    case presence = 14    // S→C
    case edit = 15        // C→S
    case delete = 16      // C→S
    case history = 17     // C→S
    case historyOK = 18   // S→C

    // Media (bytes travel over HTTP; only refs and signed URLs travel here).
    case mediaInit = 20
    case mediaTicket = 21
    case mediaFetch = 22
    case mediaURL = 23

    // Transport control.
    case transportAck = 30
    case resume = 31
    case resumeOK = 32
    case error = 40

    // Secret (end-to-end encrypted) chats.
    case keyPublish = 50
    case keyFetch = 51
    case keyBundle = 52
    case secretSend = 53
    case secretRecv = 54
    case keyFetchAll = 55
    case keyBundles = 56

    // Search.
    case search = 60
    case searchResults = 61

    // Admin / owner.
    case chatExport = 70
    case chatExportResult = 71

    // Reactions & threads.
    case react = 80
    case reactUpd = 81
    case thread = 82
    case threadOK = 83

    // Polls.
    case pollCreate = 84
    case pollVote = 85
    case pollClose = 86
    case pollState = 87

    // Calls (signaling only — media is peer-to-peer).
    case callInvite = 90
    case callAccept = 91
    case callDecline = 92
    case callHangup = 93
    case callState = 94
    case callSignal = 95

    // Contacts & blocking.
    case contactAdd = 96
    case contactRemove = 97
    case contactSync = 98
    case contactList = 99
    case block = 100

    // Forwarding, scheduling, self-destruct.
    case forward = 101
    case schedule = 102
    case scheduleList = 103
    case scheduleCancel = 104
    case scheduled = 105

    // Pins (chat-wide) and drafts (private, synced across your own devices).
    case pin = 106
    case unpin = 107
    case pinList = 108
    case pinned = 109
    case draftSet = 110
    case draftSync = 111
    case drafts = 112

    // Public handles, invite links, roles.
    case setUsername = 113
    case inviteCreate = 114
    case inviteRevoke = 115
    case inviteList = 116
    case join = 117
    case setRole = 118
    case invites = 119

    // Chat creation.
    case chatCreate = 120
    case chatInfo = 121

    // Push registration.
    case pushToken = 122

    // Chat list: the only message that tells a fresh install which chats it is
    // in. Everything else about a chat is learned as a consequence of traffic.
    case chatList = 123
    case chats = 124

    // Profiles. `profileGet` also takes "@handle", which makes it the user
    // lookup — there is no directory and no prefix search.
    case profileGet = 125
    case profileSet = 126
    case profile = 127

    /// A message of ours reached a recipient's device — the step between
    /// "stored" and "read". Pushed unsolicited; the body is a `ReadUpdateBody`,
    /// because (chat, user, up-to-seq) is exactly a delivery cursor.
    case delivered = 128

    /// Erase this account. The password is re-confirmed even though the socket
    /// is already authenticated: a session token lives on the device, so
    /// otherwise anyone holding an unlocked phone could destroy the account
    /// behind it.
    case accountDelete = 129
    /// The erasure is done. Every session was revoked before this was sent, so
    /// it is the last frame the connection carries.
    case accountDeleted = 130

    // Session management. The auth service could always list and revoke
    // sessions; nothing could ask it to, so "log out" was a purely local gesture
    // — the device forgot its token while the session stayed valid until it
    // expired, and a lost phone kept access for the whole TTL. These four close
    // that, and they were the last part of the protocol this platform did not
    // speak.
    case sessionList = 131
    case sessions = 132
    case sessionRevoke = 133
    case sessionRevoked = 134

    /// A page of history as a SINGLE frame, replacing up to a hundred `NEW`
    /// envelopes for one request. Sent only to peers that negotiated
    /// `CAP_BATCHING`, so the per-message stream remains the fallback and both
    /// shapes carry exactly the same messages.
    case historyPage = 135

    // Per-user privacy: who may see last-seen and the avatar, who may add this
    // account to a group, and whether message text may reach the push provider.
    case privacyGet = 136
    case privacySet = 137
    case privacy = 138

    /// Durable secret chats.
    ///
    /// `secretSend` was a pure relay: the gateway published to whichever nodes held
    /// the recipient and discarded the count, so a message sent while the peer was
    /// offline was dropped — nothing stored, no push, and no reply to the sender,
    /// which drew "sent" regardless. These four make it survivable.
    case secretAck = 139
    case secretSync = 140
    case secretSynced = 141
    case secretAcked = 142

    /// Per-member chat settings.
    ///
    /// `muted` existed in the server schema from its first migration with nothing
    /// reading it and no message to set it, so muting a chat was impossible while
    /// looking supported from every other angle.
    case chatFlags = 143
    case chatFlagsSet = 144

    /// Account security. The password could not be CHANGED by any path — so a leaked
    /// one meant a permanently lost account — and there was no second factor at all.
    case passwordChange = 145
    case passwordChanged = 146
    case totpSetup = 147
    case totpSetupInfo = 148
    case totpConfirm = 149
    case totpDisable = 150
    case totpState = 151

    /// Billing. `subscription` is both a reply and a server PUSH: it changes without
    /// the client asking, and until the client hears about it it goes on offering
    /// features the server has started refusing.
    case billingPlans = 152
    case billingOffers = 153
    case billingCheckout = 154
    case billingPayment = 155
    case billingStatus = 156
    case subscription = 157
    case billingCancel = 158

    /// `KEY_STATE` answers `KEY_PUBLISH` with what the directory now holds for this
    /// device: prekeys left, the age of the stored signed prekey, and how many of the
    /// keys just sent were kept.
    ///
    /// It is the only channel for that information. One-time prekeys are consumed by
    /// PEERS fetching bundles, so a device cannot watch its own balance fall - the local
    /// count drops only when a message decrypts with a key, which misses every fetch
    /// that never became a message. Without this the directory empties while the device
    /// believes it is full, and every session started afterwards silently uses three
    /// Diffie-Hellmans instead of four.
    case keyState = 159

    /// Anything this client build does not know about. Received unknown types are
    /// skipped, never treated as an error.
    case unknown = 65535

    public var name: String {
        switch self {
        case .hello: return "HELLO"
        case .welcome: return "WELCOME"
        case .auth: return "AUTH"
        case .authOK: return "AUTH_OK"
        case .authErr: return "AUTH_ERR"
        case .ping: return "PING"
        case .pong: return "PONG"
        case .send: return "SEND"
        case .sendAck: return "SEND_ACK"
        case .new: return "NEW"
        case .read: return "READ"
        case .readUpd: return "READ_UPD"
        case .typing: return "TYPING"
        case .presence: return "PRESENCE"
        case .edit: return "EDIT"
        case .delete: return "DELETE"
        case .history: return "HISTORY"
        case .historyOK: return "HISTORY_OK"
        case .mediaInit: return "MEDIA_INIT"
        case .mediaTicket: return "MEDIA_TICKET"
        case .mediaFetch: return "MEDIA_FETCH"
        case .mediaURL: return "MEDIA_URL"
        case .transportAck: return "T_ACK"
        case .resume: return "RESUME"
        case .resumeOK: return "RESUME_OK"
        case .error: return "ERROR"
        case .keyPublish: return "KEY_PUBLISH"
        case .keyFetch: return "KEY_FETCH"
        case .keyBundle: return "KEY_BUNDLE"
        case .secretSend: return "SECRET_SEND"
        case .secretRecv: return "SECRET_RECV"
        case .keyFetchAll: return "KEY_FETCH_ALL"
        case .keyBundles: return "KEY_BUNDLES"
        case .search: return "SEARCH"
        case .searchResults: return "SEARCH_RESULTS"
        case .chatExport: return "CHAT_EXPORT"
        case .chatExportResult: return "CHAT_EXPORT_RESULT"
        case .react: return "REACT"
        case .reactUpd: return "REACT_UPD"
        case .thread: return "THREAD"
        case .threadOK: return "THREAD_OK"
        case .pollCreate: return "POLL_CREATE"
        case .pollVote: return "POLL_VOTE"
        case .pollClose: return "POLL_CLOSE"
        case .pollState: return "POLL_STATE"
        case .callInvite: return "CALL_INVITE"
        case .callAccept: return "CALL_ACCEPT"
        case .callDecline: return "CALL_DECLINE"
        case .callHangup: return "CALL_HANGUP"
        case .callState: return "CALL_STATE"
        case .callSignal: return "CALL_SIGNAL"
        case .contactAdd: return "CONTACT_ADD"
        case .contactRemove: return "CONTACT_REMOVE"
        case .contactSync: return "CONTACT_SYNC"
        case .contactList: return "CONTACT_LIST"
        case .block: return "BLOCK"
        case .forward: return "FORWARD"
        case .schedule: return "SCHEDULE"
        case .scheduleList: return "SCHEDULE_LIST"
        case .scheduleCancel: return "SCHEDULE_CANCEL"
        case .scheduled: return "SCHEDULED"
        case .pin: return "PIN"
        case .unpin: return "UNPIN"
        case .pinList: return "PIN_LIST"
        case .pinned: return "PINNED"
        case .draftSet: return "DRAFT_SET"
        case .draftSync: return "DRAFT_SYNC"
        case .drafts: return "DRAFTS"
        case .setUsername: return "SET_USERNAME"
        case .inviteCreate: return "INVITE_CREATE"
        case .inviteRevoke: return "INVITE_REVOKE"
        case .inviteList: return "INVITE_LIST"
        case .join: return "JOIN"
        case .setRole: return "SET_ROLE"
        case .invites: return "INVITES"
        case .chatCreate: return "CHAT_CREATE"
        case .chatInfo: return "CHAT_INFO"
        case .pushToken: return "PUSH_TOKEN"
        case .chatList: return "CHAT_LIST"
        case .chats: return "CHATS"
        case .profileGet: return "PROFILE_GET"
        case .profileSet: return "PROFILE_SET"
        case .profile: return "PROFILE"
        case .delivered: return "DELIVERED"
        case .accountDelete: return "ACCOUNT_DELETE"
        case .accountDeleted: return "ACCOUNT_DELETED"
        case .sessionList: return "SESSION_LIST"
        case .sessions: return "SESSIONS"
        case .sessionRevoke: return "SESSION_REVOKE"
        case .sessionRevoked: return "SESSION_REVOKED"
        case .historyPage: return "HISTORY_PAGE"
        case .privacyGet: return "PRIVACY_GET"
        case .privacySet: return "PRIVACY_SET"
        case .privacy: return "PRIVACY"
        case .secretAck: return "SECRET_ACK"
        case .secretSync: return "SECRET_SYNC"
        case .secretSynced: return "SECRET_SYNCED"
        case .secretAcked: return "SECRET_ACKED"
        case .chatFlags: return "CHAT_FLAGS"
        case .chatFlagsSet: return "CHAT_FLAGS_SET"
        case .passwordChange: return "PASSWORD_CHANGE"
        case .passwordChanged: return "PASSWORD_CHANGED"
        case .totpSetup: return "TOTP_SETUP"
        case .totpSetupInfo: return "TOTP_SETUP_INFO"
        case .totpConfirm: return "TOTP_CONFIRM"
        case .totpDisable: return "TOTP_DISABLE"
        case .totpState: return "TOTP_STATE"
        case .billingPlans: return "BILLING_PLANS"
        case .billingOffers: return "BILLING_OFFERS"
        case .billingCheckout: return "BILLING_CHECKOUT"
        case .billingPayment: return "BILLING_PAYMENT"
        case .billingStatus: return "BILLING_STATUS"
        case .subscription: return "SUBSCRIPTION"
        case .billingCancel: return "BILLING_CANCEL"
        case .keyState: return "KEY_STATE"
        case .unknown: return "UNKNOWN"
        }
    }
}
