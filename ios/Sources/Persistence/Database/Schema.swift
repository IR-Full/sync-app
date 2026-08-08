import Foundation

/// Forward-only migrations, applied in order. Never edit a shipped entry —
/// append a new one, exactly as `server/internal/store/postgres/migrations`
/// does. An edited migration is a schema that differs between a fresh install
/// and an upgrade, which is the kind of bug that only appears in the field.
public enum Schema {
    public static let migrations: [String] = [
        v1,
        v2,
    ]

    /// v1 — chats, messages, the outbox, contacts, users, and a key/value slot
    /// for sync cursors.
    private static let v1 = """
        CREATE TABLE chats (
            id                   TEXT PRIMARY KEY,
            kind                 TEXT    NOT NULL,
            title                TEXT    NOT NULL DEFAULT '',
            username             TEXT,
            owner_id             TEXT    NOT NULL DEFAULT '',
            peer_user_id         TEXT,
            last_message_preview TEXT    NOT NULL DEFAULT '',
            last_message_at      INTEGER,
            last_seq             INTEGER NOT NULL DEFAULT 0,
            last_read_seq        INTEGER NOT NULL DEFAULT 0,
            is_muted             INTEGER NOT NULL DEFAULT 0,
            hidden               INTEGER NOT NULL DEFAULT 0
        );

        -- The list screen orders by recency and filters hidden chats, so that is
        -- the index. `last_message_at` is nullable (a chat created but never
        -- written to), and NULLs sort last under DESC, which is what we want.
        CREATE INDEX chats_recent ON chats (hidden, last_message_at DESC);

        CREATE TABLE messages (
            id         TEXT PRIMARY KEY,
            chat_id    TEXT    NOT NULL,
            sender_id  TEXT    NOT NULL DEFAULT '',
            seq        INTEGER NOT NULL DEFAULT 0,
            text       TEXT    NOT NULL DEFAULT '',
            sent_at    INTEGER NOT NULL DEFAULT 0,
            state      TEXT    NOT NULL DEFAULT 'sent',
            edited     INTEGER NOT NULL DEFAULT 0,
            deleted    INTEGER NOT NULL DEFAULT 0,
            reply_to   TEXT,
            attachment BLOB,
            forward    BLOB,
            expires_at INTEGER,
            reactions  BLOB,
            dedup_key  TEXT    NOT NULL DEFAULT '',
            FOREIGN KEY (chat_id) REFERENCES chats (id) ON DELETE CASCADE
        );

        -- Paging reads (chat_id, seq) descending; ordering is by seq, never by
        -- timestamp, because seq is the server's gap-free ordering and two
        -- messages can share a millisecond.
        CREATE INDEX messages_chat_seq ON messages (chat_id, seq DESC);

        -- A partial unique index: the dedup key is the idempotency guarantee, so
        -- a redelivered SEND_ACK must collide rather than insert a twin. Rows
        -- with no key (everything received rather than sent) are exempt.
        CREATE UNIQUE INDEX messages_dedup ON messages (dedup_key) WHERE dedup_key <> '';

        -- Messages composed while offline. A row lives here from the moment the
        -- user hits send until a SEND_ACK confirms durable persistence; the
        -- dedup key is the primary key because it is what makes a retry safe.
        CREATE TABLE outbox (
            dedup_key  TEXT PRIMARY KEY,
            chat_id    TEXT    NOT NULL,
            text       TEXT    NOT NULL DEFAULT '',
            reply_to   TEXT,
            created_at INTEGER NOT NULL,
            attempts   INTEGER NOT NULL DEFAULT 0,
            last_error TEXT
        );

        -- Flushed oldest-first so a chat's messages keep the order they were
        -- typed in, even across an app restart.
        CREATE INDEX outbox_order ON outbox (created_at);

        CREATE TABLE contacts (
            user_id    TEXT PRIMARY KEY,
            name       TEXT    NOT NULL DEFAULT '',
            blocked    INTEGER NOT NULL DEFAULT 0,
            updated_at INTEGER NOT NULL DEFAULT 0
        );

        -- What little we can know about other users: the gateway has no user
        -- directory, so rows appear here only when we learn something (an
        -- @handle we resolved, a presence push, a contact name).
        CREATE TABLE users (
            id           TEXT PRIMARY KEY,
            username     TEXT,
            display_name TEXT,
            online       INTEGER NOT NULL DEFAULT 0,
            last_seen_at INTEGER
        );

        CREATE TABLE meta (
            key   TEXT PRIMARY KEY,
            value TEXT NOT NULL
        );
        """

    /// v2 — attachments on queued messages, and cross-device drafts.
    ///
    /// The outbox gains an attachment column rather than the bytes themselves.
    /// The upload has already happened by the time a row lands here (a ticket is
    /// minted over the protocol and its signed size is binding, so there is
    /// nothing sensible to queue offline); what waits is the *message* carrying
    /// the resulting `media_ref`.
    private static let v2 = """
        ALTER TABLE outbox ADD COLUMN attachment BLOB;

        -- Drafts are per-user and mirrored by the gateway to that user's other
        -- devices, so this table is a cache of a server-side value, not a purely
        -- local one. `updated_at` is the sync cursor's unit.
        CREATE TABLE drafts (
            chat_id    TEXT PRIMARY KEY,
            text       TEXT    NOT NULL DEFAULT '',
            reply_to   TEXT,
            updated_at INTEGER NOT NULL DEFAULT 0
        );
        """
}
