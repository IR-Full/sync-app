DROP INDEX IF EXISTS messages_thread_idx;
ALTER TABLE messages DROP COLUMN IF EXISTS reply_count;
ALTER TABLE messages DROP COLUMN IF EXISTS thread_root;
ALTER TABLE messages DROP COLUMN IF EXISTS attachment;
