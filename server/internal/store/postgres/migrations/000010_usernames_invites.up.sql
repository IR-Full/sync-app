-- Public chat handles and invite links.

-- A public handle makes a channel reachable without an invite (t.me/<name>).
-- Case-insensitive uniqueness: "News" and "news" must not be different chats, or
-- handles become a phishing surface.
ALTER TABLE chats ADD COLUMN IF NOT EXISTS username TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS chats_username_idx
    ON chats (LOWER(username)) WHERE username <> '';

-- Invite links. The code is the secret (unguessable), so it is the primary key.
-- Bounds are first-class: an unbounded, unrevocable link is a permanent hole in
-- a private chat's membership.
CREATE TABLE IF NOT EXISTS invite_links (
    code       TEXT   PRIMARY KEY,
    chat_id    BIGINT NOT NULL,
    created_by BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    expires_at BIGINT NOT NULL DEFAULT 0,
    max_uses   INTEGER NOT NULL DEFAULT 0,
    uses       INTEGER NOT NULL DEFAULT 0,
    revoked    BOOLEAN NOT NULL DEFAULT FALSE
);

-- Listing/revoking a chat's links.
CREATE INDEX IF NOT EXISTS invite_chat_idx ON invite_links (chat_id) WHERE revoked = FALSE;
