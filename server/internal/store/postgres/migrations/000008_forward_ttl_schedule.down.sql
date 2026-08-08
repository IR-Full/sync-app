DROP TABLE IF EXISTS scheduled_messages;
DROP INDEX IF EXISTS messages_expiring_idx;
ALTER TABLE messages DROP COLUMN IF EXISTS expires_at;
ALTER TABLE messages DROP COLUMN IF EXISTS fwd_sender_id;
ALTER TABLE messages DROP COLUMN IF EXISTS fwd_msg_id;
ALTER TABLE messages DROP COLUMN IF EXISTS fwd_chat_id;
