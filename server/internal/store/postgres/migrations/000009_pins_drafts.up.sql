-- Pinned messages and per-user drafts.

-- Pins are CHAT-WIDE: every member sees the same set, so the key is
-- (chat_id, message_id) with no owner dimension.
CREATE TABLE IF NOT EXISTS pinned_messages (
    chat_id    BIGINT NOT NULL,
    message_id BIGINT NOT NULL,
    pinned_by  BIGINT NOT NULL,
    pinned_at  BIGINT NOT NULL,
    PRIMARY KEY (chat_id, message_id)
);

-- Listing a chat's pins newest-first is the only read.
CREATE INDEX IF NOT EXISTS pinned_chat_idx ON pinned_messages (chat_id, pinned_at DESC);

-- Drafts are PRIVATE to a user but shared across THEIR devices, so the key is
-- (user_id, chat_id) — one draft per conversation, last write wins.
CREATE TABLE IF NOT EXISTS drafts (
    user_id    BIGINT NOT NULL,
    chat_id    BIGINT NOT NULL,
    text       TEXT   NOT NULL DEFAULT '',
    reply_to   BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL,
    PRIMARY KEY (user_id, chat_id)
);

-- Incremental sync: "what did I change since T?" across a user's devices.
CREATE INDEX IF NOT EXISTS drafts_sync_idx ON drafts (user_id, updated_at);
