import Foundation

/// The typed operations repositories call. Everything here is a thin shell over
/// `request`/`fire`; the interesting decisions are documented per method, and
/// they are all consequences of what the gateway actually implements.
extension SynapseClient {

    // MARK: - Messaging

    /// Posts a message. `chatID` may be a snowflake or `"@username"`.
    ///
    /// `dedupKey` must be stable across retries of the *same* logical message —
    /// the server maps (device, dedupKey) → message id, so a retry after a lost
    /// ack returns the original with `duplicate == true` instead of double
    /// posting. The outbox depends on this being honoured.
    public func sendMessage(
        chatID: String,
        dedupKey: String,
        text: String,
        replyTo: String = "",
        mediaRef: String = "",
        attachment: AttachmentBody? = nil,
        ttlSeconds: Int32 = 0
    ) async throws -> SendAckBody {
        let reply = try await request(
            .send,
            body: SendBody(
                chatID: chatID,
                dedupKey: dedupKey,
                text: text,
                mediaRef: mediaRef,
                replyTo: replyTo,
                attachment: attachment,
                ttlSeconds: ttlSeconds
            ),
            expect: .sendAck
        )
        return try SendAckBody.protoDecoded(from: reply.body)
    }

    /// One page of history, newest-first, ending before `beforeSeq`
    /// (`0` = latest). The server caps `limit` at 100 and defaults it to 50.
    ///
    /// The page arrives as N `NEW` frames sharing our request id, terminated by
    /// `HISTORY_OK`. Note that `HISTORY_OK.chatID` echoes whatever *we* sent —
    /// so when the target was `"@username"` the resolved id must be read off the
    /// messages, not off the terminator.
    public func history(
        chatID: String,
        beforeSeq: UInt64 = 0,
        limit: Int32 = 50
    ) async throws -> (messages: [NewMessageBody], page: HistoryOKBody) {
        let reply = try await request(
            .history,
            body: HistoryBody(chatID: chatID, beforeSeq: beforeSeq, limit: limit),
            expect: .historyOK,
            streamItemType: .new
        )
        let messages = try reply.items.map { try NewMessageBody.protoDecoded(from: $0) }
        return (messages, try HistoryOKBody.protoDecoded(from: reply.body))
    }

    /// A thread's replies, oldest-first.
    public func thread(
        chatID: String,
        rootID: String,
        afterSeq: UInt64 = 0,
        limit: Int32 = 50
    ) async throws -> (messages: [NewMessageBody], page: ThreadOKBody) {
        let reply = try await request(
            .thread,
            body: ThreadBody(chatID: chatID, rootID: rootID, afterSeq: afterSeq, limit: limit),
            expect: .threadOK,
            streamItemType: .new
        )
        let messages = try reply.items.map { try NewMessageBody.protoDecoded(from: $0) }
        return (messages, try ThreadOKBody.protoDecoded(from: reply.body))
    }

    /// Marks a chat read. Answered only on failure, so this does not wait.
    public func markRead(chatID: String, upToMessageID: String, upToChatSeq: UInt64) async throws {
        try await fire(.read, body: ReadBody(
            chatID: chatID, upToMessageID: upToMessageID, upToChatSeq: upToChatSeq
        ))
    }

    /// Typing is best-effort by definition: the gateway throttles it per
    /// connection *and* per chat and drops the excess silently, because an error
    /// reply would cost more than the frame it refuses.
    public func setTyping(chatID: String, active: Bool) async throws {
        try await fire(.typing, body: TypingBody(chatID: chatID, active: active))
    }

    public func editMessage(chatID: String, messageID: String, text: String) async throws {
        try await fire(.edit, body: EditBody(chatID: chatID, messageID: messageID, text: text))
    }

    public func deleteMessage(chatID: String, messageID: String, forAll: Bool) async throws {
        try await fire(.delete, body: DeleteBody(chatID: chatID, messageID: messageID, forAll: forAll))
    }

    /// Toggles a reaction. The reply carries the post-change tally so the
    /// reacting client renders immediately instead of waiting for the fanout.
    public func react(chatID: String, messageID: String, emoji: String) async throws -> ReactUpdateBody {
        let reply = try await request(
            .react,
            body: ReactBody(chatID: chatID, messageID: messageID, emoji: emoji),
            expect: .reactUpd
        )
        return try ReactUpdateBody.protoDecoded(from: reply.body)
    }

    public func forwardMessage(
        fromChatID: String,
        messageID: String,
        toChatID: String,
        dedupKey: String
    ) async throws -> SendAckBody {
        let reply = try await request(
            .forward,
            body: ForwardBody(
                fromChatID: fromChatID, messageID: messageID, toChatID: toChatID, dedupKey: dedupKey
            ),
            expect: .sendAck
        )
        return try SendAckBody.protoDecoded(from: reply.body)
    }

    // MARK: - Chats & membership

    /// Creates a group or channel. Members may be user ids or `"@username"` —
    /// the gateway resolves them, so a client never has to look a stranger up
    /// before inviting them.
    public func createChat(type: String, title: String, members: [String]) async throws -> ChatInfoBody {
        let reply = try await request(
            .chatCreate,
            body: ChatCreateBody(type: type, title: title, members: members),
            expect: .chatInfo
        )
        return try ChatInfoBody.protoDecoded(from: reply.body)
    }

    /// Joins by invite code or by `@handle`. Returns the joined chat id.
    public func join(code: String = "", handle: String = "") async throws -> String {
        let reply = try await request(.join, body: JoinBody(code: code, handle: handle), expect: .invites)
        return try InvitesBody.protoDecoded(from: reply.body).joinedChat
    }

    public func createInvite(chatID: String, expiresAt: Int64 = 0, maxUses: Int32 = 0) async throws -> [InviteLinkBody] {
        let reply = try await request(
            .inviteCreate,
            body: InviteCreateBody(chatID: chatID, expiresAt: expiresAt, maxUses: maxUses),
            expect: .invites
        )
        return try InvitesBody.protoDecoded(from: reply.body).links
    }

    public func listInvites(chatID: String) async throws -> [InviteLinkBody] {
        let reply = try await request(.inviteList, body: InviteListBody(chatID: chatID), expect: .invites)
        return try InvitesBody.protoDecoded(from: reply.body).links
    }

    public func setRole(chatID: String, userID: String, role: String) async throws {
        _ = try await request(
            .setRole,
            body: SetRoleBody(chatID: chatID, userID: userID, role: role),
            expect: .invites
        )
    }

    /// Resolves `"@username"` to the canonical 1:1 chat id, creating the chat if
    /// it does not exist yet.
    ///
    /// The protocol has no "look up a user" message. What it does have is
    /// `resolveChat`, which every chat-scoped command runs on its target — so
    /// the cheapest way to ask "who is @bob, and what chat do we share?" is to
    /// send a command that resolves the target and echoes the *resolved* id
    /// back. `PIN_LIST` is that command: it is read-only, it is not charged
    /// against the flood budget, and `PINNED.chat_id` is the snowflake rather
    /// than the string we sent. `NOT_FOUND` means no such user, `FORBIDDEN`
    /// means a block in either direction.
    ///
    /// If pins are disabled server-side we fall back to a one-message `HISTORY`
    /// probe and read the id off the returned message — which works only for a
    /// chat that already has one, hence the preference order.
    public func resolveDirectChat(username: String) async throws -> String {
        let target = username.hasPrefix("@") ? username : "@" + username
        do {
            let reply = try await request(
                .pinList,
                body: PinActionBody(chatID: target),
                expect: .pinned
            )
            let chatID = try PinnedBody.protoDecoded(from: reply.body).chatID
            if !chatID.isEmpty { return chatID }
        } catch let error as ProtocolError where error.code == .unsupported {
            // Pins are optional in the service wiring; fall through.
        }

        let page = try await history(chatID: target, limit: 1)
        if let chatID = page.messages.first?.chatID, !chatID.isEmpty { return chatID }
        throw ProtocolError(code: .notFound, message: "cannot resolve \(target)")
    }

    // MARK: - Contacts

    /// Everything changed after `since` (0 = full sync), plus the next cursor.
    public func syncContacts(since: Int64 = 0) async throws -> ContactListBody {
        let reply = try await request(.contactSync, body: ContactSyncBody(since: since), expect: .contactList)
        return try ContactListBody.protoDecoded(from: reply.body)
    }

    @discardableResult
    public func addContact(target: String, name: String = "") async throws -> ContactListBody {
        let reply = try await request(
            .contactAdd,
            body: ContactAddBody(target: target, name: name),
            expect: .contactList
        )
        return try ContactListBody.protoDecoded(from: reply.body)
    }

    public func removeContact(target: String) async throws {
        _ = try await request(.contactRemove, body: ContactRemoveBody(target: target), expect: .contactList)
    }

    /// A block cuts traffic in both directions and survives the other side
    /// reopening the chat.
    public func setBlocked(target: String, blocked: Bool) async throws {
        _ = try await request(.block, body: BlockBody(target: target, blocked: blocked), expect: .contactList)
    }

    // MARK: - Search

    /// Full-text search across the user's own chats, ranked and
    /// permission-filtered server-side.
    public func search(query: String, limit: Int32 = 20) async throws -> [SearchHitBody] {
        let reply = try await request(
            .search,
            body: SearchBody(query: query, limit: limit),
            expect: .searchResults
        )
        return try SearchResultsBody.protoDecoded(from: reply.body).hits
    }

    // MARK: - Media

    public func initUpload(filename: String, contentType: String, size: Int64) async throws -> MediaTicketBody {
        let reply = try await request(
            .mediaInit,
            body: MediaInitBody(filename: filename, contentType: contentType, size: size),
            expect: .mediaTicket
        )
        return try MediaTicketBody.protoDecoded(from: reply.body)
    }

    public func downloadURL(mediaRef: String) async throws -> MediaURLBody {
        let reply = try await request(.mediaFetch, body: MediaFetchBody(mediaRef: mediaRef), expect: .mediaURL)
        return try MediaURLBody.protoDecoded(from: reply.body)
    }

    // MARK: - Drafts & pins

    /// Empty text clears the draft. The gateway mirrors it to this user's *other*
    /// devices only — a draft is private, so it is routed per-user, never to the
    /// chat.
    public func setDraft(chatID: String, text: String, replyTo: String = "") async throws {
        try await fire(.draftSet, body: DraftBody(chatID: chatID, text: text, replyTo: replyTo))
    }

    public func syncDrafts(since: Int64 = 0) async throws -> DraftsBody {
        let reply = try await request(.draftSync, body: DraftSyncBody(since: since), expect: .drafts)
        return try DraftsBody.protoDecoded(from: reply.body)
    }

    public func listPins(chatID: String) async throws -> PinnedBody {
        let reply = try await request(.pinList, body: PinActionBody(chatID: chatID), expect: .pinned)
        return try PinnedBody.protoDecoded(from: reply.body)
    }

    public func setPinned(chatID: String, messageID: String, pinned: Bool) async throws -> PinnedBody {
        let reply = try await request(
            pinned ? .pin : .unpin,
            body: PinActionBody(chatID: chatID, messageID: messageID),
            expect: .pinned
        )
        return try PinnedBody.protoDecoded(from: reply.body)
    }

    // MARK: - Push

    /// Registers (or, with an empty token, clears) this device's APNs token.
    ///
    /// Clearing is how "turn notifications off" is meant to work here: it stops
    /// them at the source rather than letting the server keep sending pushes the
    /// device silently discards.
    public func registerPushToken(_ token: String) async throws {
        _ = try await request(.pushToken, body: PushTokenBody(token: token), expect: .pushToken)
    }
}
