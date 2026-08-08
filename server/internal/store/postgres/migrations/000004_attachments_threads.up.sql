-- Message attachments (voice messages, round video notes, files, images) and
-- threads.
--
-- attachment is JSONB because the metadata differs per kind (a voice message has
-- a waveform + duration, a video note adds dimensions, a file has a filename) and
-- new kinds must not require a migration each time. The BYTES always live in the
-- media service — this column only holds the descriptor.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS attachment JSONB;

-- thread_root is the message that starts the thread a reply belongs to. It is
-- derived from reply_to server-side (a reply to a reply inherits the same root),
-- so an entire conversation branch is one indexed lookup instead of a recursive
-- walk. 0 = top-level message.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS thread_root BIGINT NOT NULL DEFAULT 0;

-- reply_count is maintained on the ROOT message so a client can render "12
-- replies" without counting rows.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS reply_count INTEGER NOT NULL DEFAULT 0;

-- Fetching a thread: all replies under one root, in order.
CREATE INDEX IF NOT EXISTS messages_thread_idx
    ON messages (chat_id, thread_root, seq) WHERE thread_root <> 0;
