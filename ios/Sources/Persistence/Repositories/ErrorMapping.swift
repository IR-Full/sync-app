import Foundation
import SynapseDomain
import SynapseNetwork

/// Translates wire errors into domain errors at the repository boundary.
///
/// This is the only place that knows both, which is the point: without it every
/// view model would need `import SynapseNetwork` just to check a status code,
/// and the layering would be a comment rather than a compiler rule.
enum ErrorMapping {

    /// Runs `work`, converting anything it throws into an `AppError`.
    static func mapped<T>(_ work: () async throws -> T) async throws -> T {
        do {
            return try await work()
        } catch {
            throw map(error)
        }
    }

    static func map(_ error: Error) -> AppError {
        if let error = error as? AppError { return error }

        if let error = error as? ProtocolError {
            switch error.code {
            case .unauthenticated, .badToken, .sessionRevoked, .deviceUnknown:
                return .unauthorized
            case .forbidden:
                // The gateway answers a blocked chat with FORBIDDEN and the word
                // "blocked" — there is no distinct code for it, so the message is
                // the only signal available.
                return error.message.localizedCaseInsensitiveContains("block") ? .blocked : .forbidden
            case .notFound:
                return .notFound
            case .conflict:
                return .usernameTaken
            case .badArgument:
                return .invalidInput(error.message)
            case .rateLimited, .flood:
                return .rateLimited(retryAfterMs: error.retryAfterMs)
            case .unsupported:
                return .unsupported
            case .resumeExpired, .protocolViolation, .badFrame, .payloadTooBig:
                return .server(error.message)
            case .internalError, .unavailable, .unknown, .none:
                return .server(error.message)
            }
        }

        if let error = error as? ClientError {
            switch error {
            case .notConnected, .closed, .requestTimedOut:
                return .offline
            case .noCredentials:
                return .unauthorized
            case .handshakeFailed(let detail):
                return .server(detail)
            case .unexpectedReply:
                return .server("unexpected reply")
            }
        }

        if error is TransportError || error is WireError { return .offline }
        if (error as NSError).domain == NSURLErrorDomain { return .offline }
        return .server(String(describing: error))
    }
}

/// Auth is the one place where the same wire code means two different things
/// depending on intent, so it gets its own mapping.
extension ErrorMapping {
    static func mapAuth(_ error: Error, registering: Bool) -> AppError {
        let mapped = map(error)
        // Registering against a taken username fails authentication rather than
        // conflicting — `Register` refuses when the account exists, and the
        // gateway reports that as the same `unauthenticated` a wrong password
        // gets. Intent is what disambiguates it.
        if case .unauthorized = mapped, registering { return .usernameTaken }
        if case .unauthorized = mapped, !registering { return .badCredentials }
        return mapped
    }
}
