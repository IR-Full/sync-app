-- Synapse initial schema (Section 7/8). Postgres-only to start; the message
-- table is the first candidate to move to a wide-column store (Cassandra/Scylla)
-- when write volume outgrows a single primary. Shard keys are noted in comments.

-- Users -----------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS users (
    id            BIGINT PRIMARY KEY,           -- snowflake
    username      TEXT NOT NULL UNIQUE,
    display_name  TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL,
    created_at    BIGINT NOT NULL
);

-- Devices ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS devices (
    id         BIGINT PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id),
    platform   TEXT NOT NULL,
    push_token TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL,
    last_seen  BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_devices_user ON devices(user_id);

-- Sessions --------------------------------------------------------------------
-- Tokens are stored hashed in production; MVP stores opaque values directly.
CREATE TABLE IF NOT EXISTS sessions (
    id           BIGINT PRIMARY KEY,
    user_id      BIGINT NOT NULL REFERENCES users(id),
    device_id    BIGINT NOT NULL,
    token        TEXT NOT NULL UNIQUE,
    resume_token TEXT UNIQUE,
    created_at   BIGINT NOT NULL,
    expires_at   BIGINT NOT NULL,
    revoked_at   BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);

-- Chats -----------------------------------------------------------------------
-- last_seq is the per-chat monotonic counter; it is the ordering source of
-- truth and is bumped atomically inside the message write transaction.
CREATE TABLE IF NOT EXISTS chats (
    id         BIGINT PRIMARY KEY,             -- shard key for messages
    type       TEXT NOT NULL,                  -- direct | group | channel
    title      TEXT NOT NULL DEFAULT '',
    owner_id   BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    last_seq   BIGINT NOT NULL DEFAULT 0
);

-- Canonical 1:1 lookup: unordered pair -> chat id.
CREATE TABLE IF NOT EXISTS direct_index (
    pair_key TEXT PRIMARY KEY,                 -- "minUser|maxUser"
    chat_id  BIGINT NOT NULL REFERENCES chats(id)
);

-- Chat members ----------------------------------------------------------------
CREATE TABLE IF NOT EXISTS chat_members (
    chat_id   BIGINT NOT NULL REFERENCES chats(id),
    user_id   BIGINT NOT NULL,
    role      TEXT NOT NULL DEFAULT 'member',
    joined_at BIGINT NOT NULL,
    muted     BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (chat_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_members_user ON chat_members(user_id);

-- Messages --------------------------------------------------------------------
-- Ordering is by (chat_id, seq). id is a globally unique snowflake. The unique
-- index on (sender_id, dedup_key) enforces idempotent writes: a retried send
-- resolves to the already-stored row instead of duplicating.
CREATE TABLE IF NOT EXISTS messages (
    id         BIGINT PRIMARY KEY,
    chat_id    BIGINT NOT NULL,
    sender_id  BIGINT NOT NULL,
    seq        BIGINT NOT NULL,
    text       TEXT NOT NULL DEFAULT '',
    media_ref  TEXT NOT NULL DEFAULT '',
    reply_to   BIGINT NOT NULL DEFAULT 0,
    edited     BOOLEAN NOT NULL DEFAULT FALSE,
    deleted    BOOLEAN NOT NULL DEFAULT FALSE,
    dedup_key  TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL,
    edited_at  BIGINT NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_messages_chat_seq ON messages(chat_id, seq);
CREATE UNIQUE INDEX IF NOT EXISTS uq_messages_dedup
    ON messages(sender_id, dedup_key) WHERE dedup_key <> '';
CREATE INDEX IF NOT EXISTS idx_messages_chat_seq_desc ON messages(chat_id, seq DESC);

-- Read state ------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS read_state (
    chat_id    BIGINT NOT NULL,
    user_id    BIGINT NOT NULL,
    up_to_seq  BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    PRIMARY KEY (chat_id, user_id)
);

-- Transactional outbox --------------------------------------------------------
-- Events are inserted in the same transaction as the message that produced them,
-- then a relay publishes unsent rows to the event bus and marks them sent. This
-- makes event emission crash-safe (durable at-least-once) instead of best-effort.
CREATE TABLE IF NOT EXISTS outbox (
    id         BIGINT PRIMARY KEY,       -- snowflake
    subject    TEXT NOT NULL,
    key        TEXT NOT NULL DEFAULT '',
    payload    BYTEA NOT NULL,
    created_at BIGINT NOT NULL,
    sent_at    BIGINT NOT NULL DEFAULT 0,
    claimed_at BIGINT NOT NULL DEFAULT 0, -- relay claim time; stale claims are reclaimable
    trace      TEXT NOT NULL DEFAULT ''   -- serialized W3C trace context
);
ALTER TABLE outbox ADD COLUMN IF NOT EXISTS claimed_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE outbox ADD COLUMN IF NOT EXISTS trace TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_outbox_unsent ON outbox(id) WHERE sent_at = 0;
