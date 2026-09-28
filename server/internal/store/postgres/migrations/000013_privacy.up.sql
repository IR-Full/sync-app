-- Per-user privacy settings.
--
-- Three separate columns rather than one JSON blob: each is read on a different
-- hot path (presence fanout, profile reads, join) and a blob would mean parsing
-- the whole thing to answer one question — plus losing the ability to say what
-- the valid values are.
--
-- 'everyone' is the default for all three because it is what the system did
-- before this existed, and a migration that silently tightens an existing
-- account's visibility is a migration that makes people think the app broke.
-- The choice is offered, not imposed.
--
-- TEXT with a CHECK rather than an enum type: adding a value to a Postgres enum
-- cannot run in the same transaction as other DDL in older versions, which makes
-- a future migration awkward for no gain here.
ALTER TABLE users
  ADD COLUMN IF NOT EXISTS privacy_last_seen TEXT NOT NULL DEFAULT 'everyone'
    CHECK (privacy_last_seen IN ('everyone', 'contacts', 'nobody')),
  ADD COLUMN IF NOT EXISTS privacy_avatar TEXT NOT NULL DEFAULT 'everyone'
    CHECK (privacy_avatar IN ('everyone', 'contacts', 'nobody')),
  ADD COLUMN IF NOT EXISTS privacy_groups TEXT NOT NULL DEFAULT 'everyone'
    CHECK (privacy_groups IN ('everyone', 'contacts', 'nobody'));
