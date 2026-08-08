import Foundation
import SynapseDomain
import SynapseNetwork

/// The media side-channel, joined to a local file cache.
///
/// Two round trips per direction, and neither carries bytes over the protocol:
///
/// - **Upload** — `MEDIA_INIT` mints a ticket (a signed URL plus the `media_ref`
///   to attach to a message), then the bytes go over HTTP `PUT`. The declared
///   size is part of what the ticket signs and the server holds the body to it
///   exactly, so the size we send at init has to be the size we upload.
/// - **Download** — `MEDIA_FETCH` mints a signed, expiring URL; we `GET` it once
///   and keep the file, because the URL expires but the bytes do not.
public final class MediaRepositoryImpl: MediaRepository, @unchecked Sendable {
    private let client: SynapseClient
    private let http: MediaHTTPClient
    private let cacheDirectory: URL

    public init(client: SynapseClient, http: MediaHTTPClient = MediaHTTPClient()) {
        self.client = client
        self.http = http
        // Caches, not Documents: these are re-downloadable from a `media_ref`,
        // so they must not be backed up and the OS is welcome to evict them
        // under storage pressure.
        let base = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("media", isDirectory: true)
        try? FileManager.default.createDirectory(at: base, withIntermediateDirectories: true)
        self.cacheDirectory = base
    }

    public func upload(
        data: Data, filename: String, mime: String, kind: Attachment.Kind, extra: MediaMetadata
    ) async throws -> Attachment {
        let size = Int64(data.count)
        guard size > 0 else { throw AppError.invalidInput("empty file") }
        guard size <= ServerLimits.maxMediaBytes else {
            throw AppError.mediaTooLarge(limit: ServerLimits.maxMediaBytes)
        }

        let ticket = try await ErrorMapping.mapped {
            try await client.initUpload(filename: filename, contentType: mime, size: size)
        }

        do {
            try await http.upload(data: data, to: ticket)
        } catch {
            throw AppError.mediaFailed(String(describing: error))
        }

        // Cache the bytes we already hold under the ref the server gave them, so
        // our own outgoing photo renders from disk instead of being downloaded
        // back from the server we just sent it to.
        try? data.write(to: fileURL(forRef: ticket.mediaRef), options: .atomic)

        return Attachment(
            kind: kind,
            mediaRef: ticket.mediaRef,
            filename: filename,
            mime: mime,
            size: size,
            durationMs: extra.durationMs,
            waveform: extra.waveform,
            width: extra.width,
            height: extra.height
        )
    }

    public func fileURL(for attachment: Attachment) async throws -> URL {
        let destination = fileURL(forRef: attachment.mediaRef)
        if FileManager.default.fileExists(atPath: destination.path) { return destination }

        let signed = try await ErrorMapping.mapped {
            try await client.downloadURL(mediaRef: attachment.mediaRef)
        }
        let data: Data
        do {
            data = try await http.download(from: signed)
        } catch {
            throw AppError.mediaFailed(String(describing: error))
        }
        try data.write(to: destination, options: .atomic)
        return destination
    }

    public func cachedURL(for attachment: Attachment) async -> URL? {
        let url = fileURL(forRef: attachment.mediaRef)
        return FileManager.default.fileExists(atPath: url.path) ? url : nil
    }

    /// The ref is server-generated and unguessable (`m<snowflake>-<128 bits>`),
    /// but it still goes through a path-component sanitiser: a filename is not
    /// the place to trust a remote string.
    private func fileURL(forRef ref: String) -> URL {
        let safe = ref.replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "..", with: "_")
        return cacheDirectory.appendingPathComponent(safe)
    }
}
