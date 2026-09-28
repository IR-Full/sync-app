import CryptoKit
import Foundation
import Network
import Security

/// SPKI certificate pinning for both transports.
///
/// Until this existed, iOS trusted any certificate the system trusted, which means a
/// device with an installed enterprise or MDM root — or a user who tapped through a
/// profile install — hands the whole session to whoever owns that root. The protocol's
/// own E2E covers secret chats; everything else (the bearer token, group messages,
/// media URLs) is protected by TLS alone, so this is the layer that decides who may
/// terminate it.
///
/// Pins are SHA-256 over the certificate's `SubjectPublicKeyInfo`, base64 — the same
/// values Android's `CertificatePinner` takes and the same `sha256/…` strings OpenSSL
/// prints, so the two platforms pin from one list.
public struct PinningPolicy: Sendable, Equatable {
    /// The host the pins apply to. A pin set is per-host by construction: pinning
    /// the media CDN's key against the gateway's list would fail every download.
    public let host: String
    public let pins: Set<String>
    /// When the pins stop being enforced.
    ///
    /// Pinning without an expiry is how an app bricks itself: a key rotated on the
    /// server side while an old build is still installed cannot connect at all, and
    /// the fix ships through review. Past this date the policy falls back to ordinary
    /// CA validation, which is strictly better than a dead app — the build is expected
    /// to have been replaced long before.
    public let expiresAt: Date?

    public init(host: String, pins: Set<String>, expiresAt: Date?) {
        self.host = host
        self.pins = pins
        self.expiresAt = expiresAt
    }

    /// The minimum number of pins worth enforcing.
    ///
    /// Two, not one. One pin means the certificate's key is a single point of failure
    /// with no way to rotate: the backup pin is what makes a planned rotation possible,
    /// so a single-pin configuration is a misconfiguration rather than a strict one.
    public static let minimumPins = 2

    /// Builds a policy, or `nil` when pinning should not be enforced.
    ///
    /// Every rejection here deliberately degrades to CA validation rather than
    /// refusing to connect. A pinning layer that fails closed on its own
    /// configuration turns a typo in an `.xcconfig` into an app that cannot reach the
    /// server at all, and the failure looks identical to an outage.
    public static func resolve(
        host: String?,
        pins: [String],
        expiresAt: Date?,
        now: Date = Date()
    ) -> PinningPolicy? {
        guard let host, !host.isEmpty else { return nil }
        let cleaned = Set(
            pins
                .map { $0.trimmingCharacters(in: .whitespaces) }
                .filter { !$0.isEmpty }
        )
        guard cleaned.count >= minimumPins else { return nil }
        if let expiresAt, now >= expiresAt { return nil }
        return PinningPolicy(host: host, pins: cleaned, expiresAt: expiresAt)
    }

    /// Whether this policy governs a given host. Exact match only — no wildcard —
    /// because a wildcard pin is a promise about every subdomain a future
    /// infrastructure change might add.
    public func governs(_ candidate: String) -> Bool {
        candidate.caseInsensitiveCompare(host) == .orderedSame
    }

    /// True when any certificate in the chain carries a pinned key.
    ///
    /// ANY, not the leaf: pinning an intermediate is what lets the leaf rotate every
    /// ninety days without shipping a build, and it is the configuration most
    /// deployments actually want.
    public func accepts(chain: [SecCertificate]) -> Bool {
        for certificate in chain {
            if let digest = SPKI.sha256(of: certificate), pins.contains(digest) {
                return true
            }
        }
        return false
    }
}

/// Hashes a certificate's public key the way pin lists express it.
enum SPKI {
    /// SHA-256 of the DER `SubjectPublicKeyInfo`, base64.
    ///
    /// Security.framework hands back the BARE key bits, not the SPKI structure the
    /// pin is taken over, so the algorithm identifier has to be prepended by hand.
    /// Those prefixes are fixed ASN.1 for each key type and size — this is the same
    /// table every pinning library on Apple platforms carries, for the same reason.
    static func sha256(of certificate: SecCertificate) -> String? {
        guard
            let key = SecCertificateCopyKey(certificate),
            let bits = SecKeyCopyExternalRepresentation(key, nil) as Data?,
            let attributes = SecKeyCopyAttributes(key) as? [CFString: Any],
            let header = asn1Header(for: attributes)
        else { return nil }

        var spki = Data(header)
        spki.append(bits)
        return Data(SHA256.hash(data: spki)).base64EncodedString()
    }

    private static func asn1Header(for attributes: [CFString: Any]) -> [UInt8]? {
        let type = attributes[kSecAttrKeyType] as? String
        let size = (attributes[kSecAttrKeySizeInBits] as? NSNumber)?.intValue ?? 0

        if type == (kSecAttrKeyTypeRSA as String) {
            switch size {
            case 2048: return rsa2048
            case 4096: return rsa4096
            default: return nil
            }
        }
        if type == (kSecAttrKeyTypeECSECPrimeRandom as String) {
            switch size {
            case 256: return ecP256
            case 384: return ecP384
            default: return nil
            }
        }
        return nil
    }

    // SEQUENCE { SEQUENCE { OID rsaEncryption, NULL }, BIT STRING …
    private static let rsa2048: [UInt8] = [
        0x30, 0x82, 0x01, 0x22, 0x30, 0x0d, 0x06, 0x09, 0x2a, 0x86, 0x48, 0x86,
        0xf7, 0x0d, 0x01, 0x01, 0x01, 0x05, 0x00, 0x03, 0x82, 0x01, 0x0f, 0x00,
    ]
    private static let rsa4096: [UInt8] = [
        0x30, 0x82, 0x02, 0x22, 0x30, 0x0d, 0x06, 0x09, 0x2a, 0x86, 0x48, 0x86,
        0xf7, 0x0d, 0x01, 0x01, 0x01, 0x05, 0x00, 0x03, 0x82, 0x02, 0x0f, 0x00,
    ]
    // SEQUENCE { SEQUENCE { OID ecPublicKey, OID prime256v1 }, BIT STRING …
    private static let ecP256: [UInt8] = [
        0x30, 0x59, 0x30, 0x13, 0x06, 0x07, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x02,
        0x01, 0x06, 0x08, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x03, 0x01, 0x07, 0x03,
        0x42, 0x00,
    ]
    private static let ecP384: [UInt8] = [
        0x30, 0x76, 0x30, 0x10, 0x06, 0x07, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x02,
        0x01, 0x06, 0x05, 0x2b, 0x81, 0x04, 0x00, 0x22, 0x03, 0x62, 0x00,
    ]
}

/// Evaluates a server trust against a policy.
///
/// Split out from the delegate so the decision is testable without a live
/// `URLSession`: the delegate turns a callback into a `Verdict`, and nothing else.
public enum TrustEvaluator {
    public enum Verdict: Equatable, Sendable {
        /// Use the certificate the server presented.
        case accept
        /// Hand the challenge back to the system — CA validation, no pinning.
        case systemDefault
        case reject(String)
    }

    /// Whether a pin check applies to this host at all.
    ///
    /// Split from `evaluate` so it can be asserted without a live handshake: every
    /// reason pinning does not run is a silent one — the app keeps connecting and
    /// nothing reports that the pins are being ignored — so this is the decision most
    /// worth having tests on, and it is the one that needs no `SecTrust`.
    public static func appliesToHost(_ host: String, policy: PinningPolicy?) -> Bool {
        guard let policy else { return false }
        return policy.governs(host)
    }

    /// The order matters: CA validation runs FIRST, then the pin check.
    ///
    /// A pin match on an otherwise invalid chain — expired, wrong host, revoked —
    /// must not pass. Pinning narrows who is acceptable; it does not replace the
    /// checks that decide what a valid certificate is in the first place.
    public static func evaluate(
        trust: SecTrust,
        host: String,
        policy: PinningPolicy?
    ) -> Verdict {
        guard appliesToHost(host, policy: policy), let policy else { return .systemDefault }

        var error: CFError?
        guard SecTrustEvaluateWithError(trust, &error) else {
            return .reject("certificate chain is not trusted: \(error.map(String.init(describing:)) ?? "unknown")")
        }

        guard let chain = SecTrustCopyCertificateChain(trust) as? [SecCertificate], !chain.isEmpty else {
            return .reject("no certificate chain to pin against")
        }

        return policy.accepts(chain: chain)
            ? .accept
            : .reject("no certificate in the chain for \(host) matches a pinned key")
    }
}

/// `URLSession`'s side of it.
///
/// `@unchecked Sendable` because the stored policy is immutable value state and the
/// delegate itself holds nothing else; `URLSession` calls this from its own queue.
public final class PinningDelegate: NSObject, URLSessionDelegate, @unchecked Sendable {
    private let policy: PinningPolicy?
    /// Dev-only escape hatch for the server's self-signed mode, carried here rather
    /// than duplicated at every call site. Never true in a Release configuration —
    /// `ServerEnvironment.current` forces it off for `.prod`.
    private let allowsInsecureTLS: Bool

    public init(policy: PinningPolicy?, allowsInsecureTLS: Bool = false) {
        self.policy = policy
        self.allowsInsecureTLS = allowsInsecureTLS
    }

    public func urlSession(
        _ session: URLSession,
        didReceive challenge: URLAuthenticationChallenge,
        completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void
    ) {
        guard
            challenge.protectionSpace.authenticationMethod == NSURLAuthenticationMethodServerTrust,
            let trust = challenge.protectionSpace.serverTrust
        else {
            completionHandler(.performDefaultHandling, nil)
            return
        }

        if allowsInsecureTLS {
            completionHandler(.useCredential, URLCredential(trust: trust))
            return
        }

        switch TrustEvaluator.evaluate(
            trust: trust,
            host: challenge.protectionSpace.host,
            policy: policy
        ) {
        case .accept:
            completionHandler(.useCredential, URLCredential(trust: trust))
        case .systemDefault:
            completionHandler(.performDefaultHandling, nil)
        case .reject:
            // Cancelling rather than falling back. A rejected pin is the one case
            // where refusing to connect is the correct outcome: something is
            // terminating the TLS that should not be.
            completionHandler(.cancelAuthenticationChallenge, nil)
        }
    }
}

extension PinningPolicy {
    /// The `sec_protocol_verify_t` form, for `Network.framework`'s TLS on the TCP
    /// transport. Same decision, different plumbing — `NWConnection` does not go
    /// through `URLSessionDelegate`.
    func verifyBlock(host: String) -> sec_protocol_verify_t {
        { _, secTrust, complete in
            let trust = sec_trust_copy_ref(secTrust).takeRetainedValue()
            switch TrustEvaluator.evaluate(trust: trust, host: host, policy: self) {
            case .accept:
                complete(true)
            case .systemDefault:
                // Not our host: let the default evaluation stand. `complete(true)`
                // here would mean this block ACCEPTS anything it does not pin,
                // which is the opposite of the intent — so evaluate explicitly.
                var error: CFError?
                complete(SecTrustEvaluateWithError(trust, &error))
            case .reject:
                complete(false)
            }
        }
    }
}
