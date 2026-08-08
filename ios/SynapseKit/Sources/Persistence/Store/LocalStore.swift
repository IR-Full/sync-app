import Foundation
import SynapseDomain

/// Typed access to the cache. Every read the app performs and every write the
/// sync engine applies goes through here, so the SQL lives in one file and the
/// mapping between rows and domain types lives next to it.
public struct LocalStore: Sendable {
    private let database: Database
    private let broker: ChangeBroker

    public init(database: Database, broker: ChangeBroker) {
        self.database = database
        self.broker = broker
    }

    // MARK: - Chats

    public func upsertChat(_ chat: Chat) async throws {
        // COALESCE on the excluded value keeps a partial update (say, a presence
        // push that only knows the peer) from blanking fields an earlier, richer
        // write already established.
        try await database.run(
            """
            INSERT INTO chats (id, kind, title, username, owner_id, peer_user_id,
                               last_message_preview, last_message_at, last_seq, last_read_seq, is_muted)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(id) DO UPDATE SET
                kind                 = excluded.kind,
                title                = CASE WHEN excluded.title <> '' THEN excluded.title ELSE chats.title END,
                username             = COALESCE(excluded.username, chats.username),
                owner_id             = CASE WHEN excluded.owner_id <> '' THEN excluded.owner_id ELSE chats.owner_id END,
                peer_user_id         = COALESCE(excluded.peer_user_id, chats.peer_user_id),
                last_seq             = MAX(excluded.last_seq, chats.last_seq),
                last_read_seq        = MAX(excluded.last_read_seq, chats.last_read_seq)
            """,
            [
                .text(chat.id), .text(chat.kind.rawValue), .text(chat.title),
                .optionalText(chat.username), .text(chat.ownerID), .optionalText(chat.peerUserID),
                .text(chat.lastMessagePreview), .date(chat.lastMessageAt),
                .uint(chat.lastSeq), .uint(chat.lastReadSeq), .bool(chat.isMuted),
            ]
        )
        await broker.notify(.chats)
    }

    public func chat(id: String) async throws -> Chat? {
        let rows = try await database.query("SELECT * FROM chats WHERE id = ?", [.text(id)])
        return rows.first.map(Self.chat(from:))
    }

    /// The list screen's query. Unread is derived, not stored: a receipt from
    /// another device moves `last_read_seq` and the badge follows, with no
    /// counter to drift.
    public func chatSummaries() async throws -> [ChatSummary] {
        let rows = try await database.query(
            """
            SELECT c.*,
                   (SELECT COUNT(*) FROM messages m
                     WHERE m.chat_id = c.id
                       AND m.seq > c.last_read_seq
                       AND m.sender_id <> ?
                       AND m.deleted = 0) AS unread,
                   (SELECT online FROM users u WHERE u.id = c.peer_user_id) AS peer_online
              FROM chats c
             WHERE c.hidden = 0
             ORDER BY c.last_message_at DESC NULLS LAST, c.id DESC
            """,
            [.text((try await currentUserID()) ?? "")]
        )
        return rows.map { row in
            ChatSummary(
                chat: Self.chat(from: row),
                unreadCount: Int(row.int("unread") ?? 0),
                isPeerOnline: row.bool("peer_online")
            )
        }
    }

    public func setChatMuted(_ chatID: String, muted: Bool) async throws {
        try await database.run(
            "UPDATE chats SET is_muted = ? WHERE id = ?", [.bool(muted), .text(chatID)]
        )
        await broker.notify(.chats)
    }

    public func setChatHidden(_ chatID: String, hidden: Bool) async throws {
        try await database.run(
            "UPDATE chats SET hidden = ? WHERE id = ?", [.bool(hidden), .text(chatID)]
        )
        await broker.notify(.chats)
    }

    public func markRead(chatID: String, upToSeq: UInt64) async throws {
        // MAX guards against a receipt arriving out of order: a read marker is a
        // high-water mark and must never move backwards.
        try await database.run(
            "UPDATE chats SET last_read_seq = MAX(last_read_seq, ?) WHERE id = ?",
            [.uint(upToSeq), .text(chatID)]
        )
        await broker.notify([.chats, .messages(chatID: chatID)])
    }

    // MARK: - Messages

    /// Applies a batch of messages plus their chats' derived columns in one
    /// transaction.
    public func upsertMessages(_ messages: [Message]) async throws {
        guard !messages.isEmpty else { return }

        var statements: [Database.Statement] = []
        for message in messages {
            statements.append(Self.upsertMessageStatement(message))
        }
        // Recompute each touched chat's tail from the rows themselves rather
        // than from whichever message happened to arrive last — a late history
        // page must not overwrite a newer preview.
        for chatID in Set(messages.map(\.chatID)) {
            statements.append(Database.Statement(
                """
                UPDATE chats
                   SET last_seq             = COALESCE((SELECT MAX(seq) FROM messages WHERE chat_id = ?), last_seq),
                       -- An attachment-only message has no text, and a chat row
                       -- reading "no messages yet" under a photo just sent is a
                       -- lie. The paperclip is locale-neutral, so it needs no
                       -- string table.
                       last_message_preview = COALESCE((SELECT CASE
                                                            WHEN text <> '' THEN text
                                                            WHEN attachment IS NOT NULL THEN '📎'
                                                            ELSE ''
                                                        END
                                                         FROM messages
                                                         WHERE chat_id = ? AND deleted = 0
                                                         ORDER BY seq DESC, sent_at DESC LIMIT 1), last_message_preview),
                       last_message_at      = COALESCE((SELECT MAX(sent_at) FROM messages WHERE chat_id = ?), last_message_at)
                 WHERE id = ?
                """,
                [.text(chatID), .text(chatID), .text(chatID), .text(chatID)]
            ))
        }

        try await database.transaction(statements)

        var topics: [ChangeBroker.Topic] = [.chats]
        for chatID in Set(messages.map(\.chatID)) {
            topics.append(ChangeBroker.Topic.messages(chatID: chatID))
        }
        await broker.notify(topics)
    }

    /// Newest `limit` messages, returned oldest-first so the view can append.
    public func messages(chatID: String, limit: Int = 200) async throws -> [Message] {
        let rows = try await database.query(
            """
            SELECT * FROM (
                SELECT * FROM messages WHERE chat_id = ?
                 ORDER BY seq DESC, sent_at DESC
                 LIMIT ?
            ) ORDER BY seq ASC, sent_at ASC
            """,
            [.text(chatID), .int(limit)]
        )
        return rows.map(Self.message(from:))
    }

    /// The paging cursor: the oldest `seq` we hold for a chat. Outbox rows have
    /// `seq = 0` and must not be mistaken for the start of history.
    public func oldestSeq(chatID: String) async throws -> UInt64 {
        let rows = try await database.query(
            "SELECT MIN(seq) AS oldest FROM messages WHERE chat_id = ? AND seq > 0",
            [.text(chatID)]
        )
        return rows.first?.uint("oldest") ?? 0
    }

    public func message(id: String) async throws -> Message? {
        let rows = try await database.query("SELECT * FROM messages WHERE id = ?", [.text(id)])
        return rows.first.map(Self.message(from:))
    }

    public func message(dedupKey: String) async throws -> Message? {
        guard !dedupKey.isEmpty else { return nil }
        let rows = try await database.query("SELECT * FROM messages WHERE dedup_key = ?", [.text(dedupKey)])
        return rows.first.map(Self.message(from:))
    }

    /// Promotes an optimistic row to its acked identity.
    ///
    /// The row's primary key changes from the dedup key to the server snowflake,
    /// which is the moment the message acquires a position in the ordering. Done
    /// as an UPDATE rather than delete+insert so a SwiftUI list keeps its
    /// identity and does not animate the message out and back in.
    public func confirmMessage(
        dedupKey: String, messageID: String, chatID: String, seq: UInt64, timestamp: Date
    ) async throws {
        try await database.transaction([
            // A duplicate ack (the same dedup key resolving to an id we already
            // hold) would violate the primary key, so clear the way first.
            Database.Statement(
                "DELETE FROM messages WHERE id = ? AND dedup_key <> ?",
                [.text(messageID), .text(dedupKey)]
            ),
            Database.Statement(
                """
                UPDATE messages
                   SET id = ?, chat_id = ?, seq = ?, sent_at = ?, state = 'sent'
                 WHERE dedup_key = ?
                """,
                [.text(messageID), .text(chatID), .uint(seq), .date(timestamp), .text(dedupKey)]
            ),
            Database.Statement("DELETE FROM outbox WHERE dedup_key = ?", [.text(dedupKey)]),
        ])
        await broker.notify([.chats, .messages(chatID: chatID)])
    }

    public func setMessageState(id: String, state: MessageState, chatID: String) async throws {
        try await database.run(
            "UPDATE messages SET state = ? WHERE id = ?", [.text(state.rawValue), .text(id)]
        )
        await broker.notify(.messages(chatID: chatID))
    }

    /// Marks everything the peer has read, up to `seq`, as read.
    public func applyReadReceipt(chatID: String, upToSeq: UInt64, ourUserID: String) async throws {
        try await database.run(
            """
            UPDATE messages SET state = 'read'
             WHERE chat_id = ? AND sender_id = ? AND seq <= ? AND seq > 0 AND state <> 'read'
            """,
            [.text(chatID), .text(ourUserID), .uint(upToSeq)]
        )
        await broker.notify(.messages(chatID: chatID))
    }

    public func applyReactions(chatID: String, messageID: String, counts: [String: Int]) async throws {
        let encoded = (try? JSONEncoder().encode(counts)) ?? Data()
        try await database.run(
            "UPDATE messages SET reactions = ? WHERE id = ?", [.blob(encoded), .text(messageID)]
        )
        await broker.notify(.messages(chatID: chatID))
    }

    /// Drops messages whose self-destruct deadline has passed.
    ///
    /// The server has its own reaper, but a device that was offline when a
    /// message expired would otherwise keep showing it — the deadline travels
    /// with the message precisely so the client can enforce it locally.
    public func purgeExpired(now: Date = .init()) async throws {
        let changed = try await database.run(
            "DELETE FROM messages WHERE expires_at IS NOT NULL AND expires_at > 0 AND expires_at <= ?",
            [.date(now)]
        )
        if changed > 0 { await broker.notify(.chats) }
    }

    // MARK: - Outbox

    public struct OutboxEntry: Sendable, Equatable {
        public var dedupKey: String
        public var chatID: String
        public var text: String
        public var replyTo: String?
        public var createdAt: Date
        public var attempts: Int
        /// The upload already happened; what waits here is the message carrying
        /// the resulting `media_ref`.
        public var attachment: Attachment?

        public init(
            dedupKey: String, chatID: String, text: String,
            replyTo: String? = nil, createdAt: Date = .init(), attempts: Int = 0,
            attachment: Attachment? = nil
        ) {
            self.dedupKey = dedupKey
            self.chatID = chatID
            self.text = text
            self.replyTo = replyTo
            self.createdAt = createdAt
            self.attempts = attempts
            self.attachment = attachment
        }
    }

    public func enqueue(_ entry: OutboxEntry) async throws {
        try await database.run(
            """
            INSERT INTO outbox (dedup_key, chat_id, text, reply_to, created_at, attempts, attachment)
            VALUES (?, ?, ?, ?, ?, 0, ?)
            ON CONFLICT(dedup_key) DO NOTHING
            """,
            [
                .text(entry.dedupKey), .text(entry.chatID), .text(entry.text),
                .optionalText(entry.replyTo), .date(entry.createdAt),
                Self.encode(entry.attachment),
            ]
        )
    }

    /// Oldest first — the order the user typed them in, preserved across a
    /// restart.
    public func pendingOutbox(limit: Int = 100) async throws -> [OutboxEntry] {
        let rows = try await database.query(
            "SELECT * FROM outbox ORDER BY created_at ASC LIMIT ?", [.int(limit)]
        )
        return rows.map(Self.outboxEntry(from:))
    }

    public func outboxEntry(dedupKey: String) async throws -> OutboxEntry? {
        let rows = try await database.query("SELECT * FROM outbox WHERE dedup_key = ?", [.text(dedupKey)])
        return rows.first.map(Self.outboxEntry(from:))
    }

    private static func outboxEntry(from row: Row) -> OutboxEntry {
        OutboxEntry(
            dedupKey: row.string("dedup_key") ?? "",
            chatID: row.string("chat_id") ?? "",
            text: row.string("text") ?? "",
            replyTo: row.string("reply_to"),
            createdAt: row.date("created_at") ?? Date(),
            attempts: Int(row.int("attempts") ?? 0),
            attachment: decode(Attachment.self, row.data("attachment"))
        )
    }

    // MARK: - Drafts

    /// Writes a draft the gateway mirrored to us from another device, or one we
    /// composed here. `updatedAt` is a last-writer-wins guard: two devices
    /// composing at once should converge on the later keystroke, not on whichever
    /// frame happened to arrive second.
    public func upsertDraft(chatID: String, text: String, replyTo: String?, updatedAt: Date) async throws {
        if text.isEmpty {
            try await database.run("DELETE FROM drafts WHERE chat_id = ?", [.text(chatID)])
        } else {
            try await database.run(
                """
                INSERT INTO drafts (chat_id, text, reply_to, updated_at)
                VALUES (?, ?, ?, ?)
                ON CONFLICT(chat_id) DO UPDATE SET
                    text       = excluded.text,
                    reply_to   = excluded.reply_to,
                    updated_at = excluded.updated_at
                 WHERE excluded.updated_at >= drafts.updated_at
                """,
                [.text(chatID), .text(text), .optionalText(replyTo), .date(updatedAt)]
            )
        }
        await broker.notify(.draft(chatID: chatID))
    }

    public func draft(chatID: String) async throws -> String {
        let rows = try await database.query(
            "SELECT text FROM drafts WHERE chat_id = ?", [.text(chatID)]
        )
        return rows.first?.string("text") ?? ""
    }

    public func recordOutboxFailure(dedupKey: String, error: String) async throws {
        try await database.run(
            "UPDATE outbox SET attempts = attempts + 1, last_error = ? WHERE dedup_key = ?",
            [.text(error), .text(dedupKey)]
        )
    }

    public func removeFromOutbox(dedupKey: String) async throws {
        try await database.run("DELETE FROM outbox WHERE dedup_key = ?", [.text(dedupKey)])
    }

    // MARK: - Contacts & users

    public func upsertContacts(_ contacts: [Contact]) async throws {
        guard !contacts.isEmpty else { return }
        try await database.transaction(contacts.map { contact in
            Database.Statement(
                """
                INSERT INTO contacts (user_id, name, blocked, updated_at)
                VALUES (?, ?, ?, ?)
                ON CONFLICT(user_id) DO UPDATE SET
                    name = excluded.name, blocked = excluded.blocked, updated_at = excluded.updated_at
                """,
                [
                    .text(contact.userID), .text(contact.name),
                    .bool(contact.isBlocked), .date(contact.updatedAt),
                ]
            )
        })
        await broker.notify(.contacts)
    }

    public func contacts() async throws -> [Contact] {
        let rows = try await database.query("SELECT * FROM contacts ORDER BY name, user_id")
        return rows.map { row in
            Contact(
                userID: row.string("user_id") ?? "",
                name: row.string("name") ?? "",
                isBlocked: row.bool("blocked"),
                updatedAt: row.date("updated_at") ?? Date()
            )
        }
    }

    public func removeContact(userID: String) async throws {
        try await database.run("DELETE FROM contacts WHERE user_id = ?", [.text(userID)])
        await broker.notify(.contacts)
    }

    public func upsertUser(_ user: User) async throws {
        try await database.run(
            """
            INSERT INTO users (id, username, display_name, online, last_seen_at)
            VALUES (?, ?, ?, ?, ?)
            ON CONFLICT(id) DO UPDATE SET
                username     = COALESCE(excluded.username, users.username),
                display_name = COALESCE(excluded.display_name, users.display_name),
                online       = excluded.online,
                last_seen_at = COALESCE(excluded.last_seen_at, users.last_seen_at)
            """,
            [
                .text(user.id), .optionalText(user.username), .optionalText(user.displayName),
                .bool(user.isOnline), .date(user.lastSeenAt),
            ]
        )
        await broker.notify(.chats)
    }

    public func user(id: String) async throws -> User? {
        let rows = try await database.query("SELECT * FROM users WHERE id = ?", [.text(id)])
        guard let row = rows.first else { return nil }
        return User(
            id: row.string("id") ?? id,
            username: row.string("username"),
            displayName: row.string("display_name"),
            isOnline: row.bool("online"),
            lastSeenAt: row.date("last_seen_at")
        )
    }

    // MARK: - Meta

    public func meta(_ key: String) async throws -> String? {
        let rows = try await database.query("SELECT value FROM meta WHERE key = ?", [.text(key)])
        return rows.first?.string("value")
    }

    public func setMeta(_ key: String, _ value: String) async throws {
        try await database.run(
            "INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
            [.text(key), .text(value)]
        )
    }

    public func currentUserID() async throws -> String? { try await meta(MetaKey.userID) }

    public func wipe() async throws {
        try await database.wipe()
        await broker.notify([.chats, .contacts])
    }

    public enum MetaKey {
        public static let userID = "user_id"
        public static let contactCursor = "contact_cursor"
        public static let draftCursor = "draft_cursor"
    }

    // MARK: - Row mapping

    private static func chat(from row: Row) -> Chat {
        Chat(
            id: row.string("id") ?? "",
            kind: Chat.Kind(rawValue: row.string("kind") ?? "") ?? .direct,
            title: row.string("title") ?? "",
            username: row.string("username"),
            ownerID: row.string("owner_id") ?? "",
            peerUserID: row.string("peer_user_id"),
            lastMessagePreview: row.string("last_message_preview") ?? "",
            lastMessageAt: row.date("last_message_at"),
            lastSeq: row.uint("last_seq"),
            lastReadSeq: row.uint("last_read_seq"),
            isMuted: row.bool("is_muted")
        )
    }

    private static func message(from row: Row) -> Message {
        Message(
            id: row.string("id") ?? "",
            chatID: row.string("chat_id") ?? "",
            senderID: row.string("sender_id") ?? "",
            seq: row.uint("seq"),
            text: row.string("text") ?? "",
            sentAt: row.date("sent_at") ?? Date(),
            state: MessageState(rawValue: row.string("state") ?? "") ?? .sent,
            isEdited: row.bool("edited"),
            isDeleted: row.bool("deleted"),
            replyToID: row.string("reply_to"),
            attachment: decode(Attachment.self, row.data("attachment")),
            forwardedFrom: decode(ForwardOrigin.self, row.data("forward")),
            expiresAt: row.date("expires_at"),
            reactions: decode([String: Int].self, row.data("reactions")) ?? [:],
            dedupKey: row.string("dedup_key") ?? ""
        )
    }

    private static func upsertMessageStatement(_ message: Message) -> Database.Statement {
        Database.Statement(
            """
            INSERT INTO messages (id, chat_id, sender_id, seq, text, sent_at, state, edited, deleted,
                                  reply_to, attachment, forward, expires_at, reactions, dedup_key)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(id) DO UPDATE SET
                text       = excluded.text,
                seq        = MAX(excluded.seq, messages.seq),
                edited     = excluded.edited,
                deleted    = excluded.deleted,
                attachment = excluded.attachment,
                forward    = excluded.forward,
                expires_at = excluded.expires_at,
                -- A redelivered NEW carries no reaction tally; keep what REACT_UPD
                -- gave us rather than blanking it.
                reactions  = COALESCE(excluded.reactions, messages.reactions),
                -- Never downgrade 'read' back to 'sent' on a replay.
                state      = CASE WHEN messages.state = 'read' THEN 'read' ELSE excluded.state END
            """,
            [
                .text(message.id), .text(message.chatID), .text(message.senderID),
                .uint(message.seq), .text(message.text), .date(message.sentAt),
                .text(message.state.rawValue), .bool(message.isEdited), .bool(message.isDeleted),
                .optionalText(message.replyToID),
                encode(message.attachment), encode(message.forwardedFrom),
                .date(message.expiresAt),
                message.reactions.isEmpty ? .null : encode(message.reactions),
                .text(message.dedupKey),
            ]
        )
    }

    private static func encode<T: Encodable>(_ value: T?) -> SQLValue {
        guard let value, let data = try? JSONEncoder().encode(value) else { return .null }
        return .blob(data)
    }

    private static func decode<T: Decodable>(_ type: T.Type, _ data: Data?) -> T? {
        guard let data, !data.isEmpty else { return nil }
        return try? JSONDecoder().decode(T.self, from: data)
    }

    // MARK: - Observation

    public func changes(_ topic: ChangeBroker.Topic) async -> AsyncStream<Void> {
        await broker.stream(topic)
    }

    public func notify(_ topics: [ChangeBroker.Topic]) async {
        await broker.notify(topics)
    }
}
