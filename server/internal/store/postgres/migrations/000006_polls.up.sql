-- Polls. The QUESTION is a normal message (message_id below), so a poll inherits
-- chat ordering, history paging, and permission checks for free — only the tally
-- lives in its own tables.
--
-- options is JSONB (an ordered array of strings): options are fixed at creation
-- and always read as a whole, so a child table would buy nothing.
CREATE TABLE IF NOT EXISTS polls (
    id           BIGINT PRIMARY KEY,
    chat_id      BIGINT  NOT NULL,
    message_id   BIGINT  NOT NULL,
    creator_id   BIGINT  NOT NULL,
    question     TEXT    NOT NULL,
    options      JSONB   NOT NULL,
    multi_choice BOOLEAN NOT NULL DEFAULT FALSE,
    anonymous    BOOLEAN NOT NULL DEFAULT FALSE,
    closed       BOOLEAN NOT NULL DEFAULT FALSE,
    created_at   BIGINT  NOT NULL
);

-- Clients hold a message id (from history/fanout) and need its poll.
CREATE UNIQUE INDEX IF NOT EXISTS polls_message_idx ON polls (message_id);

-- One row per (poll, user, option): the PK enforces "no double-counting the same
-- option", while still allowing several rows per user in a multi-choice poll.
CREATE TABLE IF NOT EXISTS poll_votes (
    poll_id      BIGINT NOT NULL,
    user_id      BIGINT NOT NULL,
    option_index INTEGER NOT NULL,
    created_at   BIGINT NOT NULL,
    PRIMARY KEY (poll_id, user_id, option_index)
);

-- Tallying groups by option within a poll.
CREATE INDEX IF NOT EXISTS poll_votes_tally_idx ON poll_votes (poll_id, option_index);
