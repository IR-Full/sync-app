-- Per-member chat settings, and the indexes the chat list needs.
--
-- The `muted` column has existed since the first migration and nothing ever read
-- it: there was no protocol message to set it and the notification path never
-- consulted it, so muting a chat was impossible while the schema implied it was
-- supported. These columns are the working version.
--
-- muted_until is a deadline rather than a flag because "mute for eight hours" is
-- what people want far more often than "mute forever", and a boolean cannot say
-- it. A very distant deadline expresses "forever", so the deadline subsumes the
-- flag rather than complicating it.
ALTER TABLE chat_members
  ADD COLUMN IF NOT EXISTS muted_until BIGINT  NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS pinned      BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS archived    BOOLEAN NOT NULL DEFAULT FALSE;

-- Carry the old boolean forward so an account that had muted something stays
-- muted. A far-future deadline is how "forever" is spelled here.
UPDATE chat_members SET muted_until = 4102444800000 WHERE muted AND muted_until = 0;

-- The chat list reads chat_members by user and needs the flags with them. The
-- existing idx_members_user covers the lookup; this adds the columns the page
-- selects so the common case is index-only.
CREATE INDEX IF NOT EXISTS idx_members_user_list
  ON chat_members(user_id, archived, pinned, chat_id);

-- The list's last-message lookup is a LATERAL over messages per chat. The
-- existing idx_messages_chat_seq_desc serves it, but only for live rows — a chat
-- whose tail is all deleted tombstones would walk backwards through them.
CREATE INDEX IF NOT EXISTS idx_messages_chat_live_seq
  ON messages(chat_id, seq DESC) WHERE NOT deleted;
