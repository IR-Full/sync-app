package postgres

// seqBumpSQL atomically allocates a chat's next per-chat sequence from the
// dedicated chat_seq table, auto-creating the row on the first message. It is
// SELF-CONTAINED (no dependency on the chats metadata row), so it works on a
// message shard that holds no chat metadata — the enabler for chat_id sharding.
// Same-chat calls in one pipelined batch return consecutive seqs because the
// server runs the queued statements in order. Authorization (CanPost) is enforced
// upstream, so allocating a seq here does not imply the write is permitted.
const seqBumpSQL = `INSERT INTO chat_seq (chat_id, last_seq) VALUES ($1, 1)
	ON CONFLICT (chat_id) DO UPDATE SET last_seq = chat_seq.last_seq + 1
	RETURNING last_seq`

// msgCols is the canonical message column list. Every message read uses it with
// scanMsg so a schema addition lands in one place instead of six query strings.
const msgCols = `id, chat_id, sender_id, seq, text, media_ref, reply_to, edited, deleted, created_at, edited_at, attachment, thread_root, reply_count, fwd_chat_id, fwd_msg_id, fwd_sender_id, expires_at`
