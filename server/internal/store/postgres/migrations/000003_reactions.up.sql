-- Emoji reactions on messages. Keyed by (message_id, user_id): a user holds at
-- most one reaction per message, so re-reacting toggles/replaces rather than
-- accumulating. chat_id is carried (not just derivable) so the table shards with
-- the messages it belongs to — same co-location rule as chat_seq.
CREATE TABLE IF NOT EXISTS reactions (
    chat_id    BIGINT NOT NULL,
    message_id BIGINT NOT NULL,
    user_id    BIGINT NOT NULL,
    emoji      TEXT   NOT NULL,
    created_at BIGINT NOT NULL,
    PRIMARY KEY (message_id, user_id)
);

-- Listing a message's reactions is the hot read; chat_id supports bulk/export.
CREATE INDEX IF NOT EXISTS reactions_chat_idx ON reactions (chat_id, message_id);
