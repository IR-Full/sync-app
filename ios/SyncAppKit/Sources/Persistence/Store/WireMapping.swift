import Foundation
import SyncAppDomain
import SyncAppNetwork

/// Translation between wire bodies and domain entities.
///
/// This is the only file in the app that knows both vocabularies, which is the
/// point: rename a protobuf field and exactly one file needs editing.
enum WireMapping {

    static func message(from body: NewMessageBody, ourUserID: String) -> Message {
        Message(
            id: body.messageID,
            chatID: body.chatID,
            senderID: body.senderID,
            seq: body.chatSeq,
            text: body.text,
            sentAt: date(millis: body.timestamp) ?? Date(),
            // Our own messages arriving back through fanout are already durable;
            // anything from someone else is, from our side, simply "sent".
            state: .sent,
            isEdited: body.edited,
            isDeleted: body.deleted,
            replyToID: body.replyTo.nilIfEmpty,
            attachment: attachment(from: body.attachment),
            forwardedFrom: body.forward.map {
                ForwardOrigin(chatID: $0.chatID, messageID: $0.messageID, senderID: $0.senderID)
            },
            expiresAt: date(millis: body.expiresAt),
            dedupKey: ""
        )
    }

    static func attachment(from body: AttachmentBody?) -> Attachment? {
        guard let body, !body.mediaRef.isEmpty else { return nil }
        return Attachment(
            kind: Attachment.Kind(rawValue: body.kind) ?? .unknown,
            mediaRef: body.mediaRef,
            filename: body.filename,
            mime: body.mime,
            size: body.size,
            durationMs: body.durationMs,
            waveform: body.waveform,
            width: body.width,
            height: body.height,
            thumbRef: body.thumbRef
        )
    }

    /// Domain → wire. Used when an outbox entry finally goes out.
    static func attachmentBody(from attachment: Attachment?) -> AttachmentBody? {
        guard let attachment else { return nil }
        var body = AttachmentBody()
        body.kind = attachment.kind.rawValue
        body.mediaRef = attachment.mediaRef
        body.filename = attachment.filename
        body.mime = attachment.mime
        body.size = attachment.size
        body.durationMs = attachment.durationMs
        body.waveform = attachment.waveform
        body.width = attachment.width
        body.height = attachment.height
        body.thumbRef = attachment.thumbRef
        return body
    }

    static func contact(from body: ContactBody) -> Contact {
        Contact(
            userID: body.userID,
            name: body.name,
            isBlocked: body.blocked,
            updatedAt: date(millis: body.updatedAt) ?? Date()
        )
    }

    static func chat(from body: ChatInfoBody) -> Chat {
        Chat(
            id: body.chatID,
            kind: Chat.Kind(rawValue: body.type) ?? .group,
            title: body.title,
            ownerID: body.ownerID
        )
    }

    /// The chat a message implies.
    ///
    /// Not the authoritative view — `chat(from: ChatSummaryBody)` is, and the sync
    /// engine fetches it on every connect. This keeps the list current *between*
    /// enumerations: a chat someone creates and writes into appears from the
    /// message itself rather than waiting for the next reconnect.
    ///
    /// A direct chat's title is unknowable here (no member list for 1:1), so it
    /// is left empty and the UI falls back to the peer's handle or id.
    static func impliedChat(from message: Message, ourUserID: String) -> Chat {
        let isFromUs = message.senderID == ourUserID
        return Chat(
            id: message.chatID,
            // We cannot tell a group from a direct chat by looking at a message.
            // Assuming `direct` and letting an explicit CHAT_INFO or a second
            // distinct sender correct it is the least-wrong default, because it
            // only ever mislabels *group* chats we learned about implicitly.
            kind: .direct,
            title: "",
            peerUserID: isFromUs ? nil : message.senderID,
            lastMessagePreview: message.text,
            lastMessageAt: message.sentAt,
            lastSeq: message.seq
        )
    }

    /// A chat as the gateway enumerates it (`CHAT_LIST` → `CHATS`).
    ///
    /// This is the authoritative view, unlike `impliedChat`: the type, title,
    /// owner, public handle and this account's role all come from the server
    /// instead of being guessed from a message. Fields the enumeration does not
    /// carry — the last message preview and our read cursor — are left to the
    /// caller to preserve, because the cache already knows them and an empty
    /// value here means "not included", not "empty".
    static func chat(from summary: ChatSummaryBody) -> Chat {
        Chat(
            id: summary.chatID,
            kind: Chat.Kind(rawValue: summary.type) ?? .group,
            title: summary.title,
            username: summary.username.nilIfEmpty,
            ownerID: summary.ownerID,
            peerUserID: summary.peerID.nilIfEmpty,
            // The preview and the timestamp now DO come with the enumeration, so the
            // note above about the caller preserving them applies only to the read
            // cursor. Before this, a client that wanted a preview had to call HISTORY
            // once per chat — the server-side N+1 moved onto the network and became a
            // round trip per row.
            lastMessagePreview: summary.lastMessage.map(preview(of:)) ?? "",
            lastMessageAt: date(millis: summary.lastMessage?.timestamp ?? 0),
            lastSeq: summary.lastSeq,
            mutedUntil: date(millis: summary.mutedUntil),
            isPinned: summary.pinned,
            isArchived: summary.archived,
            lastActivityAt: date(millis: summary.lastActivityAt)
        )
    }

    /// What a chat-list row shows for its newest message.
    ///
    /// An attachment has no text, and a row that renders as blank reads as a bug
    /// rather than as a photo — so the kind is named instead. Deleted messages are
    /// named too: the tombstone is what the peer sees, and hiding it would leave the
    /// preview showing text that no longer exists.
    static func preview(of body: NewMessageBody) -> String {
        if body.deleted { return "" }
        if !body.text.isEmpty { return body.text }
        guard let attachment = body.attachment, !attachment.mediaRef.isEmpty else { return "" }
        switch Attachment.Kind(rawValue: attachment.kind) ?? .unknown {
        case .image: return "\u{1F4F7}"
        case .video, .videoNote: return "\u{1F3AC}"
        case .voice: return "\u{1F3A4}"
        case .file, .unknown: return attachment.filename.isEmpty ? "\u{1F4C4}" : attachment.filename
        }
    }

    static func date(millis: Int64) -> Date? {
        guard millis > 0 else { return nil }
        return Date(timeIntervalSince1970: Double(millis) / 1000)
    }
}
// `String.nilIfEmpty` comes from SyncAppNetwork — defined once, not twice.
