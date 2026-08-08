import Foundation
import SynapseDomain
import SynapseNetwork

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
    /// This is the workaround for the protocol's one real gap: there is no
    /// "list my chats" message, and `ListUserChats` exists in the store but was
    /// never given a wire type. So the chat list is assembled locally from
    /// everything that mentions a chat — inbound messages, send acks, chat
    /// creations, joins — and persisted. A brand-new device therefore starts
    /// empty and fills in as traffic arrives; see README for the two ways to
    /// close that gap properly.
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

    static func date(millis: Int64) -> Date? {
        guard millis > 0 else { return nil }
        return Date(timeIntervalSince1970: Double(millis) / 1000)
    }
}
// `String.nilIfEmpty` comes from SynapseNetwork — defined once, not twice.
