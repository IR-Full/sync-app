import Foundation
import SyncAppCrypto

/// Persistence for secret chats: one ratchet session per peer device, and the queue
/// acknowledgements that have not been confirmed yet.
///
/// Separate from `LocalStore` proper because the failure modes are different in kind.
/// Losing a cached message costs a refetch; losing a ratchet session costs the
/// conversation — the chain keys are the only thing that can open what is already on
/// the wire, and there is no server-side copy to restore from.
extension LocalStore {

    // MARK: - Ratchet sessions

    /// The session for one peer DEVICE, or nil when there is none yet.
    ///
    /// Per device, not per peer. The relay addresses devices, and a peer with a phone
    /// and a tablet is two independent ratchets; sharing one between them means the
    /// second device cannot decrypt anything and the first breaks as soon as the
    /// second replies.
    public func secretSession(peerUserID: String, peerDeviceID: String) async throws -> SerializedSession? {
        let rows = try await database.query(
            "SELECT state FROM secret_sessions WHERE peer_user_id = ? AND peer_device_id = ?",
            [.text(peerUserID), .text(peerDeviceID)]
        )
        guard let json = rows.first?.string("state"), let data = json.data(using: .utf8) else {
            return nil
        }
        return try? JSONDecoder().decode(SerializedSession.self, from: data)
    }

    /// Stores a session after a send or a receive.
    ///
    /// This has to happen on EVERY message, not periodically: the ratchet advances per
    /// message, so a session written one message behind cannot decrypt the next one.
    /// A skipped write is not a stale cache, it is a broken chain.
    public func saveSecretSession(
        peerUserID: String,
        peerDeviceID: String,
        state: SerializedSession
    ) async throws {
        let data = try JSONEncoder().encode(state)
        guard let json = String(data: data, encoding: .utf8) else { return }
        try await database.run(
            """
            INSERT INTO secret_sessions (peer_user_id, peer_device_id, state, updated_at)
            VALUES (?, ?, ?, ?)
            ON CONFLICT(peer_user_id, peer_device_id) DO UPDATE SET
                state      = excluded.state,
                updated_at = excluded.updated_at
            """,
            [
                .text(peerUserID), .text(peerDeviceID), .text(json),
                .millis(Date()),
            ]
        )
    }

    /// Every peer device this account currently holds a session with.
    ///
    /// Used to notice a peer device that has DISAPPEARED. A session whose device is no
    /// longer in the key directory will never receive anything again, and keeping it
    /// means every send fans out to a socket that does not exist.
    public func secretSessionDevices(peerUserID: String) async throws -> [String] {
        let rows = try await database.query(
            "SELECT peer_device_id FROM secret_sessions WHERE peer_user_id = ?",
            [.text(peerUserID)]
        )
        return rows.compactMap { $0.string("peer_device_id") }
    }

    /// Drops a session. Called when the peer device is gone from the directory, or
    /// when the user explicitly resets a conversation after a safety-number change.
    public func forgetSecretSession(peerUserID: String, peerDeviceID: String) async throws {
        try await database.run(
            "DELETE FROM secret_sessions WHERE peer_user_id = ? AND peer_device_id = ?",
            [.text(peerUserID), .text(peerDeviceID)]
        )
    }

    // MARK: - Queue acknowledgements

    /// Records that a queued envelope has been stored locally and may be dropped
    /// server-side.
    ///
    /// Written in the same breath as the plaintext, and BEFORE the ack goes out. The
    /// ordering is the whole point: acking first and storing second loses the message
    /// if the app dies in between, and loses it in the worst possible way — the server
    /// has forgotten it and the sender was told it arrived.
    public func noteSecretAck(queueID: String) async throws {
        guard !queueID.isEmpty else { return }
        try await database.run(
            "INSERT OR IGNORE INTO secret_acks (queue_id, noted_at) VALUES (?, ?)",
            [.text(queueID), .millis(Date())]
        )
    }

    /// Queue ids waiting to be confirmed to the server.
    public func pendingSecretAcks(limit: Int = 200) async throws -> [String] {
        let rows = try await database.query(
            "SELECT queue_id FROM secret_acks ORDER BY noted_at LIMIT ?",
            [.int(limit)]
        )
        return rows.compactMap { $0.string("queue_id") }
    }

    /// Clears ids the server has confirmed.
    ///
    /// Only after the send succeeds. Clearing optimistically would leave a row the
    /// server still holds, which it replays on every connect — forever, because
    /// nothing remains locally to say it was already handled.
    public func clearSecretAcks(_ ids: [String]) async throws {
        guard !ids.isEmpty else { return }
        let placeholders = Array(repeating: "?", count: ids.count).joined(separator: ", ")
        try await database.run(
            "DELETE FROM secret_acks WHERE queue_id IN (\(placeholders))",
            ids.map { SQLValue.text($0) }
        )
    }
}
