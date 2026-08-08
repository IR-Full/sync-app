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
    /// (`SYNAPSE_TLS_SELFSIGNED=1`). Always false in Release.
    public let allowsInsecureTLS: Bool

    public init(
        name: Name,
        gatewayURL: URL,
        tcpHost: String,
        tcpPort: UInt16,
        transport: TransportKind,
        mediaBaseURL: URL?,
        allowsInsecureTLS: Bool
    ) {
        self.name = name
        self.gatewayURL = gatewayURL
        self.tcpHost = tcpHost
        self.tcpPort = tcpPort
        self.transport = transport
        self.mediaBaseURL = mediaBaseURL
        self.allowsInsecureTLS = allowsInsecureTLS
    }

    /// Builds a transport for this environment.
    public func makeTransport() -> Transport {
        switch transport {
        case .webSocket:
            return WebSocketTransport(url: gatewayURL)
        case .tcp:
            let secure = gatewayURL.scheme == "wss"
            return TCPTransport(
                host: tcpHost,
                port: tcpPort,
                useTLS: secure,
                allowSelfSignedCertificates: allowsInsecureTLS
            )
        }
    }
}

extension ServerEnvironment {
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

        let name = Name(rawValue: string("SynapseEnvironment") ?? "") ?? .dev
        let gateway = string("SynapseGatewayURL").flatMap(URL.init(string:))
            ?? URL(string: "ws://localhost:8080/ws")!
        let transport = TransportKind(rawValue: string("SynapseTransport") ?? "") ?? .webSocket
        let media = string("SynapseMediaBaseURL").flatMap(URL.init(string:))
        let insecure = (string("SynapseAllowsInsecureTLS") ?? "NO").uppercased() == "YES"

        return ServerEnvironment(
            name: name,
            gatewayURL: gateway,
            tcpHost: string("SynapseTCPHost") ?? "localhost",
            tcpPort: UInt16(string("SynapseTCPPort") ?? "7000") ?? 7000,
            transport: transport,
            mediaBaseURL: media,
            allowsInsecureTLS: insecure && name != .prod
        )
    }
}

extension String {
    var nilIfEmpty: String? { isEmpty ? nil : self }
}
