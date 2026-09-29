import Foundation

/// Password, second factor, and the Premium tier.
///
/// Its own protocol rather than more methods on `AuthRepository`, because the lifetime
/// is different: `AuthRepository` answers "who is signed in", which every screen needs,
/// while these serve two settings screens and nothing else. Keeping them apart means
/// the login path does not grow a dependency on billing.
public protocol AccountSecurityRepository: Sendable {

    /// Changes the password and reports how many OTHER sessions were signed out.
    ///
    /// Signing them out is not optional: a password change that leaves an attacker's
    /// session alive has achieved nothing. This session survives, so the user can
    /// finish securing the account without being thrown back to the login screen.
    func changePassword(current: String, new: String) async throws -> Int

    /// The current second-factor state.
    func twoFactorState() async throws -> TwoFactorState

    /// Begins enrolment. Nothing is enforced yet.
    ///
    /// Two steps on purpose: a user who scans a QR code wrongly and never confirms has
    /// not locked themselves out of their own account.
    func beginTwoFactor() async throws -> TwoFactorSetup

    /// Confirms enrolment with a code from the authenticator, proving the secret
    /// actually reached somewhere the user can read it. The RECOVERY CODES come back
    /// here, once — they are stored only as hashes, so there is no second chance.
    func confirmTwoFactor(code: String) async throws -> TwoFactorState

    /// Turns it off. Needs the password AND a code, so a stolen session cannot remove
    /// the factor that keeps its holder out of the next login.
    func disableTwoFactor(password: String, code: String) async throws -> TwoFactorState

    // MARK: - Sessions
    //
    // These belong next to the password and the second factor because they answer the
    // same question — "is anyone else in my account?" — and because the honest answer
    // to a lost phone is all three: change the password, keep the factor, and end the
    // session that device is holding. Until they existed here, signing out was a purely
    // local gesture on this platform: the app forgot its token while the session stayed
    // valid on the server until it expired, so a lost phone kept access for the whole
    // TTL and nothing in the app could say so, let alone stop it.

    /// Every live session of this account.
    ///
    /// No credential comes back for any of them, this one included. The list exists so a
    /// person can recognise a device well enough to decide whether to end it; handing
    /// each device the credentials of every other would turn a read into a way to move
    /// between them, which is the opposite of the point.
    func sessions() async throws -> [DeviceSession]

    /// Ends one session.
    ///
    /// Ending the one marked `isCurrent` signs THIS device out, and the implementation
    /// is responsible for the local wipe that follows — the caller gets back a result
    /// that says it happened, not a chore to remember.
    @discardableResult
    func revokeSession(id: String) async throws -> SessionRevocation

    /// Ends every OTHER session, keeping this one — what a person reaches for after
    /// losing a device.
    ///
    /// `includingThisDevice` extends it to this one as well. A parameter rather than a
    /// second method, because "sign out everywhere, including here" and "sign out
    /// everywhere but here" are different intentions and the difference should be
    /// stated rather than implied by which call was picked.
    @discardableResult
    func revokeOtherSessions(includingThisDevice: Bool) async throws -> SessionRevocation

    // MARK: - Erasure

    /// Erases this account, then wipes every local trace of it.
    ///
    /// The password is required even though the session is already authenticated, and
    /// that is the point of asking: a token lives on the device, so without it anyone
    /// holding an unlocked phone could destroy the account behind it.
    ///
    /// What actually goes is worth stating to the user before they confirm, because it
    /// is not what "delete everything I sent" sounds like: sessions, devices, push
    /// tokens, contacts on both sides, drafts, read cursors, reactions, votes, invites,
    /// pins and memberships are deleted, while MESSAGES are anonymised in place —
    /// their text, media and sender erased and the row tombstoned. A message is content
    /// in somebody else's conversation, so the conversations keep their shape and the
    /// person disappears from them.
    func deleteAccount(password: String, reason: String) async throws -> AccountErasure

    // MARK: - Premium

    /// The tier and what it grants, now and on every change.
    ///
    /// A stream because a subscription lapses, renews or is cancelled without this
    /// client asking — the server pushes it — and a screen that read it once at launch
    /// goes on offering features the server has started refusing.
    func entitlements() -> AsyncStream<Entitlements>

    /// The plans on offer for a country, with a price per payment method.
    func plans(country: String) async throws -> [PlanOffer]

    /// Starts a payment and returns where to send the user.
    func checkout(plan: String, method: PaymentMethod, country: String) async throws -> PaymentIntent

    /// Cancels at period end. The time already paid for is not taken away.
    func cancelSubscription() async throws -> Entitlements
}

/// One live session, as the device list shows it.
///
/// `Identifiable` so a SwiftUI list can present it without a synthesised key — declared
/// on the type rather than as a retroactive conformance in the presentation layer, which
/// is where the type's identity belongs.
public struct DeviceSession: Equatable, Sendable, Identifiable {
    public var id: String { sessionID }

    public var sessionID: String
    public var deviceID: String
    /// `ios` | `android` | `web` | `desktop` | `cli`, as that device declared itself in
    /// its handshake. Client-asserted, so it is a label to show and never something to
    /// decide anything on.
    public var platform: String
    public var createdAt: Date
    /// When it lapses on its own. The TTL is rolling — using a session pushes this out —
    /// so a date far in the future means the device is still in daily use, which is
    /// exactly what someone scanning this list wants to notice.
    public var expiresAt: Date?
    /// The session this app is signed in with. Worth labelling, and worth confirming
    /// before revoking: ending it signs this device out.
    public var isCurrent: Bool

    public init(
        sessionID: String,
        deviceID: String,
        platform: String,
        createdAt: Date,
        expiresAt: Date?,
        isCurrent: Bool
    ) {
        self.sessionID = sessionID
        self.deviceID = deviceID
        self.platform = platform
        self.createdAt = createdAt
        self.expiresAt = expiresAt
        self.isCurrent = isCurrent
    }
}

/// What a revoke actually did.
public struct SessionRevocation: Equatable, Sendable {
    /// How many sessions ended. The count matters rather than a bare success: a request
    /// to sign out five devices that signed out one should say so, not show a checkmark.
    public var revoked: Int
    /// This device was among them, so the app has already been signed out locally and
    /// the UI must return to the login screen.
    public var signedOutThisDevice: Bool

    public init(revoked: Int, signedOutThisDevice: Bool) {
        self.revoked = revoked
        self.signedOutThisDevice = signedOutThisDevice
    }
}

/// Confirmation that the account is gone. Local state has already been wiped by the
/// time this is returned.
public struct AccountErasure: Equatable, Sendable {
    public var userID: String
    public var deletedAt: Date

    public init(userID: String, deletedAt: Date) {
        self.userID = userID
        self.deletedAt = deletedAt
    }
}

/// Whether a second factor is on, and how much recovery is left.
public struct TwoFactorState: Equatable, Sendable {
    public var isEnabled: Bool
    /// Unused recovery codes remaining. A user down to their last one is a user about
    /// to be locked out, which is worth showing before it happens.
    public var recoveryCodesLeft: Int
    /// Non-empty ONLY in the reply to a confirmation. The stored form is an argon2id
    /// hash, so this is the single moment they can be displayed.
    public var recoveryCodes: [String]
    public var confirmedAt: Date?

    public init(
        isEnabled: Bool = false,
        recoveryCodesLeft: Int = 0,
        recoveryCodes: [String] = [],
        confirmedAt: Date? = nil
    ) {
        self.isEnabled = isEnabled
        self.recoveryCodesLeft = recoveryCodesLeft
        self.recoveryCodes = recoveryCodes
        self.confirmedAt = confirmedAt
    }
}

/// What enrolment hands back before confirmation.
public struct TwoFactorSetup: Equatable, Sendable {
    /// Base32, for the people who type it in by hand.
    public var secret: String
    /// The `otpauth://` URI, for a QR code.
    public var uri: String

    public init(secret: String, uri: String) {
        self.secret = secret
        self.uri = uri
    }
}

/// What the tier grants.
///
/// Explicit flags rather than a plan name, deliberately. A client that switches on
/// `plan == "premium"` has a second copy of the entitlement policy, and the two drift —
/// and worse, a deployment with no billing configured reports the plan as `free` while
/// granting everything, so name-based gating hides every feature on exactly the
/// installation where they are all available.
public struct Entitlements: Equatable, Sendable {
    public var plan: String
    public var status: String
    public var periodEnd: Date?
    public var cancelAtPeriodEnd: Bool

    public var secretChats: Bool
    public var maxUploadBytes: Int64
    public var maxPinnedChats: Int
    public var folders: Bool
    public var advancedSearch: Bool
    public var priorityDelivery: Bool
    public var voiceTranscription: Bool
    public var badge: Bool
    /// The accent palettes beyond the default one.
    public var customThemes: Bool

    public init(
        plan: String = "free",
        status: String = "",
        periodEnd: Date? = nil,
        cancelAtPeriodEnd: Bool = false,
        secretChats: Bool = false,
        maxUploadBytes: Int64 = 0,
        maxPinnedChats: Int = 0,
        folders: Bool = false,
        advancedSearch: Bool = false,
        priorityDelivery: Bool = false,
        voiceTranscription: Bool = false,
        badge: Bool = false,
        customThemes: Bool = false
    ) {
        self.plan = plan
        self.status = status
        self.periodEnd = periodEnd
        self.cancelAtPeriodEnd = cancelAtPeriodEnd
        self.secretChats = secretChats
        self.maxUploadBytes = maxUploadBytes
        self.maxPinnedChats = maxPinnedChats
        self.folders = folders
        self.advancedSearch = advancedSearch
        self.priorityDelivery = priorityDelivery
        self.voiceTranscription = voiceTranscription
        self.badge = badge
        self.customThemes = customThemes
    }

    /// What to draw before the server has answered.
    ///
    /// Everything OFF, which is the safe direction: a feature shown as available and
    /// then refused is worse than one that appears a moment late.
    ///
    /// This is NOT the "no billing configured" case, and conflating the two would be
    /// the mistake worth avoiding. `BILLING_STATUS` always answers — a gateway with no
    /// billing service replies with everything granted rather than with an error — so
    /// a deployment that does not sell tiers still sends real entitlements on connect.
    /// The only time this value is on screen is the moment before the first reply.
    public static let unknown = Entitlements(plan: "free")

    /// Whether the account is on the paid plan right now.
    ///
    /// The plan has to be exactly `premium`: an unknown name from a newer server is
    /// not proof of payment. The period is checked as well as the plan, because a
    /// cancelled subscription keeps its access until the paid period ends and loses
    /// it after — hiding Premium at cancellation would take away what was bought,
    /// and trusting the plan alone would keep showing it after it lapsed.
    ///
    /// This answers "is this account paying", for the plan label and the cancel
    /// button. Whether a FEATURE is available is the entitlement's question: a
    /// deployment with no payment provider reports `free` and grants everything.
    public func isPremiumActive(now: Date = Date()) -> Bool {
        guard plan == "premium" else { return false }
        guard let periodEnd else { return true }
        return periodEnd > now
    }
}

/// How a payment is taken. The server decides which are available from the country —
/// the client offers a choice among what it was told, never a provider of its own.
public enum PaymentMethod: String, Equatable, Sendable, CaseIterable {
    case card
    case sbp
}

/// One purchasable plan.
public struct PlanOffer: Equatable, Sendable, Identifiable {
    public var plan: String
    /// The price in the currency's MINOR unit — kopeks, cents. An integer, never a
    /// float: a price is an exact quantity and binary floating point cannot hold 0.01.
    public var amountMinor: Int64
    public var currency: String
    public var periodDays: Int
    public var methods: [PaymentMethod]

    public var id: String { plan }

    public init(
        plan: String,
        amountMinor: Int64,
        currency: String,
        periodDays: Int,
        methods: [PaymentMethod]
    ) {
        self.plan = plan
        self.amountMinor = amountMinor
        self.currency = currency
        self.periodDays = periodDays
        self.methods = methods
    }

    /// The price as text, formatted by integer arithmetic.
    ///
    /// Never through `Double`: dividing by 100 and formatting is how a price becomes
    /// 9.989999999999999, and a payment screen is the last place to explain floating
    /// point to a user.
    public func priceText() -> String {
        let major = amountMinor / 100
        let minor = amountMinor % 100
        let amount = minor == 0 ? "\(major)" : "\(major)." + String(format: "%02d", minor)
        return "\(amount) \(currency)"
    }
}

/// Where to send the user to finish paying.
/// `Identifiable` because a SwiftUI `sheet(item:)` presents one — declared here rather
/// than as a retroactive conformance in the presentation layer, which Swift 6 warns
/// about and which would put the type's identity somewhere other than the type.
public struct PaymentIntent: Equatable, Sendable, Identifiable {
    public var id: String { paymentID }

    public var paymentID: String
    public var status: String
    /// Followed in a browser. Empty for a method that has no redirect.
    public var payURL: URL?
    /// DISPLAYED as a QR code. An SBP payload is not a URL and must not be opened as
    /// one — confusing the two is how a working payment turns into a dead link.
    public var qrPayload: String
    /// The server returned an EXISTING payment for this idempotency key, rather than
    /// creating a second one. Worth knowing: it means the button was tapped twice, not
    /// that a new charge was made.
    public var isDeduplicated: Bool

    public init(
        paymentID: String,
        status: String,
        payURL: URL?,
        qrPayload: String,
        isDeduplicated: Bool
    ) {
        self.paymentID = paymentID
        self.status = status
        self.payURL = payURL
        self.qrPayload = qrPayload
        self.isDeduplicated = isDeduplicated
    }
}
