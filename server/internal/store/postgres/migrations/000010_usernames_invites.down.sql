DROP TABLE IF EXISTS invite_links;
DROP INDEX IF EXISTS chats_username_idx;
ALTER TABLE chats DROP COLUMN IF EXISTS username;
