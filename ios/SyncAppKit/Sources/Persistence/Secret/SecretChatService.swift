import Foundation
import SyncAppCrypto
import SyncAppDomain
import SyncAppNetwork

/// Secret chats end to end: this device's keys, the per-device ratchets, the fan-out on
/// send, and the offline queue on receive.
///
/// The crypto has been in `Sources/Crypto` for a long time with nothing calling it —
/// and, as it turns out, without even being compiled. This is the part that was
/// missing, and most of what is interesting here is not cryptography but bookkeeping,
/// because that is where the failure modes are:
///
/// - a message is encrypted once per peer DEVICE, and the device list changes between
///   messages, so the fan-out is recomputed every time rather than cached;
/// - a session advances per message, so it is written to disk on every send and every
///   receive — one skipped write and the chain is broken;
/// - a queued message is acked only after its plaintext is stored, or a crash between
///   the two loses it with the server believing it was delivered.
public actor SecretChatService {

    /// Where the identity lives: the Keychain, not the SQLite cache. These are
    /// long-term private keys, and the cache rides along in an encrypted backup while
    /// `thisDeviceOnly` Keychain items do not.
    enum Key {
        static let identity = "secret.identity"
        static let signing = "secret.signing"
        static let signedPreKey = "secret.signed_prekey"
        static let oneTimePreKeys = "secret.one_time_prekeys"
        /// When the current signed prekey was minted, and the one it replaced. Stored
        /// beside the key rather than inside it so an install from before rotation
        /// existed still decodes: a missing record reads as "minted now", which is the
        /// conservative answer (see `loadSignedPreKeyMeta`).
        static let signedPreKeyMeta = "secret.signed_prekey_meta"
        /// Public halves the directory has CONFIRMED it stored, so each one-time prekey
        /// is offered exactly once.
        static let publishedPreKeys = "secret.published_prekeys"
        /// Peer identity keys pinned on first use (`TrustStore`). In the Keychain rather
        /// than the cache: a pin that can be edited as easily as the data it guards
        /// guards nothing.
        static let trust = "secret.trust"

        /// Every key this service owns, so logout can remove them all without a second
        /// list somewhere else to fall out of step with this one.
        static let all = [
            identity, signing, signedPreKey, oneTimePreKeys, signedPreKeyMeta, publishedPreKeys, trust,
        ]
    }

    /// How many one-time prekeys a publish uploads.
    ///
    /// Each is consumed by one peer starting one session, so this is a budget for how
    /// many conversations can BEGIN while this device is offline. Running dry is not a
    /// failure — the handshake falls back to the signed prekey — but the fallback loses
    /// the one-time key's contribution, so the batch is topped up on every publish.
    private static let preKeyBatch = 50

    /// Top back up once the DIRECTORY reports fewer than this many left.
    ///
    /// The directory's count is the one that matters and the only one that is knowable:
    /// the local map shrinks when a message decrypts with a key, which misses every
    /// fetch that never became a message — a peer that gave up, or a fan-out where
    /// another device answered. Refilling at zero means the batch is empty for as long
    /// as it takes to notice, and empty costs the stronger handshake.
    private static let preKeyLowWater = preKeyBatch / 4

    /// Ceiling on prekeys kept LOCALLY, mirroring the directory's own cap.
    ///
    /// The map has to be able to grow past `preKeyBatch`: when the directory runs low,
    /// the keys it is missing are ones it already served, so topping it up means minting
    /// NEW pairs while the old private halves are still needed for first messages in
    /// flight. Unbounded, it grows by a batch each time the device gets popular. Over
    /// the cap the oldest go, which is what the directory does with them too.
    private static let maxLocalPreKeys = 256

    /// How long a signed prekey is served before a fresh one replaces it.
    ///
    /// The signed prekey is the medium-term key: an initiator does DH against it to
    /// start a session, so a device that never rotates has ONE key standing behind every
    /// conversation it will ever receive. Compromising it long afterwards opens the
    /// initial handshake of all of them. Seven days is the interval Signal documents.
    private static let signedPreKeyMaxAge: TimeInterval = 7 * 24 * 60 * 60
    /// One page of the offline replay.
    private static let syncPage: Int32 = 50

    /// Rounds of `syncQueue` before giving up.
    ///
    /// Bounded rather than `while true`: a server answering `done: false` with an
    /// unchanging cursor would spin here forever, and fourteen days of queue at fifty
    /// per page cannot legitimately need this many rounds.
    private static let maxSyncRounds = 200

    private let client: SyncAppClient
    private let store: LocalStore
    private let keychain: KeychainStore
    private let deviceID: String
    private let log: SecretLog

    private var identity: SecretKeyPair?
    private var signing: SigningKeyPair?
    private var signedPreKey: SecretKeyPair?

    /// The signed prekey this device served BEFORE the last rotation.
    ///
    /// Kept for one generation because rotation is not atomic across the network: a peer
    /// may have fetched the old bundle seconds earlier and be about to send its first
    /// message against it. Without a grace copy that message is undecryptable and the
    /// sender is never told — the relay reports delivery, not decryption. One
    /// generation against a weekly rotation means a bundle has to be a week stale to fail.
    private var previousSignedPreKey: SecretKeyPair?

    /// When `signedPreKey` was minted. Drives rotation.
    private var signedPreKeyCreatedAt = Date()

    /// Public halves (base64) the directory has confirmed it stored.
    ///
    /// Publishing APPENDS on the server rather than replacing, so resending the whole
    /// map on every connect — which is what this did — filed each public key again. A
    /// one-time prekey stored twice can be handed to two peers, which is the one thing
    /// it exists not to be.
    private var publishedPreKeys: Set<String> = []

    /// Private halves of the published one-time prekeys, keyed by public key (base64).
    ///
    /// Retained because the RESPONDER needs the private half of whichever prekey the
    /// initiator consumed, and nothing on the wire says which one that was — the
    /// envelope deliberately does not name it, since that would tell the relay
    /// something about the handshake it has no business knowing. Losing this map makes
    /// every inbound first message from a new peer undecryptable.
    private var oneTimePreKeys: [String: Data] = [:]

    /// Sessions in memory, so a burst to one device does not reload and re-serialise
    /// per message. The disk copy stays authoritative.
    private var sessions: [String: RatchetSession] = [:]

    /// The X3DH bootstrap still owed to a peer device: its ephemeral public key, keyed
    /// by session.
    ///
    /// Present means "keep attaching `ik`/`ek` to every message for this device", and it
    /// clears only when something from that device DECRYPTS — the one proof that the
    /// peer actually established a session.
    ///
    /// Two failure modes made this shape necessary, and the first is subtle. Tracking a
    /// boolean "already bootstrapped" and keeping the ephemeral for only the first
    /// attempt means a retry after a failed send attaches `ik` with no `ek`, because the
    /// ephemeral is gone — and a half-envelope is undecryptable rather than merely
    /// wasteful. Storing the key itself makes the two fields travel together or not at
    /// all. The second: a first message that was queued and then expired unread leaves
    /// the peer with no session, so a strict "first message only" rule would make every
    /// later message undecryptable too. Repeating ~90 bytes until the peer replies is
    /// the cheaper mistake.
    private var pendingBootstrap: [String: Data] = [:]

    /// Pinned peer identities, loaded from the Keychain on first use.
    private var trust: TrustStore?

    public init(
        client: SyncAppClient,
        store: LocalStore,
        keychain: KeychainStore = KeychainStore(),
        deviceID: String,
        log: SecretLog = .init()
    ) {
        self.client = client
        self.store = store
        self.keychain = keychain
        self.deviceID = deviceID
        self.log = log
    }

    // MARK: - This device's keys

    /// Generates the identity if this device has none, rotates what is stale, publishes
    /// a bundle, and folds the directory's answer back in.
    ///
    /// Called on every connect, unconditionally. A device whose publish was lost is
    /// INVISIBLE to the key directory, and the only symptom is that secret messages to
    /// this account report `undeliverable` forever; one redundant publish per connect is
    /// far cheaper than diagnosing that, and the directory upserts, so repeating is
    /// harmless.
    ///
    /// The publish is a REQUEST now. `KEY_STATE` carries what the directory holds, and
    /// that is the only place it can come from: one-time prekeys are consumed by PEERS
    /// fetching bundles, so nothing observable here says the batch is running out. A
    /// second pass runs when the answer says the directory needs more — bounded, because
    /// each pass either satisfies it or fails outright.
    public func publishIfNeeded() async {
        await publishOnce(allowRepublish: true)
    }

    /// One publish, plus at most one follow-up.
    ///
    /// `allowRepublish` is what bounds the recursion. Every reason for a second pass is
    /// resolved by it — fresh keys are minted before it runs, a rotated prekey is in the
    /// bundle it sends — so a third would answer nothing the second did not, and a
    /// directory wedged in a state that keeps asking for more must not turn a connect
    /// into an unbounded publish loop.
    private func publishOnce(allowRepublish: Bool) async {
        do {
            try loadOrCreateIdentity()
            rotateSignedPreKeyIfStale()
            guard let identity, let signing, let signedPreKey else { return }

            guard let signature = Crypto.signPreKey(
                signingPrivateKey: signing.privateKey,
                signedPreKeyPublic: signedPreKey.publicKey
            ) else {
                // Publishing an unsigned bundle would be worse than publishing none:
                // a peer that verifies rejects it, and a peer that does not has no
                // MITM protection at all.
                log.error("could not sign the prekey; refusing to publish an unsigned bundle")
                return
            }

            refillOneTimePreKeys()

            // Only what the directory has not acknowledged, in a stable order so the
            // reply's `accepted` count can be matched back to the keys it refers to.
            let offered = oneTimePreKeys.keys.filter { !publishedPreKeys.contains($0) }.sorted()

            let state = try await client.publishKeys(KeyPublishBody(
                identityKey: B64.encode(identity.publicKey),
                signingKey: B64.encode(signing.publicKey),
                signedPrekey: B64.encode(signedPreKey.publicKey),
                signedPrekeySig: B64.encode(signature),
                prekeys: offered
            ))

            if apply(state: state, offered: offered), allowRepublish {
                await publishOnce(allowRepublish: false)
            }
        } catch {
            // Not retried here: the next connect publishes again, and a failed publish
            // degrades to "cannot start new secret chats" rather than to data loss. An
            // older gateway that does not answer `KEY_PUBLISH` lands here too, on the
            // request timeout, having still published — which is exactly the behaviour
            // this had before the reply existed.
            log.error("publishing device keys failed: \(error)")
        }
    }

    /// Folds the directory's report into local state; true when the bundle must go again.
    ///
    /// `offered` is the prekey list of the publish being answered, in the order it went
    /// out. The survivors of a truncated frame are its LAST `accepted` entries: the
    /// directory appends and trims from the front, so the newest are the ones that stay.
    /// Marking the first ones instead would retire exactly the keys it dropped and
    /// re-offer the ones it kept — the duplicate this tracking exists to prevent, in the
    /// one case where it is certain to happen.
    private func apply(state: KeyStateBody, offered: [String]) -> Bool {
        let accepted = max(0, min(Int(state.accepted), offered.count))
        publishedPreKeys.formUnion(offered.suffix(accepted))

        var republish = false

        if Int(state.oneTimePrekeysLeft) <= Self.preKeyLowWater {
            // Fresh pairs, not the ones already held: the keys the directory is missing
            // are precisely the ones it has already handed out.
            let missing = Self.preKeyBatch - Int(state.oneTimePrekeysLeft)
            for _ in 0..<max(0, missing) {
                let pair = Crypto.generateKeyPair()
                oneTimePreKeys[B64.encode(pair.publicKey)] = pair.privateKey
            }
            republish = true
        }

        trimLocalPreKeys()

        // The directory is serving a signed prekey older than the rotation window while
        // this device may well believe it rotated. Only a republish settles it: a
        // genuinely old local key was already rotated above, and a fresh one simply goes
        // out again. Either way the next report reads 0.
        if state.signedPrekeyAgeMs >= Int64(Self.signedPreKeyMaxAge * 1000) { republish = true }

        // Republishing is never driven by what is still unoffered here. An unoffered
        // remainder means a cap truncated the frame, and republishing on that would send
        // the remainder, have it truncated again, and loop against a full directory.

        persistOneTimePreKeys()
        return republish
    }

    /// This account's own identity, for rendering a safety number.
    public func localIdentity(userID: String) throws -> Safety.Identity? {
        try loadOrCreateIdentity()
        guard let identity, let signing else { return nil }
        return Safety.Identity(
            stableID: userID,
            identityKey: identity.publicKey,
            signingKey: signing.publicKey
        )
    }

    private func loadOrCreateIdentity() throws {
        if identity == nil {
            if let stored = try loadAgreementPair(Key.identity) {
                identity = stored
            } else {
                let fresh = Crypto.generateKeyPair()
                try save(fresh, at: Key.identity)
                identity = fresh
            }
        }
        if signing == nil {
            if let stored: StoredPair = try keychain.value(StoredPair.self, forKey: Key.signing),
               let priv = B64.decode(stored.priv), let pub = B64.decode(stored.pub) {
                signing = SigningKeyPair(privateKey: priv, publicKey: pub)
            } else {
                let fresh = Crypto.generateSigningKeyPair()
                try keychain.set(
                    StoredPair(priv: B64.encode(fresh.privateKey), pub: B64.encode(fresh.publicKey)),
                    forKey: Key.signing
                )
                signing = fresh
            }
        }
        if signedPreKey == nil {
            if let stored = try loadAgreementPair(Key.signedPreKey) {
                signedPreKey = stored
                try loadSignedPreKeyMeta()
            } else {
                let fresh = Crypto.generateKeyPair()
                try save(fresh, at: Key.signedPreKey)
                signedPreKey = fresh
                signedPreKeyCreatedAt = Date()
                persistSignedPreKeyMeta()
            }
        }
        if oneTimePreKeys.isEmpty {
            oneTimePreKeys = (try keychain.value([String: String].self, forKey: Key.oneTimePreKeys) ?? [:])
                .compactMapValues(B64.decode)
            publishedPreKeys = Set(try keychain.value([String].self, forKey: Key.publishedPreKeys) ?? [])
        }
    }

    /// Replaces the signed prekey once it is older than `signedPreKeyMaxAge`.
    ///
    /// The outgoing key becomes the grace copy and the one IT replaced is dropped, which
    /// is the point: a key kept forever is a key that never rotated. Silent when nothing
    /// is due, so it can sit on the publish path.
    private func rotateSignedPreKeyIfStale() {
        guard let current = signedPreKey else { return }
        guard Date().timeIntervalSince(signedPreKeyCreatedAt) >= Self.signedPreKeyMaxAge else {
            return
        }
        let fresh = Crypto.generateKeyPair()
        previousSignedPreKey = current
        signedPreKey = fresh
        signedPreKeyCreatedAt = Date()
        try? save(fresh, at: Key.signedPreKey)
        persistSignedPreKeyMeta()
    }

    /// Reads the rotation record, tolerating its absence.
    ///
    /// An identity stored before rotation existed has no record. Treating that as
    /// "minted now" rather than "minted at the epoch" is deliberate: the alternative
    /// rotates every such device on its next launch, which is a thundering herd of
    /// republishes for a key nobody knows to be old.
    private func loadSignedPreKeyMeta() throws {
        guard let meta = try keychain.value(StoredSignedPreKeyMeta.self, forKey: Key.signedPreKeyMeta)
        else {
            signedPreKeyCreatedAt = Date()
            persistSignedPreKeyMeta()
            return
        }
        signedPreKeyCreatedAt = Date(timeIntervalSince1970: meta.createdAt)
        if let previous = meta.previous,
           let priv = B64.decode(previous.priv), let pub = B64.decode(previous.pub) {
            previousSignedPreKey = SecretKeyPair(privateKey: priv, publicKey: pub)
        } else {
            previousSignedPreKey = nil
        }
    }

    private func persistSignedPreKeyMeta() {
        try? keychain.set(
            StoredSignedPreKeyMeta(
                createdAt: signedPreKeyCreatedAt.timeIntervalSince1970,
                previous: previousSignedPreKey.map {
                    StoredPair(priv: B64.encode($0.privateKey), pub: B64.encode($0.publicKey))
                }
            ),
            forKey: Key.signedPreKeyMeta
        )
    }

    /// Drops the oldest local prekeys once the map is over the ceiling.
    ///
    /// Oldest by the order the directory would have trimmed them: it keeps the newest,
    /// so a key dropped here is one it dropped there, or one that has gone longest
    /// without being claimed. Sorted by key because a dictionary has no order of its own
    /// and an arbitrary choice would discard a key some peer is about to use.
    private func trimLocalPreKeys() {
        guard oneTimePreKeys.count > Self.maxLocalPreKeys else { return }
        let doomed = oneTimePreKeys.keys.sorted().prefix(oneTimePreKeys.count - Self.maxLocalPreKeys)
        for key in doomed {
            oneTimePreKeys.removeValue(forKey: key)
            publishedPreKeys.remove(key)
        }
    }

    /// Tops the batch back up to `preKeyBatch`.
    ///
    /// Additive, never a replacement. A prekey some peer fetched but has not used yet
    /// is still needed to answer their first message, so discarding the old batch would
    /// break exactly the conversations that are about to start.
    private func refillOneTimePreKeys() {
        while oneTimePreKeys.count < Self.preKeyBatch {
            let pair = Crypto.generateKeyPair()
            oneTimePreKeys[B64.encode(pair.publicKey)] = pair.privateKey
        }
        persistOneTimePreKeys()
    }

    /// Forgets a prekey a peer has now consumed.
    ///
    /// Reusing one weakens exactly the forward secrecy a one-time key exists to
    /// provide, so it is dropped the moment a handshake proves it was used.
    private func consumeOneTimePreKey(publicKey: String) {
        guard oneTimePreKeys.removeValue(forKey: publicKey) != nil else { return }
        // The mark goes with it. Left behind, the set grows by every key this device ever
        // offered, and nothing would ever remove an entry whose key is gone.
        publishedPreKeys.remove(publicKey)
        persistOneTimePreKeys()
    }

    private func persistOneTimePreKeys() {
        try? keychain.set(oneTimePreKeys.mapValues(B64.encode), forKey: Key.oneTimePreKeys)
        // Written together with the keys it describes. Split across two writes, a crash
        // between them leaves a key marked as offered that the map no longer holds, or
        // the reverse — an unoffered key published a second time.
        try? keychain.set(publishedPreKeys.sorted(), forKey: Key.publishedPreKeys)
    }

    private struct StoredPair: Codable {
        let priv: String
        let pub: String
    }

    /// The rotation record: when the current signed prekey was minted, and the pair it
    /// replaced. Both optional-tolerant on read, because installs predate this.
    private struct StoredSignedPreKeyMeta: Codable {
        let createdAt: TimeInterval
        let previous: StoredPair?
    }

    private func loadAgreementPair(_ key: String) throws -> SecretKeyPair? {
        guard
            let stored: StoredPair = try keychain.value(StoredPair.self, forKey: key),
            let priv = B64.decode(stored.priv),
            let pub = B64.decode(stored.pub)
        else { return nil }
        return SecretKeyPair(privateKey: priv, publicKey: pub)
    }

    private func save(_ pair: SecretKeyPair, at key: String) throws {
        try keychain.set(
            StoredPair(priv: B64.encode(pair.privateKey), pub: B64.encode(pair.publicKey)),
            forKey: key
        )
    }

    // MARK: - Sending

    /// What a send achieved, per the relay.
    public struct SendOutcome: Sendable, Equatable {
        /// Peer devices the ciphertext reached live.
        public var delivered = 0
        /// Peer devices it was stored for.
        public var queued = 0
        /// Peer devices whose bundle could not be used, or whose relay call failed.
        public var failed = 0

        /// Nothing reached anything — the one case a UI must not draw as "sent".
        public var isTotalFailure: Bool { delivered == 0 && queued == 0 }
    }

    /// Encrypts `text` once per peer device and relays each copy.
    ///
    /// The device list is re-fetched on every send rather than cached. A peer who signs
    /// in on a second phone between two messages would otherwise never receive the
    /// second one there — and would have no way to tell that from a message never sent.
    ///
    /// Sessions for devices that have DISAPPEARED are dropped here too, because keeping
    /// them means fanning out forever to a device the directory no longer lists.
    @discardableResult
    public func send(
        chatID: String,
        peerUserID: String,
        text: String,
        localMessageID: String
    ) async throws -> SendOutcome {
        try loadOrCreateIdentity()
        guard let identity else { throw SecretChatError.noIdentity }

        let bundles = try await client.fetchAllKeys(userID: peerUserID).bundles
        guard !bundles.isEmpty else {
            // No published device. Not "offline": there is nowhere for the message to
            // wait, and the UI should say so rather than spin indefinitely.
            try await store.setMessageState(id: localMessageID, state: .failed, chatID: chatID)
            throw SecretChatError.peerHasNoDevices
        }

        let live = Set(bundles.map(\.deviceID))
        for stale in try await store.secretSessionDevices(peerUserID: peerUserID)
        where !live.contains(stale) {
            try await store.forgetSecretSession(peerUserID: peerUserID, peerDeviceID: stale)
            sessions[sessionKey(peerUserID, stale)] = nil
        }

        // Every device is checked against its pin BEFORE anything is encrypted. Once a
        // shared secret is derived from a substituted key the message is readable by
        // whoever substituted it, so noticing afterwards protects nothing.
        let pins = trustStore()
        for bundle in bundles where bundle.deviceID != deviceID {
            if case .changed = pins.verify(
                userID: peerUserID,
                deviceID: bundle.deviceID,
                identityKey: bundle.identityKey,
                signingKey: bundle.signingKey
            ) {
                try await store.setMessageState(id: localMessageID, state: .failed, chatID: chatID)
                throw SecretChatError.identityChanged(userID: peerUserID, deviceID: bundle.deviceID)
            }
        }

        var outcome = SendOutcome()
        var pinned = false
        let plaintext = Data(text.utf8)

        for bundle in bundles {
            // This account's own other devices belong in this list too — that is how a
            // secret chat appears on a second phone. Only THIS device is skipped,
            // because it already holds the plaintext.
            if bundle.deviceID == deviceID { continue }

            do {
                // Not named `session`: that would shadow the method being called on the
                // same line, which compiles and reads as a mistake.
                let ratchet = try await session(for: bundle, peerUserID: peerUserID)
                let (header, ciphertext) = try ratchet.encrypt(plaintext)

                let key = sessionKey(peerUserID, bundle.deviceID)
                // Both fields or neither: `SecretEnvelope.bootstrap` returns nil unless
                // both are present, so an envelope carrying one of them is a message
                // nobody can open.
                let ephemeral = pendingBootstrap[key]
                let envelope = SecretEnvelope(
                    identityKey: ephemeral == nil ? nil : B64.encode(identity.publicKey),
                    ephemeralKey: ephemeral.map(B64.encode),
                    ratchetHeader: B64.encode(RatchetHeaderCodec.marshal(header))
                )

                // Persisted BEFORE the send. A send that fails has still advanced the
                // ratchet, and writing afterwards would leave the disk copy a message
                // behind the peer on a retry — which breaks the chain rather than
                // resending anything.
                try await store.saveSecretSession(
                    peerUserID: peerUserID,
                    peerDeviceID: bundle.deviceID,
                    state: ratchet.serialize()
                )

                // Pinned once a session exists, not before: pinning a key this device
                // then failed to use would record an identity it never talked to.
                if case .firstUse = pins.verify(
                    userID: peerUserID,
                    deviceID: bundle.deviceID,
                    identityKey: bundle.identityKey,
                    signingKey: bundle.signingKey
                ) {
                    pins.accept(
                        userID: peerUserID,
                        deviceID: bundle.deviceID,
                        identityKey: bundle.identityKey,
                        signingKey: bundle.signingKey
                    )
                    pinned = true
                }

                let ack = try await client.sendSecret(
                    toUserID: peerUserID,
                    toDeviceID: bundle.deviceID,
                    fromDeviceID: deviceID,
                    header: envelope.encoded(),
                    ciphertext: ciphertext
                )

                switch ack {
                case .some(let ack) where ack.delivered:
                    outcome.delivered += 1
                case .some(let ack) where ack.queued:
                    outcome.queued += 1
                case .some:
                    outcome.failed += 1
                case .none:
                    // A gateway that did not negotiate the ack says nothing at all.
                    // Counting that as delivered is a guess; the alternative is drawing
                    // every message as failed against an older server.
                    outcome.delivered += 1
                }
            } catch {
                log.error("secret send to \(bundle.deviceID) failed: \(error)")
                outcome.failed += 1
            }
        }

        if pinned { persistTrust() }
        if outcome.isTotalFailure {
            try await store.setMessageState(id: localMessageID, state: .failed, chatID: chatID)
        }
        return outcome
    }

    // MARK: - Safety numbers and pins

    /// One peer device's safety number and how its key compares with the pin.
    public struct DeviceSafetyInfo: Sendable, Equatable {
        public let userID: String
        public let deviceID: String
        public let number: String
        public let verdict: TrustVerdict
        public let identityKey: String
        public let signingKey: String
    }

    /// Safety numbers for every device the peer has published, fetched fresh so the
    /// screen shows the keys the next message would actually use.
    public func safety(peerUserID: String, ourUserID: String) async throws -> [DeviceSafetyInfo] {
        guard let local = try localIdentity(userID: ourUserID) else { throw SecretChatError.noIdentity }
        let bundles = try await client.fetchAllKeys(userID: peerUserID).bundles
        let pins = trustStore()
        return bundles.compactMap { bundle in
            guard
                bundle.deviceID != deviceID,
                let identityKey = B64.decode(bundle.identityKey),
                let signingKey = B64.decode(bundle.signingKey)
            else { return nil }
            let remote = Safety.Identity(stableID: peerUserID, identityKey: identityKey, signingKey: signingKey)
            return DeviceSafetyInfo(
                userID: peerUserID,
                deviceID: bundle.deviceID,
                number: Safety.number(local: local, remote: remote),
                verdict: pins.verify(
                    userID: peerUserID,
                    deviceID: bundle.deviceID,
                    identityKey: bundle.identityKey,
                    signingKey: bundle.signingKey
                ),
                identityKey: bundle.identityKey,
                signingKey: bundle.signingKey
            )
        }
    }

    /// Pins the keys a person just looked at, replacing a changed pin. Takes the keys
    /// rather than refetching them, so what is pinned is what was displayed.
    ///
    /// Accepting a CHANGED key also drops the session with that device: it was built
    /// on the old identity, so the next message has to start a fresh handshake with the
    /// keys that were just accepted.
    public func acceptIdentity(
        userID: String,
        deviceID peerDeviceID: String,
        identityKey: String,
        signingKey: String
    ) async throws {
        let pins = trustStore()
        if case .changed = pins.verify(
            userID: userID, deviceID: peerDeviceID, identityKey: identityKey, signingKey: signingKey
        ) {
            try await store.forgetSecretSession(peerUserID: userID, peerDeviceID: peerDeviceID)
            sessions[sessionKey(userID, peerDeviceID)] = nil
            pendingBootstrap[sessionKey(userID, peerDeviceID)] = nil
        }
        pins.accept(
            userID: userID,
            deviceID: peerDeviceID,
            identityKey: identityKey,
            signingKey: signingKey
        )
        persistTrust()
    }

    private func trustStore() -> TrustStore {
        if let trust { return trust }
        // An unreadable record starts empty: every device is then pinned again on
        // first use, the same position as a fresh install.
        let stored = try? keychain.value([String: PinnedIdentity].self, forKey: Key.trust)
        let loaded = TrustStore(pins: stored ?? [:])
        trust = loaded
        return loaded
    }

    private func persistTrust() {
        guard let trust else { return }
        do {
            try keychain.set(trust.snapshot(), forKey: Key.trust)
        } catch {
            // Reported, not thrown: the message already went out. A lost pin means the
            // next send pins again on first use, which is weaker but not wrong.
            log.error("could not store identity pins: \(error)")
        }
    }

    /// The session for a peer device: from memory, from disk, or newly handshaked.
    ///
    /// Returns only the session. It used to also report whether it had just been created
    /// and what ephemeral key the first message owed — both now live in
    /// `pendingBootstrap`, because that state outlives one call and a second copy here
    /// was a second thing to keep in step with it.
    private func session(for bundle: KeyBundleBody, peerUserID: String) async throws -> RatchetSession {
        let key = sessionKey(peerUserID, bundle.deviceID)
        if let live = sessions[key] {
            return live
        }
        if let stored = try await store.secretSession(peerUserID: peerUserID, peerDeviceID: bundle.deviceID),
           let restored = RatchetSession.deserialize(stored) {
            sessions[key] = restored
            // Restored from disk, therefore past its first message: a session only
            // reaches disk after a send or a receive, so whatever bootstrap it owed was
            // already attached to that message.
            return restored
        }

        guard let identity else { throw SecretChatError.noIdentity }
        guard
            let identityKey = B64.decode(bundle.identityKey),
            let signingKey = B64.decode(bundle.signingKey),
            let theirSignedPreKey = B64.decode(bundle.signedPrekey),
            let signature = B64.decode(bundle.signedPrekeySig)
        else { throw SecretChatError.malformedBundle }

        // X3DH verifies the prekey signature itself and throws when it does not hold,
        // so a hostile directory cannot get a session started against its own key.
        let (secret, ephemeral) = try X3DH.initiator(
            keys: InitiatorKeys(identity: identity, ephemeral: Crypto.generateKeyPair()),
            bundle: PreKeyBundle(
                identityKey: identityKey,
                signingKey: signingKey,
                signedPreKey: theirSignedPreKey,
                signedPreKeySignature: signature,
                oneTimePreKey: B64.decode(bundle.oneTimePrekey) ?? Data()
            )
        )
        guard let started = RatchetSession.initiator(
            sharedSecret: secret,
            theirSignedPreKey: theirSignedPreKey
        ) else { throw SecretChatError.malformedBundle }

        sessions[key] = started
        // Owed from now until the peer proves, by sending something this device can
        // decrypt, that they have a session.
        pendingBootstrap[key] = ephemeral
        return started
    }

    // MARK: - Receiving

    /// Decrypts one inbound frame, stores the plaintext, and acks the queue row.
    ///
    /// The order is fixed and load-bearing: decrypt, persist the session, persist the
    /// message, note the ack, then tell the server. Any other order loses a message on
    /// a crash — and loses it silently, because the server has dropped its copy and the
    /// sender was told it arrived.
    public func receive(_ body: SecretMsgBody, ourUserID: String) async {
        guard let (headerBytes, ciphertext) = body.payload() else {
            log.error("secret frame from \(body.fromDeviceID) carried no usable payload")
            return
        }
        guard
            let envelope = SecretEnvelope.decode(headerBytes),
            let header = envelope.header
        else {
            log.error("secret frame from \(body.fromDeviceID) had a malformed envelope")
            return
        }

        do {
            try loadOrCreateIdentity()
            let opened = try await inboundSession(
                peerUserID: body.fromUserID,
                peerDeviceID: body.fromDeviceID,
                envelope: envelope,
                header: header,
                ciphertext: ciphertext
            )

            try await store.saveSecretSession(
                peerUserID: body.fromUserID,
                peerDeviceID: body.fromDeviceID,
                state: opened.session.serialize()
            )
            if let consumed = opened.consumedOneTimePreKey {
                consumeOneTimePreKey(publicKey: consumed)
            }
            try await persist(plaintext: opened.plaintext, from: body, ourUserID: ourUserID)

            if !body.queueID.isEmpty {
                try await store.noteSecretAck(queueID: body.queueID)
                await flushAcks()
            }
        } catch {
            // A frame that does not decrypt is dropped and NOT acked, so it stays in
            // the queue and is retried on the next connect. That is the right default:
            // the usual cause is transient, and an ack here would discard the message
            // permanently.
            log.error("secret frame from \(body.fromDeviceID) did not decrypt: \(error)")
        }
    }

    private struct DecryptedFrame {
        let plaintext: Data
        let session: RatchetSession
        let consumedOneTimePreKey: String?
    }

    /// Opens an inbound frame, establishing a responder session when it is a first
    /// message.
    ///
    /// An existing session is tried FIRST and its failure is not fatal: a peer that
    /// reinstalled starts a brand-new session against the same device, so a frame that
    /// the old chain cannot open may still be a legitimate handshake.
    private func inboundSession(
        peerUserID: String,
        peerDeviceID: String,
        envelope: SecretEnvelope,
        header: RatchetHeader,
        ciphertext: Data
    ) async throws -> DecryptedFrame {
        let key = sessionKey(peerUserID, peerDeviceID)

        let existing = try await restoredSession(peerUserID: peerUserID, peerDeviceID: peerDeviceID)

        if let existing,
           let plaintext = try? existing.decrypt(header: header, ciphertext: ciphertext) {
            sessions[key] = existing
            // Something from this device decrypted, so the peer has a working session
            // and the handshake material no longer needs repeating.
            pendingBootstrap[key] = nil
            return DecryptedFrame(plaintext: plaintext, session: existing, consumedOneTimePreKey: nil)
        }

        // Not an established session, so this has to be a first message — which means
        // the envelope must carry the bootstrap. Without it there is nothing to try.
        guard let bootstrap = envelope.bootstrap else { throw SecretChatError.noHandshakeMaterial }
        guard let identity, let signedPreKey else { throw SecretChatError.noIdentity }

        // Every retained one-time prekey is tried, then the signed-prekey-only form, and
        // all of it against BOTH signed prekeys.
        //
        // Trying rather than being told: nothing on the wire names which prekey the
        // initiator consumed, and adding a field would leak handshake detail to the
        // relay. The AEAD is the oracle — only the right key authenticates — and the
        // cost is a handful of X25519 operations, paid once per new peer device.
        //
        // The previous signed prekey is in the list because rotation is not atomic across
        // the network: a peer that fetched the old bundle seconds before the rotation is
        // sending its first message against the key this device has just replaced.
        // Without the grace copy that message is undecryptable and nobody is told — the
        // relay reports delivery, not decryption.
        var candidates: [(oneTime: SecretKeyPair?, publicKey: String?)] =
            oneTimePreKeys.map { (SecretKeyPair(privateKey: $0.value, publicKey: Data()), $0.key) }
        candidates.append((nil, nil))
        // Current first: it is the one all but the rotation window's stragglers used.
        let signedPreKeys = [signedPreKey] + (previousSignedPreKey.map { [$0] } ?? [])

        for spk in signedPreKeys {
            for candidate in candidates {
                guard let secret = try? X3DH.responder(
                    keys: ResponderKeys(
                        identity: identity,
                        signedPreKey: spk,
                        oneTimePreKey: candidate.oneTime
                    ),
                    initiatorIdentity: bootstrap.identity,
                    initiatorEphemeral: bootstrap.ephemeral,
                    usedOneTime: candidate.oneTime != nil
                ) else { continue }

                let session = RatchetSession.responder(sharedSecret: secret, signedPreKey: spk)
                guard let plaintext = try? session.decrypt(header: header, ciphertext: ciphertext)
                else { continue }
                sessions[key] = session
                // This device is the RESPONDER here, so it owes no bootstrap of its own —
                // its ratchet key travels in every header. Clearing is still right: if both
                // sides opened a session at once, the peer has one now.
                pendingBootstrap[key] = nil
                return DecryptedFrame(
                    plaintext: plaintext,
                    session: session,
                    consumedOneTimePreKey: candidate.publicKey
                )
            }
        }

        throw SecretChatError.noMatchingPreKey
    }

    /// Writes a decrypted message into the ORDINARY message cache.
    ///
    /// Ordinary, deliberately. A secret chat is a chat: it needs history, an unread
    /// count and a place in the list, and keeping its messages somewhere else is
    /// exactly what made the previous design unusable. What is never stored is the
    /// ciphertext — nothing keeps a copy of what the relay carried.
    private func persist(plaintext: Data, from body: SecretMsgBody, ourUserID: String) async throws {
        let text = String(decoding: plaintext, as: UTF8.self)
        let chatID = try await secretChatID(with: body.fromUserID, ourUserID: ourUserID)

        // Derived from the queue id when there is one, so a replayed envelope converges
        // on the same row rather than duplicating the message. A live frame has no
        // queue id and no server id either — the relay assigns none — so a UUID is the
        // only identifier available.
        let id = body.queueID.isEmpty
            ? "secret-\(body.fromDeviceID)-\(UUID().uuidString)"
            : "secret-q-\(body.queueID)"

        try await store.upsertMessages([
            Message(
                id: id,
                chatID: chatID,
                senderID: body.fromUserID,
                // Secret messages have no chat sequence: a blind relay assigns none,
                // which is the point of it being blind. Ordering is by arrival, and the
                // seq stays 0 so nothing mistakes one for a cloud message and tries to
                // page history around it.
                seq: 0,
                text: text,
                sentAt: Date(),
                state: .sent,
                dedupKey: id
            ),
        ])
    }

    /// The local chat row a peer's secret messages belong in.
    ///
    /// Looked up by peer rather than created blindly: the server made the chat when one
    /// side called `createSecretChat`, and the next enumeration delivers its real id.
    /// Until then a message from a peer whose chat this device has not seen yet is held
    /// against a deterministic placeholder so it is not lost — the alternative is
    /// dropping the first message of every conversation this device did not start.
    private func secretChatID(with peerUserID: String, ourUserID: String) async throws -> String {
        for archived in [false, true] {
            for summary in try await store.chatSummaries(archived: archived)
            where summary.chat.kind == .secret && summary.chat.peerUserID == peerUserID {
                return summary.chat.id
            }
        }

        // Ordered by user id so both sides derive the same placeholder, which keeps two
        // devices of this account from inventing two different rows for one peer.
        let placeholder = "secret:\(min(ourUserID, peerUserID)):\(max(ourUserID, peerUserID))"
        try await store.upsertChat(Chat(
            id: placeholder,
            kind: .secret,
            title: "",
            peerUserID: peerUserID,
            lastActivityAt: Date()
        ))
        return placeholder
    }

    // MARK: - The offline queue

    /// Drains everything queued for this device, page by page.
    ///
    /// Run on connect. The frames arrive as ordinary `SECRET_RECV` pushes while this is
    /// in flight, so it is a request for a REPLAY rather than a fetch — `SecretSynced`
    /// only reports how many were sent and where to resume, and the receive path needs
    /// no separate branch for history.
    public func syncQueue() async {
        await flushAcks()

        var cursor = ""
        for _ in 0..<Self.maxSyncRounds {
            do {
                let page = try await client.syncSecrets(after: cursor, limit: Self.syncPage)
                // Any of the three means there is nothing more to ask for. The third —
                // an unchanged cursor — is the one that matters: without it a server
                // that keeps saying "not done" loops until the ceiling.
                if page.done || page.nextAfter.isEmpty || page.nextAfter == cursor { return }
                cursor = page.nextAfter
            } catch {
                log.error("secret queue sync failed: \(error)")
                return
            }
        }
        log.error("secret queue sync hit its page ceiling; stopping")
    }

    /// Confirms stored envelopes so the server can drop them.
    ///
    /// Retryable by design: the ids sit in a table until the send succeeds, so an ack
    /// lost to a dropped connection is sent again rather than leaving a row the server
    /// replays on every connect for a fortnight.
    public func flushAcks() async {
        do {
            let pending = try await store.pendingSecretAcks()
            guard !pending.isEmpty else { return }
            try await client.ackSecrets(pending)
            try await store.clearSecretAcks(pending)
        } catch {
            log.error("acking stored secret envelopes failed: \(error)")
        }
    }

    /// The live session for a peer device, from memory or from disk.
    ///
    /// A plain method rather than an inline closure: an immediately-invoked async
    /// throwing closure compiles, but the inference around `try await { … }()` is the
    /// kind of thing that produces an error pointing at the wrong line.
    private func restoredSession(peerUserID: String, peerDeviceID: String) async throws -> RatchetSession? {
        if let live = sessions[sessionKey(peerUserID, peerDeviceID)] { return live }
        guard let stored = try await store.secretSession(
            peerUserID: peerUserID,
            peerDeviceID: peerDeviceID
        ) else { return nil }
        return RatchetSession.deserialize(stored)
    }

    /// Forgets this device's secret-chat identity.
    ///
    /// Part of logout, and not optional. The identity is per ACCOUNT even though the
    /// device id is per install: keeping it would have the next account to sign in
    /// publish the previous one's identity key, so the old account's peers would find
    /// their pin still matching — which is precisely the signal a pin exists to break.
    ///
    /// The ratchet sessions go with the cache, which `wipe()` clears.
    public func forgetIdentity() {
        identity = nil
        signing = nil
        signedPreKey = nil
        oneTimePreKeys = [:]
        sessions = [:]
        pendingBootstrap = [:]
        trust = nil
        for key in Key.all {
            try? keychain.remove(key: key)
        }
    }

    private func sessionKey(_ userID: String, _ deviceID: String) -> String {
        "\(userID)|\(deviceID)"
    }
}

public enum SecretChatError: Error, Equatable, Sendable {
    /// The peer has published no device keys, so there is nowhere to deliver and
    /// nothing to queue against. Distinct from "offline", which resolves on its own.
    case peerHasNoDevices
    /// This device has no identity yet — `publishIfNeeded` has not run, or failed.
    case noIdentity
    /// A bundle from the directory could not be decoded, or did not verify.
    case malformedBundle
    /// A frame that no existing session opens, and whose envelope carries no handshake
    /// either. Nothing can be done with it; it is not retried.
    case noHandshakeMaterial
    /// A first message whose handshake matched none of this device's prekeys. Usually
    /// means the prekey it consumed has already been forgotten.
    case noMatchingPreKey
    /// A peer device's identity key differs from the one pinned for it. Nothing was
    /// sent to any device.
    case identityChanged(userID: String, deviceID: String)
}

/// Logging seam.
///
/// Injectable so a test can assert that a failure was REPORTED rather than swallowed.
/// Several paths here deliberately continue after an error, and "continued quietly" and
/// "continued after saying so" are different bugs.
public struct SecretLog: Sendable {
    private let sink: @Sendable (String) -> Void

    public init(sink: @Sendable @escaping (String) -> Void = { print("[secret] \($0)") }) {
        self.sink = sink
    }

    public func error(_ message: String) { sink(message) }
}
