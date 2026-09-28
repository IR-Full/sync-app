-- Undelivered end-to-end ciphertext, held for devices that were offline when it
-- was relayed.
--
-- Before this table SECRET_SEND was pure relay: the gateway asked the router
-- which nodes held the recipient and published to each. Zero nodes — recipient
-- offline — meant the ciphertext was discarded, no push was queued, and the
-- sender was told nothing at all. A secret chat between two people who are not
-- online simultaneously delivered nothing.
--
-- to_device_id / from_device_id are TEXT, not BIGINT. A device id is asserted by
-- the client in HELLO and is routinely a string like "web-3f2a" — the rest of the
-- schema stores devices under a BIGINT id, which is fine for a row keyed by the
-- account, but a queue is addressed by the exact device whose ratchet session
-- can decrypt the payload. Coercing it to a number would file envelopes under
-- device 0 and deliver them to the wrong session, or to none.
--
-- header/ciphertext are BYTEA: these are the Double Ratchet wire bytes. The
-- server cannot read them and never tries.
CREATE TABLE IF NOT EXISTS secret_queue (
    id             BIGINT PRIMARY KEY,          -- snowflake; also the delivery cursor
    to_user_id     BIGINT NOT NULL,
    to_device_id   TEXT   NOT NULL,
    from_user_id   BIGINT NOT NULL,
    from_device_id TEXT   NOT NULL DEFAULT '',
    header         BYTEA  NOT NULL,
    ciphertext     BYTEA  NOT NULL,
    created_at     BIGINT NOT NULL,
    expires_at     BIGINT NOT NULL
);

-- The delivery read: "next N for this device after this cursor, oldest first".
-- id is a snowflake, so ordering by it IS ordering by time, and the same index
-- serves both the keyset walk and the per-device cap enforced on insert.
CREATE INDEX IF NOT EXISTS idx_secret_queue_device
    ON secret_queue(to_user_id, to_device_id, id);

-- The collector's read. A queue of undelivered ciphertext addressed by
-- user+device is also a record of who messaged whom and when — metadata the
-- stateless relay never persisted — so expiry is not optional and wants its own
-- index rather than a sequential scan.
CREATE INDEX IF NOT EXISTS idx_secret_queue_expiry
    ON secret_queue(expires_at);
