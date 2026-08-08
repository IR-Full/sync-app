-- Forwarding, self-destruct, and scheduled send.

-- Forward provenance. Kept as plain columns (not a join) because a forward must
-- survive the ORIGINAL being deleted — the copy is independent content that only
-- remembers where it came from.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS fwd_chat_id   BIGINT NOT NULL DEFAULT 0;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS fwd_msg_id    BIGINT NOT NULL DEFAULT 0;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS fwd_sender_id BIGINT NOT NULL DEFAULT 0;

-- Self-destruct: 0 = never. The reaper tombstones rows past their time, so an
-- expired message stops being readable even if a client never asks again.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS expires_at BIGINT NOT NULL DEFAULT 0;

-- Partial index: only the (small) set of expiring messages is scanned.
CREATE INDEX IF NOT EXISTS messages_expiring_idx
    ON messages (expires_at) WHERE expires_at > 0 AND deleted = FALSE;

-- Scheduled messages live OUTSIDE the message log until they are due: a pending
-- send must not occupy a chat sequence number (that would leave a permanent gap
-- if it is cancelled) and must not be visible to history or fanout.
CREATE TABLE IF NOT EXISTS scheduled_messages (
    id         BIGINT PRIMARY KEY,
    chat_id    BIGINT NOT NULL,
    sender_id  BIGINT NOT NULL,
    text       TEXT   NOT NULL DEFAULT '',
    media_ref  TEXT   NOT NULL DEFAULT '',
    attachment JSONB,
    reply_to   BIGINT NOT NULL DEFAULT 0,
    ttl_secs   INTEGER NOT NULL DEFAULT 0,
    send_at    BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    sent       BOOLEAN NOT NULL DEFAULT FALSE
);

-- The dispatcher's only query: "what is due and not yet sent?"
CREATE INDEX IF NOT EXISTS scheduled_due_idx ON scheduled_messages (send_at) WHERE sent = FALSE;
-- A user listing/cancelling their own pending sends.
CREATE INDEX IF NOT EXISTS scheduled_owner_idx ON scheduled_messages (sender_id, chat_id) WHERE sent = FALSE;
