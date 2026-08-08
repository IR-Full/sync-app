-- Per-user address book and block list.
--
-- Rows are keyed by (owner, user) because a contact is PRIVATE to its owner: the
-- local name Alice gave Bob is Alice's data, not a property of Bob, and blocking
-- is one-directional. This is not a shared social graph.
CREATE TABLE IF NOT EXISTS contacts (
    owner_id   BIGINT  NOT NULL,
    user_id    BIGINT  NOT NULL,
    name       TEXT    NOT NULL DEFAULT '',
    blocked    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at BIGINT  NOT NULL,
    updated_at BIGINT  NOT NULL,
    PRIMARY KEY (owner_id, user_id)
);

-- Incremental sync: "give me everything that changed since T" for one owner.
-- Ordering by updated_at makes the client's cursor a simple high-water mark.
CREATE INDEX IF NOT EXISTS contacts_sync_idx ON contacts (owner_id, updated_at);
