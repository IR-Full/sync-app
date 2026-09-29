import Foundation

// Envelope bodies for per-member chat settings, account security and billing.
// Field numbers come from server/proto/syncapp/v1/body.proto and must never be
// renumbered independently of it: the number is the only thing protobuf puts on the
// wire, so two swapped ones produce a frame that both sides encode and decode
// happily and that means something else entirely.

// MARK: - Per-member chat settings

/// `CHAT_FLAGS` — this account's own settings for one chat.
///
/// `muted` existed in the server schema from its first migration with nothing reading
/// it and no message to set it, so muting a chat was impossible while looking
/// supported from every other angle.
///
/// `mutedUntil` is a DEADLINE in unix millis, not a flag: "mute for eight hours" is
/// what muting usually means and a boolean cannot express it, while a very distant
/// deadline expresses "forever". 0 unmutes.
///
/// All three are sent every time — the server REPLACES rather than patches — so a
/// caller changing the pin must send the mute it already has, or drop it.
public struct ChatFlagsBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var mutedUntil: Int64 = 0
    public var pinned = false
    public var archived = false

    public init(chatID: String = "", mutedUntil: Int64 = 0, pinned: Bool = false, archived: Bool = false) {
        self.chatID = chatID
        self.mutedUntil = mutedUntil
        self.pinned = pinned
        self.archived = archived
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.int64(2, mutedUntil)
        w.bool(3, pinned)
        w.bool(4, archived)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: mutedUntil = try r.int64()
            case 3: pinned = try r.bool()
            case 4: archived = try r.bool()
            default: try r.skip(f)
            }
        }
    }
}

/// `CHAT_FLAGS_SET` — the flags now STORED, which is not always what was asked for.
///
/// The server normalises a mute deadline that has already passed to zero, so a client
/// that kept its own request would show a chat as muted until something contradicted
/// it.
public struct ChatFlagsSetBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var mutedUntil: Int64 = 0
    public var pinned = false
    public var archived = false

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.int64(2, mutedUntil)
        w.bool(3, pinned)
        w.bool(4, archived)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: mutedUntil = try r.int64()
            case 3: pinned = try r.bool()
            case 4: archived = try r.bool()
            default: try r.skip(f)
            }
        }
    }
}

// MARK: - Account security
//
// None of this existed. The password could not be CHANGED by any path — so a leaked
// one meant a permanently lost account, since revoking every session does not stop
// whoever knows the password from signing in again — and there was no second factor
// at all, leaving one interceptable credential with nothing behind it.

/// `PASSWORD_CHANGE` — replaces the password.
///
/// The old one is required even on an authenticated connection: a session token is
/// enough to ACT as the account but not to replace its credential, or a stolen token
/// becomes permanent ownership — which is exactly what a password change is meant to
/// take back.
public struct PasswordChangeBody: ProtoMessage, Sendable, Equatable {
    public var oldPassword = ""
    public var newPassword = ""

    public init(oldPassword: String = "", newPassword: String = "") {
        self.oldPassword = oldPassword
        self.newPassword = newPassword
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, oldPassword)
        w.string(2, newPassword)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: oldPassword = try r.string()
            case 2: newPassword = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `PASSWORD_CHANGED` — how many OTHER sessions were signed out.
///
/// The caller's own survives, so they are not logged out of the device they are using
/// to secure the account. The count is worth showing: a password change is usually a
/// response to suspecting someone else has access, and "four other devices were
/// signed out" is the confirmation the user is looking for.
public struct PasswordChangedBody: ProtoMessage, Sendable, Equatable {
    public var sessionsRevoked: Int32 = 0

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.int32(1, sessionsRevoked)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: sessionsRevoked = try r.int32()
            default: try r.skip(f)
            }
        }
    }
}

/// `TOTP_SETUP_INFO` — the new secret and the `otpauth://` URI to render as a QR code.
///
/// The factor is NOT yet enforced at this point. Enrolment is two steps on purpose:
/// without a confirmation step a mis-scanned QR code locks an account out of itself,
/// which is the most common way a 2FA rollout goes wrong.
public struct TOTPSetupInfoBody: ProtoMessage, Sendable, Equatable {
    public var secret = ""
    public var uri = ""

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, secret)
        w.string(2, uri)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: secret = try r.string()
            case 2: uri = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `TOTP_CONFIRM` — proves the user can produce a code, which is what enrols them.
public struct TOTPConfirmBody: ProtoMessage, Sendable, Equatable {
    public var code = ""

    public init(code: String = "") { self.code = code }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, code)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: code = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `TOTP_DISABLE` — removes the factor.
///
/// Password AND code, because someone holding only a stolen session token must not be
/// able to take off the factor that would keep them out of the next login.
public struct TOTPDisableBody: ProtoMessage, Sendable, Equatable {
    public var password = ""
    public var code = ""

    public init(password: String = "", code: String = "") {
        self.password = password
        self.code = code
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, password)
        w.string(2, code)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: password = try r.string()
            case 2: code = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `TOTP_STATE` — the current second-factor state.
///
/// `recoveryCodes` is populated ONLY in the reply to `TOTP_CONFIRM`. The stored form
/// is an argon2id hash, so there is nothing to show later — which is what makes a leak
/// of that table worthless, and the reason a screen has to put them in front of the
/// user immediately rather than logging them and moving on.
///
/// `recoveryLeft` is exposed because losing the last code and the phone together is
/// the state there is no way back from, and a screen that cannot see the count cannot
/// warn before it happens.
public struct TOTPStateBody: ProtoMessage, Sendable, Equatable {
    public var enabled = false
    public var recoveryLeft: Int32 = 0
    public var recoveryCodes: [String] = []
    public var confirmedAtMs: Int64 = 0

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.bool(1, enabled)
        w.int32(2, recoveryLeft)
        w.repeatedString(3, recoveryCodes)
        w.int64(4, confirmedAtMs)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: enabled = try r.bool()
            case 2: recoveryLeft = try r.int32()
            case 3: recoveryCodes.append(try r.string())
            case 4: confirmedAtMs = try r.int64()
            default: try r.skip(f)
            }
        }
    }
}

// MARK: - Billing

/// `BILLING_PLANS` — asks what is purchasable.
///
/// `country` comes from the CLIENT rather than a server-side GeoIP lookup: the user
/// knows which market they are in, and a lookup that guesses wrong offers a payment
/// method their bank does not support.
public struct BillingPlansBody: ProtoMessage, Sendable, Equatable {
    public var country = ""

    public init(country: String = "") { self.country = country }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, country)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: country = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// One purchasable plan in one market.
///
/// `amountMinor` is an INTEGER in the currency's minor unit — kopeks, cents. Never a
/// float and never a pre-formatted string: a price is an exact quantity, binary
/// floating point cannot hold 0.01, and a client that renders the price through a
/// `Double` eventually shows a number that differs from what it charges.
public struct PlanOfferBody: ProtoMessage, Sendable, Equatable {
    public var plan = ""
    public var amountMinor: Int64 = 0
    public var currency = ""
    public var periodDays: Int32 = 0
    public var methods: [String] = []

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, plan)
        w.int64(2, amountMinor)
        w.string(3, currency)
        w.int32(4, periodDays)
        w.repeatedString(5, methods)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: plan = try r.string()
            case 2: amountMinor = try r.int64()
            case 3: currency = try r.string()
            case 4: periodDays = try r.int32()
            case 5: methods.append(try r.string())
            default: try r.skip(f)
            }
        }
    }
}

/// `BILLING_OFFERS` — the catalogue for one market.
public struct BillingOffersBody: ProtoMessage, Sendable, Equatable {
    public var offers: [PlanOfferBody] = []

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.repeatedMessage(1, offers)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: offers.append(try r.message(PlanOfferBody.self))
            default: try r.skip(f)
            }
        }
    }
}

/// `BILLING_CHECKOUT` — starts a payment.
public struct BillingCheckoutBody: ProtoMessage, Sendable, Equatable {
    public var plan = ""
    public var method = ""
    /// REQUIRED, and the client's own.
    ///
    /// A retried checkout must reach the same payment rather than starting a second
    /// one, and only the client knows two requests are the same request — a key the
    /// server invents differs on every retry, which is the same as having none. The
    /// thing being duplicated otherwise is somebody's money.
    public var idempotencyKey = ""
    public var country = ""
    public var returnURL = ""

    public init(
        plan: String = "",
        method: String = "",
        idempotencyKey: String = "",
        country: String = "",
        returnURL: String = ""
    ) {
        self.plan = plan
        self.method = method
        self.idempotencyKey = idempotencyKey
        self.country = country
        self.returnURL = returnURL
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, plan)
        w.string(2, method)
        w.string(3, idempotencyKey)
        w.string(4, country)
        w.string(5, returnURL)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: plan = try r.string()
            case 2: method = try r.string()
            case 3: idempotencyKey = try r.string()
            case 4: country = try r.string()
            case 5: returnURL = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `BILLING_PAYMENT` — where to send the user.
///
/// `payURL` is FOLLOWED; `qrPayload` is DISPLAYED as a QR code. They are not
/// interchangeable — an SBP payload is not a URL, and a client that renders it as a
/// link produces a broken one.
public struct BillingPaymentBody: ProtoMessage, Sendable, Equatable {
    public var paymentID = ""
    public var status = ""
    public var amountMinor: Int64 = 0
    public var currency = ""
    public var payURL = ""
    public var qrPayload = ""
    /// True when this repeated an earlier request and no new charge was made.
    public var deduplicated = false

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, paymentID)
        w.string(2, status)
        w.int64(3, amountMinor)
        w.string(4, currency)
        w.string(5, payURL)
        w.string(6, qrPayload)
        w.bool(7, deduplicated)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: paymentID = try r.string()
            case 2: status = try r.string()
            case 3: amountMinor = try r.int64()
            case 4: currency = try r.string()
            case 5: payURL = try r.string()
            case 6: qrPayload = try r.string()
            case 7: deduplicated = try r.bool()
            default: try r.skip(f)
            }
        }
    }
}

/// `SUBSCRIPTION` — the caller's tier and what it grants.
///
/// Sent as a reply to `BILLING_STATUS` and PUSHED whenever the subscription changes —
/// a payment settling, a cancellation, a period lapsing. The push is what keeps a
/// client from offering features the server has started refusing, which a user
/// experiences as the app breaking rather than as a plan ending.
///
/// The entitlements are sent EXPLICITLY rather than derived from the plan name. A
/// client that mapped "premium" to a set of capabilities would hold a second copy of
/// the policy, and the two drift: a server raising the upload ceiling would need every
/// client updated before anybody could use it.
public struct SubscriptionBody: ProtoMessage, Sendable, Equatable {
    public var plan = ""
    public var status = ""
    /// When access lapses without a renewal, unix millis; 0 = no expiry.
    public var periodEnd: Int64 = 0
    public var cancelAtPeriodEnd = false

    public var secretChats = false
    public var maxUploadBytes: Int64 = 0
    public var maxPinnedChats: Int32 = 0
    public var folders = false
    public var advancedSearch = false
    public var priorityDelivery = false
    public var voiceTranscription = false
    public var badge = false
    /// Unlocks the accent palettes in appearance settings. An entitlement like the
    /// rest: the client must not decide who is paying by reading the plan name.
    public var customThemes = false

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, plan)
        w.string(2, status)
        w.int64(3, periodEnd)
        w.bool(4, cancelAtPeriodEnd)
        w.bool(5, secretChats)
        w.int64(6, maxUploadBytes)
        w.int32(7, maxPinnedChats)
        w.bool(8, folders)
        w.bool(9, advancedSearch)
        w.bool(10, priorityDelivery)
        w.bool(11, voiceTranscription)
        w.bool(12, badge)
        w.bool(13, customThemes)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: plan = try r.string()
            case 2: status = try r.string()
            case 3: periodEnd = try r.int64()
            case 4: cancelAtPeriodEnd = try r.bool()
            case 5: secretChats = try r.bool()
            case 6: maxUploadBytes = try r.int64()
            case 7: maxPinnedChats = try r.int32()
            case 8: folders = try r.bool()
            case 9: advancedSearch = try r.bool()
            case 10: priorityDelivery = try r.bool()
            case 11: voiceTranscription = try r.bool()
            case 12: badge = try r.bool()
            case 13: customThemes = try r.bool()
            default: try r.skip(f)
            }
        }
    }
}
