DROP INDEX IF EXISTS idx_messages_chat_live_seq;
DROP INDEX IF EXISTS idx_members_user_list;
ALTER TABLE chat_members
  DROP COLUMN IF EXISTS muted_until,
  DROP COLUMN IF EXISTS pinned,
  DROP COLUMN IF EXISTS archived;
