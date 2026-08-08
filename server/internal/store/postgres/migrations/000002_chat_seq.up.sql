-- Dedicated per-chat sequence source, decoupled from the chats metadata table.
-- The message write path (allocate seq → insert message → stage outbox) now bumps
-- chat_seq via UPSERT instead of chats.last_seq, so it is SELF-CONTAINED: it no
-- longer needs the chats row. That is what makes chat_id-sharded message storage
-- correct — a shard holding only messages+seq+outbox for its chats can allocate a
-- gap-free per-chat seq locally, co-located with the messages, while chat metadata
-- (title/owner/members) stays in the central store.
--
-- Backfilled from chats.last_seq so an existing single-node deployment upgrades
-- with sequence continuity.
CREATE TABLE IF NOT EXISTS chat_seq (
    chat_id  BIGINT PRIMARY KEY,
    last_seq BIGINT NOT NULL DEFAULT 0
);

INSERT INTO chat_seq (chat_id, last_seq)
    SELECT id, last_seq FROM chats
    ON CONFLICT (chat_id) DO NOTHING;
