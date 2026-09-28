import Foundation
import SyncAppDomain
import SyncAppNetwork

/// Password, second factor and billing, over the protocol.
///
/// Nothing is cached locally. All three are server-owned state with no offline
/// meaning: a password change has to reach the server to be a change at all, and an
/// entitlement read from a stale cache is how a client offers a feature the server has
/// stopped honouring. The one exception is the entitlement STREAM, which replays the
/// last pushed value so a screen has something to draw immediately.
public final class AccountSecurityRepositoryImpl: AccountSecurityRepository, @unchecked Sendable {
    private let client: SyncAppClient
    private let sync: SyncEngine
    /// Used for one thing: the local wipe that has to follow a revoke of THIS session
    /// or an account deletion.
    ///
    /// Injected rather than left to the caller, because "remember to sign out
    /// afterwards" is the kind of obligation that gets honoured on the screen it was
    /// written for and forgotten by the second caller — and the failure is a messenger
    /// still showing a deleted account's chats. `AuthRepository.logout()` already knows
    /// everything that has to go (tokens, cache, outbox, the per-account secret-chat
    /// identity), so this reuses it rather than keeping a second list that can drift.
    private let auth: any AuthRepository

    public init(client: SyncAppClient, sync: SyncEngine, auth: any AuthRepository) {
        self.client = client
        self.sync = sync
        self.auth = auth
    }

    public func changePassword(current: String, new: String) async throws -> Int {
        let reply = try await ErrorMapping.mapped {
            try await client.changePassword(old: current, new: new)
        }
        return Int(reply.sessionsRevoked)
    }

    public func twoFactorState() async throws -> TwoFactorState {
        try await ErrorMapping.mapped { Self.state(from: try await client.totpState()) }
    }

    public func beginTwoFactor() async throws -> TwoFactorSetup {
        let info = try await ErrorMapping.mapped { try await client.beginTOTP() }
        return TwoFactorSetup(secret: info.secret, uri: info.uri)
    }

    public func confirmTwoFactor(code: String) async throws -> TwoFactorState {
        try await ErrorMapping.mapped { Self.state(from: try await client.confirmTOTP(code: code)) }
    }

    public func disableTwoFactor(password: String, code: String) async throws -> TwoFactorState {
        try await ErrorMapping.mapped {
            Self.state(from: try await client.disableTOTP(password: password, code: code))
        }
    }

    // MARK: - Sessions

    public func sessions() async throws -> [DeviceSession] {
        let infos = try await ErrorMapping.mapped { try await client.listSessions() }
        return infos.map { info in
            DeviceSession(
                sessionID: info.sessionID,
                deviceID: info.deviceID,
                platform: info.platform,
                createdAt: Date(timeIntervalSince1970: TimeInterval(info.createdAt) / 1000),
                // 0 means "no expiry recorded", which is not the same as 1970 — a date
                // in the distant past would show as an expired session that still works.
                expiresAt: info.expiresAt == 0
                    ? nil
                    : Date(timeIntervalSince1970: TimeInterval(info.expiresAt) / 1000),
                isCurrent: info.current
            )
        }
    }

    @discardableResult
    public func revokeSession(id: String) async throws -> SessionRevocation {
        let reply = try await ErrorMapping.mapped { try await client.revokeSession(sessionID: id) }
        return try await settle(reply)
    }

    @discardableResult
    public func revokeOtherSessions(includingThisDevice: Bool) async throws -> SessionRevocation {
        let reply = try await ErrorMapping.mapped {
            try await client.revokeOtherSessions(includingThisDevice: includingThisDevice)
        }
        return try await settle(reply)
    }

    /// Turns a revoke reply into the domain result, signing this device out first when
    /// its own session was among those killed.
    ///
    /// The order matters. The gateway sends this reply and then closes, so waiting for
    /// the socket to fail would leave the app briefly holding a token that is already
    /// dead — and reconnecting with it looks, from the server, like someone using a
    /// revoked credential.
    private func settle(_ reply: SessionRevokedBody) async throws -> SessionRevocation {
        if reply.isSelf {
            await auth.logout()
        }
        return SessionRevocation(revoked: Int(reply.revoked), signedOutThisDevice: reply.isSelf)
    }

    // MARK: - Erasure

    public func deleteAccount(password: String, reason: String) async throws -> AccountErasure {
        let reply = try await ErrorMapping.mapped {
            try await client.deleteAccount(password: password, reason: reason)
        }
        // Unconditional, unlike the revoke path: there is no version of a deleted
        // account where this device should keep its cache. Every session is revoked
        // server-side before the reply is sent, so the credentials are already dead and
        // the only thing left is the copy on this phone.
        await auth.logout()
        return AccountErasure(
            userID: reply.userID,
            deletedAt: Date(timeIntervalSince1970: TimeInterval(reply.deletedAt) / 1000)
        )
    }

    // MARK: - Premium

    /// Replays the last known tier, then every push.
    ///
    /// The replay matters more than it looks: `SUBSCRIPTION` is sent on connect and on
    /// change, so a screen opened between the two would otherwise sit empty until
    /// something happened to the subscription — which for most users is never.
    public func entitlements() -> AsyncStream<Entitlements> {
        AsyncStream { continuation in
            let pump = Task {
                for await body in await sync.subscriptions() {
                    continuation.yield(Self.entitlements(from: body))
                }
                continuation.finish()
            }
            continuation.onTermination = { _ in pump.cancel() }
        }
    }

    public func plans(country: String) async throws -> [PlanOffer] {
        let offers = try await ErrorMapping.mapped {
            try await client.billingPlans(country: country).offers
        }
        return offers.map { offer in
            PlanOffer(
                plan: offer.plan,
                amountMinor: offer.amountMinor,
                currency: offer.currency,
                periodDays: Int(offer.periodDays),
                // Unknown methods are DROPPED rather than surfaced as a raw string: a
                // button labelled with a method this build cannot start is worse than
                // no button, and the server will add methods faster than the app ships.
                methods: offer.methods.compactMap(PaymentMethod.init(rawValue:))
            )
        }
    }

    public func checkout(
        plan: String,
        method: PaymentMethod,
        country: String
    ) async throws -> PaymentIntent {
        // The idempotency key is minted HERE and nowhere else.
        //
        // It is the only thing standing between a double-tapped button and a double
        // charge: the server has a unique index on it, so a repeat returns the first
        // payment instead of creating a second. A key the server invented would differ
        // on every retry, which is the same as having none — and the thing being
        // duplicated is somebody's money.
        let key = UUID().uuidString

        let payment = try await ErrorMapping.mapped {
            try await client.checkout(
                plan: plan,
                method: method.rawValue,
                country: country,
                idempotencyKey: key
            )
        }
        return PaymentIntent(
            paymentID: payment.paymentID,
            status: payment.status,
            payURL: payment.payURL.nilIfEmpty.flatMap(URL.init(string:)),
            qrPayload: payment.qrPayload,
            isDeduplicated: payment.deduplicated
        )
    }

    public func cancelSubscription() async throws -> Entitlements {
        let body = try await ErrorMapping.mapped { try await client.cancelSubscription() }
        return Self.entitlements(from: body)
    }

    // MARK: - Mapping

    private static func state(from body: TOTPStateBody) -> TwoFactorState {
        TwoFactorState(
            isEnabled: body.enabled,
            recoveryCodesLeft: Int(body.recoveryLeft),
            recoveryCodes: body.recoveryCodes,
            confirmedAt: WireMapping.date(millis: body.confirmedAtMs)
        )
    }

    private static func entitlements(from body: SubscriptionBody) -> Entitlements {
        Entitlements(
            plan: body.plan,
            status: body.status,
            periodEnd: WireMapping.date(millis: body.periodEnd),
            cancelAtPeriodEnd: body.cancelAtPeriodEnd,
            secretChats: body.secretChats,
            maxUploadBytes: body.maxUploadBytes,
            maxPinnedChats: Int(body.maxPinnedChats),
            folders: body.folders,
            advancedSearch: body.advancedSearch,
            priorityDelivery: body.priorityDelivery,
            voiceTranscription: body.voiceTranscription,
            badge: body.badge
        )
    }
}
