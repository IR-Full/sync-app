import Foundation

/// The typed operations repositories call. Everything here is a thin shell over
/// `request`/`fire`; the interesting decisions are documented per method, and
/// they are all consequences of what the gateway actually implements.
extension SyncAppClient {

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
    /// The answer comes in one of two shapes, chosen by the gateway from the
    /// capabilities negotiated in `HELLO`, and both carry exactly the same
    /// messages:
    ///
    /// - `HISTORY_PAGE`, a single frame, since this build advertises
    ///   `.batching`;
    /// - N `NEW` frames sharing our request id terminated by `HISTORY_OK`, which
    ///   is what an older gateway that does not know the page still sends.
    ///
    /// Both are handled here rather than assuming the first, because the
    /// capability is negotiated per connection: a client cannot know which one
    /// it will get until it gets it, and downgrading is exactly what negotiation
    /// is for.
    ///
    /// The returned `page.chatID` differs between them in a way worth knowing:
    /// `HISTORY_PAGE` carries the RESOLVED id, while `HISTORY_OK` echoes
    /// whatever *we* sent — so on the legacy path a target of `"@username"`
    /// comes back unresolved and the real id must be read off the messages.
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

        if reply.type == .historyPage {
            let page = try HistoryPageBody.protoDecoded(from: reply.body)
            // Re-expressed as the terminator the callers already read, so the
            // transport optimisation stays invisible above this line.
            var cursor = HistoryOKBody()
            cursor.chatID = page.chatID
            cursor.nextBefore = page.nextBefore
            cursor.done = page.done
            return (page.messages, cursor)
        }

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
    /// Full-text search, optionally narrowed to one chat or one sender.
    ///
    /// Neither narrowing is a security boundary — the server still intersects the result
    /// with the caller's own membership — so naming a chat the caller is not in returns
    /// nothing rather than opening anything.
    public func search(
        query: String,
        limit: Int32 = 20,
        chatID: String = "",
        senderID: String = ""
    ) async throws -> [SearchHitBody] {
        let reply = try await request(
            .search,
            body: SearchBody(query: query, limit: limit, chatID: chatID, senderID: senderID),
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

    // MARK: - Chat list

    /// One page of the chats this account belongs to.
    ///
    /// This is the only message that enumerates them: everything else about a
    /// chat is learned as a consequence of traffic, which is why a fresh install
    /// without this call starts blank. `after` is the `nextAfter` of the
    /// previous page — an id, not an offset, so a page stays correct even while
    /// the list changes.
    /// One page of the chat list.
    ///
    /// The cursor has TWO halves and both are required. The list is ordered by
    /// activity, which reorders as messages arrive, so a cursor naming only a chat id
    /// skips and repeats rows exactly when the account is busy — which is when somebody
    /// is most likely to be scrolling it. `afterActivity` is the previous page's last
    /// `lastActivityAt`; pass both or neither.
    public func chatList(
        after: String = "",
        afterActivity: Int64 = 0,
        limit: Int32 = 0,
        includeArchived: Bool = false
    ) async throws -> ChatsBody {
        let reply = try await request(
            .chatList,
            body: ChatListBody(
                after: after,
                limit: limit,
                afterActivity: afterActivity,
                includeArchived: includeArchived
            ),
            expect: .chats
        )
        return try ChatsBody.protoDecoded(from: reply.body)
    }

    /// Walks `chatList` to the end and returns every chat.
    ///
    /// Paging is the transport's business, not the caller's: a sync that stops
    /// at the first page would silently lose chats for anyone past the page
    /// size, and that is exactly the bug this message exists to fix.
    /// Walks `chatList` to the end and returns every chat, archived ones included.
    ///
    /// `includeArchived` is on because this is the SYNC, not the screen: an archived
    /// chat is still a chat the cache has to know about, and omitting it here would make
    /// the archive empty on a fresh install and un-unarchivable.
    ///
    /// Bounded rather than `while true`: the loop trusts a cursor the server supplies,
    /// and a server that answers "not done" with an unchanging cursor would otherwise
    /// spin forever on a connect path the user is waiting on.
    public func allChats(pageLimit: Int32 = 100, maxPages: Int = 200) async throws -> [ChatSummaryBody] {
        var out: [ChatSummaryBody] = []
        var cursor = ""
        var activity: Int64 = 0

        for _ in 0..<maxPages {
            let page = try await chatList(
                after: cursor,
                afterActivity: activity,
                limit: pageLimit,
                includeArchived: true
            )
            out.append(contentsOf: page.chats)
            // `done` is the server's word for "that was the last page"; the empty
            // cursor guards against a page that says otherwise but hands back nothing
            // to continue from; the unchanged cursor guards against one that hands back
            // the same thing twice.
            if page.done || page.nextAfter.isEmpty || page.nextAfter == cursor { return out }
            cursor = page.nextAfter
            activity = page.nextAfterActivity
        }
        return out
    }

    // MARK: - Profiles

    /// Reads a profile. `target` is a user id or `"@username"`; empty means our
    /// own. This doubles as the user lookup — there is no directory and no
    /// prefix search, so an exact handle is the only way to find someone.
    ///
    /// `NOT_FOUND` means no such user; `FORBIDDEN` means a block in either
    /// direction.
    public func profile(target: String = "") async throws -> ProfileBody {
        let reply = try await request(
            .profileGet,
            body: ProfileGetBody(target: target),
            expect: .profile
        )
        return try ProfileBody.protoDecoded(from: reply.body)
    }

    /// Changes our own profile and returns it as stored.
    ///
    /// Omitted values are left alone: proto3 cannot tell an absent string from
    /// an empty one, so the gateway reads an empty `avatarRef` as "no change".
    /// Removing the picture is therefore `clearAvatar`, not an empty ref.
    ///
    /// The gateway also mirrors the result to this account's other devices as an
    /// unsolicited `PROFILE`, so both paths deliver the same body.
    @discardableResult
    public func setProfile(
        displayName: String = "",
        avatarRef: String = "",
        clearAvatar: Bool = false
    ) async throws -> ProfileBody {
        let reply = try await request(
            .profileSet,
            body: ProfileSetBody(
                displayName: displayName,
                avatarRef: avatarRef,
                clearAvatar: clearAvatar
            ),
            expect: .profile
        )
        return try ProfileBody.protoDecoded(from: reply.body)
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

    // MARK: - Sessions
    //
    // Until these existed, "log out" on this platform was a purely local
    // gesture: the app forgot its token while the session stayed valid on the
    // server until it expired. A lost phone therefore kept access for the whole
    // session TTL, and there was no way from this app to see that it had, let
    // alone stop it.

    /// Every live session of this account, newest first is NOT guaranteed — the
    /// order is the server's and the UI should sort by `createdAt`.
    ///
    /// No token comes back for any of them, including this one. The list exists
    /// so a person can recognise a device well enough to decide whether to kill
    /// it; handing every device the credentials of every other would turn a read
    /// into a lateral-movement tool.
    public func listSessions() async throws -> [SessionInfoBody] {
        let reply = try await request(.sessionList, body: SessionListBody(), expect: .sessions)
        return try SessionsBody.protoDecoded(from: reply.body).sessions
    }

    /// Revokes one session by id.
    ///
    /// Revoking the session marked `current` logs THIS device out, and the
    /// answer says so via `self` — the caller must drop its stored credentials
    /// on that rather than wait for the socket to fail, because the gateway
    /// sends the reply before closing and a client that ignores it reconnects
    /// with a token that is already dead.
    @discardableResult
    public func revokeSession(sessionID: String) async throws -> SessionRevokedBody {
        let reply = try await request(
            .sessionRevoke,
            body: SessionRevokeBody(sessionID: sessionID),
            expect: .sessionRevoked
        )
        return try SessionRevokedBody.protoDecoded(from: reply.body)
    }

    /// Signs out every OTHER session, keeping this one — what a person reaches
    /// for after losing a device.
    ///
    /// `includingThisDevice` extends the sweep to this connection. It is a
    /// parameter rather than a second method because "log out everywhere,
    /// including here" and "log out everywhere but here" are different
    /// intentions, and the difference should be stated rather than implied by
    /// which call was picked.
    @discardableResult
    public func revokeOtherSessions(includingThisDevice: Bool = false) async throws -> SessionRevokedBody {
        let reply = try await request(
            .sessionRevoke,
            body: SessionRevokeBody(allIncludingCurrent: includingThisDevice),
            expect: .sessionRevoked
        )
        return try SessionRevokedBody.protoDecoded(from: reply.body)
    }

    // MARK: - Account

    /// Erases this account: sessions, devices, push tokens, contacts on both
    /// sides, drafts, read cursors, reactions, votes, invites, pins and chat
    /// memberships.
    ///
    /// Messages are ANONYMISED rather than dropped — a message is content in
    /// somebody else's conversation, so the text, media and sender are erased in
    /// place and the row is tombstoned. The conversations keep their shape and
    /// the person disappears from them. Worth surfacing in the confirmation UI,
    /// because "delete everything I ever sent" is what people expect it to mean
    /// and it is not what happens.
    ///
    /// The password is required even though this connection is already
    /// authenticated. That is the point: a session token lives on the device, so
    /// without it anyone holding an unlocked phone could destroy the account
    /// behind it.
    ///
    /// Every session is revoked before the reply is sent, so this is the last
    /// frame the connection carries — the caller must clear local state and
    /// return to the login screen rather than expect to keep working.
    @discardableResult
    public func deleteAccount(password: String, reason: String = "") async throws -> AccountDeletedBody {
        let reply = try await request(
            .accountDelete,
            body: AccountDeleteBody(password: password, reason: reason),
            expect: .accountDeleted
        )
        return try AccountDeletedBody.protoDecoded(from: reply.body)
    }
}
