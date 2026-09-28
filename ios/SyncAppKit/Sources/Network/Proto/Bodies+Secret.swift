import Foundation

// Secret-chat wire bodies: the key directory and the durable relay.
//
// None of this existed on iOS. The Crypto module has been complete for a while —
// X3DH, the Double Ratchet, safety numbers, trust pinning — but nothing carried its
// output anywhere, so the primitives were unreachable from the app. These bodies are
// the missing half.

// MARK: - Key directory

/// `KEY_PUBLISH` — this device's long-term identity plus a batch of one-time prekeys.
///
/// Keys travel as base64 STRINGS rather than `bytes`, matching the server schema. It
/// is the wrong encoding for binary and it is kept anyway: unlike ciphertext, a
/// bundle is published once per device and refilled rarely, so the 33% costs
/// kilobytes a month, while changing it would fork the key directory across four
/// clients that must all read each other's bundles.
public struct KeyPublishBody: ProtoMessage, Sendable, Equatable {
    public var identityKey = ""
    public var signingKey = ""
    public var signedPrekey = ""
    public var signedPrekeySig = ""
    public var prekeys: [String] = []

    public init(
        identityKey: String = "",
        signingKey: String = "",
        signedPrekey: String = "",
        signedPrekeySig: String = "",
        prekeys: [String] = []
    ) {
        self.identityKey = identityKey
        self.signingKey = signingKey
        self.signedPrekey = signedPrekey
        self.signedPrekeySig = signedPrekeySig
        self.prekeys = prekeys
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, identityKey)
        w.string(2, signingKey)
        w.string(3, signedPrekey)
        w.string(4, signedPrekeySig)
        w.repeatedString(5, prekeys)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: identityKey = try r.string()
            case 2: signingKey = try r.string()
            case 3: signedPrekey = try r.string()
            case 4: signedPrekeySig = try r.string()
            case 5: prekeys.append(try r.string())
            default: try r.skip(f)
            }
        }
    }
}

/// `KEY_FETCH` (one device) and `KEY_FETCH_ALL` (every device of a user, leaving
/// `deviceID` empty). Both types carry this one body — the gateway distinguishes them
/// by envelope type, not by the shape of what is inside.
public struct KeyFetchBody: ProtoMessage, Sendable, Equatable {
    public var userID = ""
    public var deviceID = ""

    public init(userID: String = "", deviceID: String = "") {
        self.userID = userID
        self.deviceID = deviceID
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, userID)
        w.string(2, deviceID)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: userID = try r.string()
            case 2: deviceID = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `KEY_BUNDLE` — one device's published keys, with one prekey CONSUMED on the way
/// out. Fetching the same device twice yields different `oneTimePrekey` values, and an
/// empty one means the peer's batch has run dry; the handshake then falls back to the
/// signed prekey, which is weaker but still authenticated.
public struct KeyBundleBody: ProtoMessage, Sendable, Equatable {
    public var userID = ""
    public var deviceID = ""
    public var identityKey = ""
    public var signingKey = ""
    public var signedPrekey = ""
    public var signedPrekeySig = ""
    public var oneTimePrekey = ""

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, userID)
        w.string(2, deviceID)
        w.string(3, identityKey)
        w.string(4, signingKey)
        w.string(5, signedPrekey)
        w.string(6, signedPrekeySig)
        w.string(7, oneTimePrekey)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: userID = try r.string()
            case 2: deviceID = try r.string()
            case 3: identityKey = try r.string()
            case 4: signingKey = try r.string()
            case 5: signedPrekey = try r.string()
            case 6: signedPrekeySig = try r.string()
            case 7: oneTimePrekey = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `KEY_STATE` — what the directory holds for this device, answering `KEY_PUBLISH`.
///
/// The reply exists because of a gap that was invisible from both ends. One-time
/// prekeys are consumed one per peer that starts a session, so a popular device runs
/// its own batch down; with an empty batch X3DH silently drops from four
/// Diffie-Hellmans to three, which is weaker and reported to nobody — the peer cannot
/// tell the difference and the owner is never told. Only the OWNER can refill, and the
/// owner is not the party fetching, so the count has to come back here.
public struct KeyStateBody: ProtoMessage, Sendable, Equatable {
    /// Prekeys the directory holds AFTER this publish, capped at its per-device ceiling.
    /// A client tops up below its own threshold rather than at zero: zero has already
    /// cost somebody the stronger handshake.
    public var oneTimePrekeysLeft: Int32 = 0
    /// How long ago the STORED signed prekey first appeared; 0 when this publish
    /// introduced it. A device cannot compute this itself, because it cannot know
    /// whether its own last rotation ever landed.
    public var signedPrekeyAgeMs: Int64 = 0
    /// How many prekeys from this frame were kept. Lower than what was sent when a cap
    /// trimmed it — which a client holding the private halves needs, or it keeps private
    /// keys for public ones nobody can fetch and believes its reserve is larger than it
    /// is.
    public var accepted: Int32 = 0

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.int32(1, oneTimePrekeysLeft)
        w.int64(2, signedPrekeyAgeMs)
        w.int32(3, accepted)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: oneTimePrekeysLeft = try r.int32()
            case 2: signedPrekeyAgeMs = try r.int64()
            case 3: accepted = try r.int32()
            default: try r.skip(f)
            }
        }
    }
}

/// `KEY_BUNDLES` — every device a user has. A secret message must be encrypted once
/// per device, so a peer who signs in on a second phone is a fan-out, not a retarget;
/// sending to one device only would leave the message unreadable on the other.
public struct KeyBundlesBody: ProtoMessage, Sendable, Equatable {
    public var userID = ""
    public var bundles: [KeyBundleBody] = []

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, userID)
        w.repeatedMessage(2, bundles)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: userID = try r.string()
            case 2: bundles.append(try r.message(KeyBundleBody.self))
            default: try r.skip(f)
            }
        }
    }
}

// MARK: - The relay

/// `SECRET_SEND` / `SECRET_RECV` — one ratchet message, addressed to a DEVICE.
///
/// The payload exists in two encodings and which one is populated depends on what the
/// peer negotiated, so read with `payload()` and write with `setPayload(_:_:binary:)`
/// rather than touching the fields.
public struct SecretMsgBody: ProtoMessage, Sendable, Equatable {
    public var toUserID = ""
    public var toDeviceID = ""
    public var fromUserID = ""
    public var fromDeviceID = ""

    /// The legacy TEXT form. `ratchetHeader` is JSON — text that happens to be
    /// structured, so its bytes are its bytes — while `ciphertext` is base64. The
    /// asymmetry is not a design, it is what the first clients happened to send, and
    /// misreading the header as base64 is a mistake that has already been made once.
    public var ratchetHeader = ""
    public var ciphertext = ""

    /// Set only on a replay out of the offline queue, and echoed back in
    /// `SECRET_ACKED` so the server can drop the row. Empty on a live message.
    public var queueID = ""

    /// The binary form, used with peers that negotiated `secretQueue`.
    public var ratchetHeaderBin = Data()
    public var ciphertextBin = Data()

    public init() {}

    /// The payload in whichever encoding arrived, as bytes.
    ///
    /// Binary wins when present: a sender that filled both would be ambiguous, and the
    /// binary fields are the ones that cannot have been mangled by a round trip
    /// through base64.
    public func payload() -> (header: Data, ciphertext: Data)? {
        if !ratchetHeaderBin.isEmpty || !ciphertextBin.isEmpty {
            return (ratchetHeaderBin, ciphertextBin)
        }
        if ratchetHeader.isEmpty && ciphertext.isEmpty { return nil }
        guard let cipher = Data(base64Encoded: ciphertext) else { return nil }
        return (Data(ratchetHeader.utf8), cipher)
    }

    /// Fills exactly one encoding. `binary` comes from the peer's negotiated
    /// capabilities — never from a preference — because a client that cannot read the
    /// bytes fields receives an empty message rather than an error.
    public mutating func setPayload(_ header: Data, _ cipher: Data, binary: Bool) {
        if binary {
            ratchetHeaderBin = header
            ciphertextBin = cipher
            ratchetHeader = ""
            ciphertext = ""
        } else {
            // The header is text on the wire in this form, so it goes back as text.
            ratchetHeader = String(decoding: header, as: UTF8.self)
            ciphertext = cipher.base64EncodedString()
            ratchetHeaderBin = Data()
            ciphertextBin = Data()
        }
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, toUserID)
        w.string(2, toDeviceID)
        w.string(3, fromUserID)
        w.string(4, fromDeviceID)
        w.string(5, ratchetHeader)
        w.string(6, ciphertext)
        w.string(7, queueID)
        w.bytes(8, ratchetHeaderBin)
        w.bytes(9, ciphertextBin)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: toUserID = try r.string()
            case 2: toDeviceID = try r.string()
            case 3: fromUserID = try r.string()
            case 4: fromDeviceID = try r.string()
            case 5: ratchetHeader = try r.string()
            case 6: ciphertext = try r.string()
            case 7: queueID = try r.string()
            case 8: ratchetHeaderBin = try r.bytesField()
            case 9: ciphertextBin = try r.bytesField()
            default: try r.skip(f)
            }
        }
    }
}

/// `SECRET_ACK` — what the relay actually did.
///
/// Before this the relay answered nothing at all: it published to whichever nodes held
/// the recipient, discarded the count, and returned. A message to an offline peer was
/// dropped silently while the sender's UI drew "sent", which is the worst of the three
/// possible outcomes because it is indistinguishable from success.
public struct SecretAckBody: ProtoMessage, Sendable, Equatable {
    public var toUserID = ""
    public var toDeviceID = ""
    public var devices: Int32 = 0
    public var queued = false

    public init() {}

    /// Handed to at least one live device.
    public var delivered: Bool { devices > 0 }
    /// Stored for later — "sent", not "delivered".
    public var pending: Bool { devices == 0 && queued }
    /// Nowhere to put it: the peer has published no device keys at all, so waiting
    /// will not help and the UI should say so rather than spin.
    public var undeliverable: Bool { devices == 0 && !queued }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, toUserID)
        w.string(2, toDeviceID)
        w.int32(3, devices)
        w.bool(4, queued)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: toUserID = try r.string()
            case 2: toDeviceID = try r.string()
            case 3: devices = try r.int32()
            case 4: queued = try r.bool()
            default: try r.skip(f)
            }
        }
    }
}

/// `SECRET_SYNC` — "replay what I missed while I was gone".
///
/// Sent once per connect, then repeated with the returned cursor until `done`. The
/// replay arrives as ordinary `SECRET_RECV` frames carrying a `queueID`, so the
/// receive path needs no separate branch for history.
public struct SecretSyncBody: ProtoMessage, Sendable, Equatable {
    public var after = ""
    public var limit: Int32 = 0

    public init(after: String = "", limit: Int32 = 0) {
        self.after = after
        self.limit = limit
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, after)
        w.int32(2, limit)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: after = try r.string()
            case 2: limit = try r.int32()
            default: try r.skip(f)
            }
        }
    }
}

/// `SECRET_SYNCED` — how many frames the replay sent, and where to resume.
public struct SecretSyncedBody: ProtoMessage, Sendable, Equatable {
    public var count: Int32 = 0
    public var nextAfter = ""
    public var done = false

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.int32(1, count)
        w.string(2, nextAfter)
        w.bool(3, done)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: count = try r.int32()
            case 2: nextAfter = try r.string()
            case 3: done = try r.bool()
            default: try r.skip(f)
            }
        }
    }
}

/// `SECRET_ACKED` — the queue rows this device has now stored locally, so the server
/// can drop them.
///
/// Acking is deliberately NOT automatic on receipt: a frame that arrived but whose
/// plaintext was never written to disk must stay in the queue, or a crash between the
/// two loses the message while the server believes it was delivered.
public struct SecretAckedBody: ProtoMessage, Sendable, Equatable {
    public var ids: [String] = []

    public init(ids: [String] = []) { self.ids = ids }

    public func encode(to w: inout ProtoWriter) {
        w.repeatedString(1, ids)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: ids.append(try r.string())
            default: try r.skip(f)
            }
        }
    }
}
