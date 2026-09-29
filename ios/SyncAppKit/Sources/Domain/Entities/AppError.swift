import Foundation

/// What can go wrong, in the vocabulary of the app rather than the wire.
///
/// The gateway's error codes are grouped by class on purpose (1xxx transport,
/// 2xxx auth, 3xxx business, 4xxx throttling, 5xxx server) so a client can react
/// by range. This enum is that reaction, made explicit — and it is what keeps
/// `ProtocolError` from leaking above the repository boundary, where a view
/// model would otherwise have to import the network module to read a status
/// code.
public enum AppError: Error, Equatable, Sendable {
    /// The credential is dead; the user has to sign in again.
    case unauthorized
    /// Wrong username or password. Deliberately indistinguishable from "no such
    /// account" — the server refuses to tell the difference so the protocol
    /// cannot be used to enumerate usernames.
    case badCredentials
    /// The username is already taken (registration).
    case usernameTaken
    case notFound
    case forbidden
    /// A block in either direction; the chat will not resolve for either side.
    case blocked
    case rateLimited(retryAfterMs: Int)
    case invalidInput(String)
    /// The server build has this feature switched off.
    case unsupported
    /// The feature exists and this account's tier does not include it.
    ///
    /// Distinct from `forbidden` because the two lead somewhere different: forbidden is
    /// final, this one has an answer, and a screen that conflates them shows a dead end
    /// where an upgrade prompt belongs.
    case premiumRequired
    /// Credentials were right and a second factor is needed.
    ///
    /// Not a failure: the login screen adds a six-digit field and keeps what the user
    /// already typed. Reporting it as `badCredentials` would tell them their password
    /// is wrong when it is not.
    case twoFactorRequired
    /// A two-factor or recovery code did not verify.
    case twoFactorInvalid
    /// Nothing reached the server.
    case offline
    /// An upload or download failed after the ticket was issued.
    case mediaFailed(String)
    /// The file is larger than the server will accept.
    case mediaTooLarge(limit: Int64)
    /// A peer device's identity key no longer matches the pinned one. Nothing was
    /// encrypted; the safety screen is where the user decides whether to trust it.
    case identityChanged(userID: String, deviceID: String)
    case server(String)

    public var isRetryable: Bool {
        switch self {
        case .rateLimited, .offline, .server:
            return true
        default:
            return false
        }
    }
}

extension AppError: LocalizedError {
    public var errorDescription: String? {
        switch self {
        case .unauthorized: return "unauthorized"
        case .badCredentials: return "bad credentials"
        case .usernameTaken: return "username taken"
        case .notFound: return "not found"
        case .forbidden: return "forbidden"
        case .blocked: return "blocked"
        case .rateLimited: return "rate limited"
        case .invalidInput(let detail): return detail
        case .unsupported: return "unsupported"
        case .premiumRequired: return "premium required"
        case .twoFactorRequired: return "two-factor code required"
        case .twoFactorInvalid: return "invalid two-factor code"
        case .offline: return "offline"
        case .mediaFailed(let detail): return detail
        case .mediaTooLarge: return "file too large"
        case .identityChanged: return "peer identity key changed"
        case .server(let detail): return detail
        }
    }
}
