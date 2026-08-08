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
        case .unknown: return "UNKNOWN"
        }
    }
}
