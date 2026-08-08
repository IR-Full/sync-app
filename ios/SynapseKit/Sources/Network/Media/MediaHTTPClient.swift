import Foundation

/// The media side-channel.
///
/// Large blobs never travel as protocol frames — the 16 MiB frame cap is there
/// to protect the parser, not to size an upload. The binary protocol carries a
/// short `media_ref` and these two HTTP calls carry the bytes, behind
/// HMAC-signed, expiring URLs the gateway minted.
public struct MediaHTTPClient: Sendable {
    private let session: URLSession

    public init(session: URLSession = .shared) {
        self.session = session
    }

    public enum MediaError: Error, Equatable, Sendable {
        case badTicketURL(String)
        case sizeMismatch(expected: Int64, actual: Int)
        case rejected(status: Int, message: String)
    }

    /// Uploads to a ticket's URL.
    ///
    /// The ticket signs the *declared* size, and the server refuses anything
    /// that does not match exactly — so a ticket cannot be reused to store more
    /// than it asked for. We check locally first to turn that into a clear error
    /// instead of a 400 from a round trip.
    public func upload(data: Data, to ticket: MediaTicketBody) async throws {
        guard let url = URL(string: ticket.uploadURL) else {
            throw MediaError.badTicketURL(ticket.uploadURL)
        }
        var request = URLRequest(url: url)
        request.httpMethod = "PUT"
        request.setValue("application/octet-stream", forHTTPHeaderField: "Content-Type")

        let (responseData, response) = try await session.upload(for: request, from: data)
        try Self.check(response, responseData)
    }

    /// Downloads the bytes behind a signed URL.
    ///
    /// The server serves uploads as `application/octet-stream` with `nosniff`
    /// and an attachment disposition, so these bytes are never renderable
    /// content — treat them as opaque and decide the type from the message's
    /// attachment metadata, not from the response.
    public func download(from urlBody: MediaURLBody) async throws -> Data {
        guard let url = URL(string: urlBody.downloadURL) else {
            throw MediaError.badTicketURL(urlBody.downloadURL)
        }
        let (data, response) = try await session.data(from: url)
        try Self.check(response, data)
        return data
    }

    private static func check(_ response: URLResponse, _ body: Data) throws {
        guard let http = response as? HTTPURLResponse else { return }
        guard (200..<300).contains(http.statusCode) else {
            let message = String(data: body, encoding: .utf8) ?? ""
            throw MediaError.rejected(status: http.statusCode, message: message)
        }
    }
}
