-- Voice/video calls and conferences.
--
-- The server stores only SIGNALING state: who is in the room and what the room's
-- lifecycle state is. Audio/video never touches these tables (or the server) —
-- media flows peer-to-peer or through an SFU, and the SDP/ICE payloads the server
-- relays are opaque and never persisted.
--
-- Calls are low-volume next to messages, so they live with the metadata store;
-- keeping them durable also yields call history for free.
CREATE TABLE IF NOT EXISTS calls (
    id           BIGINT PRIMARY KEY,
    chat_id      BIGINT NOT NULL,
    initiator_id BIGINT NOT NULL,
    kind         TEXT   NOT NULL,          -- audio | video
    state        TEXT   NOT NULL,          -- ringing | active | ended
    created_at   BIGINT NOT NULL,
    ended_at     BIGINT NOT NULL DEFAULT 0
);

-- One row per (call, user). Multiple devices of a user may be invited; the first
-- to accept takes the call, so device_id is recorded on join, not on invite.
CREATE TABLE IF NOT EXISTS call_participants (
    call_id   BIGINT NOT NULL,
    user_id   BIGINT NOT NULL,
    device_id TEXT   NOT NULL DEFAULT '',
    state     TEXT   NOT NULL,             -- invited | joined | left | declined
    joined_at BIGINT NOT NULL DEFAULT 0,
    left_at   BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (call_id, user_id)
);

-- "Is there already a call in this chat?" — the lookup that stops two people
-- starting rival rooms in the same chat. Partial index: ended calls are history.
CREATE INDEX IF NOT EXISTS calls_active_chat_idx
    ON calls (chat_id) WHERE state <> 'ended';
