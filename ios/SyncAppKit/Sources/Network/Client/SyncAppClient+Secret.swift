import Foundation

/// Secret chats, per-member chat flags, account security and billing.
///
/// Split from `SyncAppClient+API` because it is a different kind of surface: these
/// operations mostly exist to serve ONE screen each, and grouping them by the screen
/// they belong to keeps the general messaging API from becoming a list of everything
/// the gateway happens to accept.
extension SyncAppClient {

    // MARK: - Key directory

    /// Publishes this device's identity and a batch of one-time prekeys, and returns
    /// what the directory now holds for it.
    ///
    /// Until a device has done this, nobody can start a secret chat with it — and,
    /// less obviously, nobody can start one with the ACCOUNT either: the relay
    /// addresses devices, so an account with no published device is unreachable
    /// rather than merely offline. `SECRET_ACK` reports that as
    /// `undeliverable`.
    ///
    /// A REQUEST rather than fire-and-forget, which it used to be. The reply is the only
    /// place a device can learn its own one-time prekey balance: those keys are consumed
    /// by PEERS fetching bundles, so nothing this device can observe tells it the
    /// directory is running dry, and a dry directory silently costs every new session the
    /// stronger four-DH handshake.
    public func publishKeys(_ bundle: KeyPublishBody) async throws -> KeyStateBody {
        let reply = try await request(.keyPublish, body: bundle, expect: .keyState)
        return try KeyStateBody.protoDecoded(from: reply.body)
    }

    /// One device's bundle, consuming a one-time prekey.
    public func fetchKeys(userID: String, deviceID: String) async throws -> KeyBundleBody {
        let reply = try await request(
            .keyFetch,
            body: KeyFetchBody(userID: userID, deviceID: deviceID),
            expect: .keyBundle
        )
        return try KeyBundleBody.protoDecoded(from: reply.body)
    }

    /// Every device a user has published.
    ///
    /// This is the call a secret chat actually needs. Encrypting for one device leaves
    /// the message unreadable on the peer's other phone, and the peer has no way to
    /// tell that from a message that was never sent — so a secret chat fans out per
    /// device, and the fan-out has to be recomputed each time because a new device can
    /// appear between two messages.
    public func fetchAllKeys(userID: String) async throws -> KeyBundlesBody {
        let reply = try await request(
            .keyFetchAll,
            body: KeyFetchBody(userID: userID),
            expect: .keyBundles
        )
        return try KeyBundlesBody.protoDecoded(from: reply.body)
    }

    // MARK: - The relay

    /// Relays one ratchet message to one device, and reports what became of it.
    ///
    /// The encoding of the payload is decided HERE rather than by the caller, from
    /// what this connection negotiated: a peer that did not negotiate `secretQueue`
    /// reads only the legacy text fields, and filling the binary ones for it delivers
    /// an empty message. `capabilities` is the connection's, and it is the gateway that
    /// re-encodes per recipient — this only picks the form for the leg to the server.
    ///
    /// The reply is `SECRET_ACK` when the peer negotiated it. An older gateway answers
    /// nothing at all, which is why this tolerates a timeout rather than treating one
    /// as a send failure: the message was very likely relayed, and retrying would
    /// double-send it through a ratchet that cannot deduplicate.
    @discardableResult
    public func sendSecret(
        toUserID: String,
        toDeviceID: String,
        fromDeviceID: String,
        header: Data,
        ciphertext: Data
    ) async throws -> SecretAckBody? {
        // One read, used twice: the encoding and whether an ack is coming are the
        // same decision, and splitting them would let the two disagree if the
        // connection renegotiated between the lines.
        let durable = capabilities.contains(.secretQueue)

        var body = SecretMsgBody()
        body.toUserID = toUserID
        body.toDeviceID = toDeviceID
        body.fromDeviceID = fromDeviceID
        body.setPayload(header, ciphertext, binary: durable)

        guard durable else {
            try await fire(.secretSend, body: body)
            return nil
        }
        let reply = try await request(.secretSend, body: body, expect: .secretAck)
        return try SecretAckBody.protoDecoded(from: reply.body)
    }

    /// Asks for everything queued for this device while it was offline.
    ///
    /// The queued frames arrive as ordinary `SECRET_RECV` pushes carrying a `queueID`,
    /// not as items of this request — so the receive path needs no separate branch for
    /// history, and a frame that arrives mid-sync is handled the same way whether it
    /// came from the queue or from a peer typing right now.
    public func syncSecrets(after: String = "", limit: Int32 = 0) async throws -> SecretSyncedBody {
        let reply = try await request(
            .secretSync,
            body: SecretSyncBody(after: after, limit: limit),
            expect: .secretSynced
        )
        return try SecretSyncedBody.protoDecoded(from: reply.body)
    }

    /// Confirms the queue rows whose plaintext is now on disk, so the server drops
    /// them.
    ///
    /// Call this AFTER the local write, never on receipt. Acking on receipt loses the
    /// message if the app dies in between, and loses it silently — the server has
    /// already forgotten it and the sender was told it was delivered.
    public func ackSecrets(_ ids: [String]) async throws {
        guard !ids.isEmpty else { return }
        try await fire(.secretAcked, body: SecretAckedBody(ids: ids))
    }

    /// Creates a secret chat with one other account.
    ///
    /// A chat TYPE, not a side channel: it appears in the chat list, has history, and
    /// carries a lock badge. Premium-gated on the server, which answers
    /// `premiumRequired` — a code of its own rather than `forbidden` precisely so this
    /// can route to the upgrade screen instead of a dead end.
    /// `target` is a user id OR an `@username` — the gateway resolves either, the same
    /// way it does for a direct chat. Titleless by construction: a two-party chat is
    /// named after its peer, and the server ignores a title here rather than
    /// validating one.
    public func createSecretChat(with target: String) async throws -> ChatInfoBody {
        try await createChat(type: "secret", title: "", members: [target])
    }

    // MARK: - Per-member chat flags

    /// Sets this account's own mute / pin / archive state for a chat.
    ///
    /// There is no matching READ, and that is not an omission: the flags already ride
    /// along on every `ChatSummary`, so a separate getter would be a second round trip
    /// for data the chat list just delivered — and a second source of truth to
    /// disagree with it.
    ///
    /// The three values are ABSOLUTE, not a patch. proto3 has no field presence for
    /// scalars, so "leave pinned alone" cannot be expressed on the wire; the caller
    /// passes the state it wants, which means reading the current flags off the
    /// summary first. Defaulting them here instead would silently unpin a chat every
    /// time somebody muted one.
    ///
    /// `mutedUntil` is a DEADLINE in epoch milliseconds — "for eight hours" is what
    /// muting usually means and a boolean cannot say it. `0` unmutes, and the server
    /// normalises a deadline already in the past to `0` rather than storing it.
    ///
    /// The reply echoes what was STORED, so two racing changes converge on the
    /// server's state rather than on whichever request happened to be sent last.
    @discardableResult
    public func setChatFlags(
        chatID: String,
        mutedUntil: Int64,
        pinned: Bool,
        archived: Bool
    ) async throws -> ChatFlagsSetBody {
        let reply = try await request(
            .chatFlags,
            body: ChatFlagsBody(
                chatID: chatID,
                mutedUntil: mutedUntil,
                pinned: pinned,
                archived: archived
            ),
            expect: .chatFlagsSet
        )
        return try ChatFlagsSetBody.protoDecoded(from: reply.body)
    }

    // MARK: - Account security

    /// Changes the password.
    ///
    /// There was no way to do this at all: a user whose password leaked could not
    /// replace it, which makes the account permanently compromised rather than
    /// temporarily. Every OTHER session is revoked — the reply says how many — because
    /// a password change that leaves the attacker's session alive has not achieved
    /// anything.
    public func changePassword(old: String, new: String) async throws -> PasswordChangedBody {
        let reply = try await request(
            .passwordChange,
            body: PasswordChangeBody(oldPassword: old, newPassword: new),
            expect: .passwordChanged
        )
        return try PasswordChangedBody.protoDecoded(from: reply.body)
    }

    /// Begins TOTP enrolment: returns the secret, its `otpauth://` URI and the
    /// recovery codes.
    ///
    /// Nothing is enabled yet. The codes are shown ONCE, here, because they are stored
    /// only as argon2id hashes — there is no second chance to display them, which is
    /// also why enrolment is two steps: a user who never confirms has not been locked
    /// out of their own account by a QR code they failed to scan.
    public func beginTOTP() async throws -> TOTPSetupInfoBody {
        let reply = try await request(.totpSetup, body: nil, expect: .totpSetupInfo)
        return try TOTPSetupInfoBody.protoDecoded(from: reply.body)
    }

    /// Confirms enrolment with a code from the authenticator, proving the secret was
    /// actually stored somewhere the user can reach.
    public func confirmTOTP(code: String) async throws -> TOTPStateBody {
        let reply = try await request(
            .totpConfirm,
            body: TOTPConfirmBody(code: code),
            expect: .totpState
        )
        return try TOTPStateBody.protoDecoded(from: reply.body)
    }

    /// Turns it off. Needs the password AND a code (current or recovery).
    ///
    /// Both, deliberately. A stolen session token must not be able to remove the
    /// factor that keeps its holder out of the next login — and a second factor that
    /// can be disabled by whoever already has the session is not a second factor.
    public func disableTOTP(password: String, code: String) async throws -> TOTPStateBody {
        let reply = try await request(
            .totpDisable,
            body: TOTPDisableBody(password: password, code: code),
            expect: .totpState
        )
        return try TOTPStateBody.protoDecoded(from: reply.body)
    }

    /// Whether a second factor is enabled, and how many recovery codes remain.
    public func totpState() async throws -> TOTPStateBody {
        let reply = try await request(.totpState, body: nil, expect: .totpState)
        return try TOTPStateBody.protoDecoded(from: reply.body)
    }

    // MARK: - Billing

    /// The plans on offer for a country, with per-method prices.
    ///
    /// Country-scoped because the payment methods differ, not just the currency: SBP
    /// and Mir cards in Russia, card and wallet elsewhere. The SERVER decides from the
    /// country — a client that picked its own provider would be choosing which
    /// regulator applies to the transaction.
    public func billingPlans(country: String = "") async throws -> BillingOffersBody {
        let reply = try await request(
            .billingPlans,
            body: BillingPlansBody(country: country),
            expect: .billingOffers
        )
        return try BillingOffersBody.protoDecoded(from: reply.body)
    }

    /// Starts a payment and returns where to send the user.
    ///
    /// `idempotencyKey` must be stable across retries of the same intent. It is the
    /// only thing standing between a tapped-twice button and two charges: the server
    /// has a unique index on it, so a repeat returns the FIRST payment rather than
    /// creating a second.
    /// `returnURL` is where a redirect provider sends the browser back to. It is a
    /// universal link on iOS, not a web page — the app has to regain the foreground,
    /// and the subscription arrives as a PUSH anyway, so the return is a navigation
    /// concern rather than the source of truth about whether the payment succeeded.
    public func checkout(
        plan: String,
        method: String,
        country: String,
        idempotencyKey: String,
        returnURL: String = ""
    ) async throws -> BillingPaymentBody {
        let reply = try await request(
            .billingCheckout,
            body: BillingCheckoutBody(
                plan: plan,
                method: method,
                idempotencyKey: idempotencyKey,
                country: country,
                returnURL: returnURL
            ),
            expect: .billingPayment
        )
        return try BillingPaymentBody.protoDecoded(from: reply.body)
    }

    /// The current subscription and the entitlements it grants.
    ///
    /// Read the ENTITLEMENTS, never the plan name. A deployment without billing
    /// configured grants everything while reporting the plan as `free`, so a client
    /// that gates features on `plan == "premium"` hides them on exactly the
    /// installation where they are all available.
    public func subscription() async throws -> SubscriptionBody {
        let reply = try await request(.billingStatus, body: nil, expect: .subscription)
        return try SubscriptionBody.protoDecoded(from: reply.body)
    }

    /// Cancels at period end — the paid time already bought is not taken away.
    public func cancelSubscription() async throws -> SubscriptionBody {
        let reply = try await request(.billingCancel, body: nil, expect: .subscription)
        return try SubscriptionBody.protoDecoded(from: reply.body)
    }
}
