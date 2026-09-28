import Foundation

/// Where the gateway lives, and how to reach it.
///
/// Nothing here is hardcoded at a call site: the values come from the build's
/// `.xcconfig` via `Info.plist` (see `Config/*.xcconfig` and
/// `ServerEnvironment.current`), so dev / stage / prod is a scheme choice rather
/// than a code change.
public struct ServerEnvironment: Sendable, Equatable {
    public enum Name: String, Sendable, Codable, CaseIterable {
        case dev, stage, prod
    }

    public let name: Name
    /// WebSocket endpoint, e.g. `ws://localhost:8080/ws` or `wss://…/ws`.
    public let gatewayURL: URL
    /// Host/port of the raw binary listener, used when `transport == .tcp`.
    public let tcpHost: String
    public let tcpPort: UInt16
    public let transport: TransportKind
    /// Base URL of the media HTTP pipeline (`/media/upload/…`, `/media/download/…`).
    /// The gateway mints absolute signed URLs, so this is only a fallback for
    /// builds pointed at a rewritten host.
    public let mediaBaseURL: URL?
    /// Dev-only: accept the server's ephemeral self-signed cert
    /// (`SYNCAPP_TLS_SELFSIGNED=1`). Always false in Release.
    public let allowsInsecureTLS: Bool
    /// Base64 SHA-256 SPKI pins for `gatewayURL`'s host, comma-separated in the
    /// `.xcconfig`. Fewer than two, or none, means ordinary CA validation — see
    /// `PinningPolicy.resolve`.
    public let tlsPins: [String]
    /// When the pins stop being enforced. A build that outlives this date keeps
    /// working against a rotated key instead of becoming unusable.
    public let tlsPinsExpireAt: Date?

    public init(
        name: Name,
        gatewayURL: URL,
        tcpHost: String,
        tcpPort: UInt16,
        transport: TransportKind,
        mediaBaseURL: URL?,
        allowsInsecureTLS: Bool,
        tlsPins: [String] = [],
        tlsPinsExpireAt: Date? = nil
    ) {
        self.name = name
        self.gatewayURL = gatewayURL
        self.tcpHost = tcpHost
        self.tcpPort = tcpPort
        self.transport = transport
        self.mediaBaseURL = mediaBaseURL
        self.allowsInsecureTLS = allowsInsecureTLS
        self.tlsPins = tlsPins
        self.tlsPinsExpireAt = tlsPinsExpireAt
    }

    /// The resolved policy, or `nil` when nothing should be pinned.
    ///
    /// Keyed on the host actually connected to, which differs between transports:
    /// the WebSocket path uses `gatewayURL`'s host while the TCP path dials
    /// `tcpHost` directly, and pinning the wrong one of those enforces nothing.
    public func pinningPolicy(for host: String?, now: Date = Date()) -> PinningPolicy? {
        PinningPolicy.resolve(host: host, pins: tlsPins, expiresAt: tlsPinsExpireAt, now: now)
    }

    /// Builds a transport for this environment.
    public func makeTransport() -> Transport {
        switch transport {
        case .webSocket:
            return WebSocketTransport(
                url: gatewayURL,
                pinning: pinningPolicy(for: gatewayURL.host),
                allowsInsecureTLS: allowsInsecureTLS
            )
        case .tcp:
            let secure = gatewayURL.scheme == "wss"
            return TCPTransport(
                host: tcpHost,
                port: tcpPort,
                useTLS: secure,
                allowSelfSignedCertificates: allowsInsecureTLS,
                pinning: pinningPolicy(for: tcpHost)
            )
        }
    }
}

extension ServerEnvironment {
    /// The fallback used when `Info.plist` carries no usable gateway URL.
    ///
    /// Built with `URL(string:)!`-free initialisation on purpose. The literal is
    /// a compile-time constant that cannot fail to parse, so the force unwrap
    /// was safe — but "safe today" is how force unwraps survive into code where
    /// the literal is later made configurable, and a crash on launch is the
    /// worst possible way to report a malformed development URL.
    static let localDevGateway: URL = {
        guard let url = URL(string: "ws://localhost:8080/ws") else {
            // Unreachable: the string above is a literal. `URL(string:)` returning
            // nil here would mean Foundation itself changed what it accepts.
            preconditionFailure("the local development gateway URL literal is malformed")
        }
        return url
    }()

    /// Reads the active environment out of the app bundle's `Info.plist`, which
    /// the `.xcconfig` for the selected configuration populated.
    ///
    /// A missing or malformed key is a build-configuration bug, not a runtime
    /// condition to paper over — but a crash on launch is a poor way to say so,
    /// so we fall back to the local dev server and leave a loud breadcrumb.
    public static func current(bundle: Bundle = .main) -> ServerEnvironment {
        func string(_ key: String) -> String? {
            (bundle.object(forInfoDictionaryKey: key) as? String)?
                .trimmingCharacters(in: .whitespaces)
                .nilIfEmpty
        }

        let name = Name(rawValue: string("SyncAppEnvironment") ?? "") ?? .dev
        let gateway = string("SyncAppGatewayURL").flatMap(URL.init(string:))
            ?? Self.localDevGateway
        let transport = TransportKind(rawValue: string("SyncAppTransport") ?? "") ?? .webSocket
        let media = string("SyncAppMediaBaseURL").flatMap(URL.init(string:))
        let insecure = (string("SyncAppAllowsInsecureTLS") ?? "NO").uppercased() == "YES"

        // Comma-separated so one `.xcconfig` line carries the primary and the backup.
        let pins = (string("SyncAppTLSPins") ?? "")
            .split(separator: ",")
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }
        // Unix seconds rather than a date format: an `.xcconfig` value goes through
        // several layers of string handling before it lands here, and every date
        // format that survives one of them is ambiguous in another.
        let expiry = string("SyncAppTLSPinsExpireAt")
            .flatMap(Double.init)
            .map(Date.init(timeIntervalSince1970:))

        return ServerEnvironment(
            name: name,
            gatewayURL: gateway,
            tcpHost: string("SyncAppTCPHost") ?? "localhost",
            tcpPort: UInt16(string("SyncAppTCPPort") ?? "7000") ?? 7000,
            transport: transport,
            mediaBaseURL: media,
            allowsInsecureTLS: insecure && name != .prod,
            tlsPins: pins,
            tlsPinsExpireAt: expiry
        )
    }
}

public extension String {
    var nilIfEmpty: String? {
        isEmpty ? nil : self
    }
}