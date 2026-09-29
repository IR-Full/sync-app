// Postgres implementations for the product surface built on top of the message
// log: reactions, read state, calls, polls, contacts, the self-destruct reaper,
// scheduled sends, pins, drafts and invites. They share the connection pool and
// the helpers in postgres.go; they are separated because none of them is on the
// message write path, which is the part that has to stay easy to read.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/store"
	"github.com/jackc/pgx/v5"
)

// --- ReactionStore ---

// SetReaction toggles a user's reaction on a message in ONE round trip: the
// DELETE removes an identical existing reaction (toggle off) and the INSERT
// applies a new/changed one only when that delete matched nothing. Wrapped in a
// transaction so the pair is atomic under concurrent taps from a user's devices.
func (s *Store) SetReaction(ctx context.Context, r *model.Reaction) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	tag, err := tx.Exec(ctx,
		`DELETE FROM reactions WHERE message_id=$1 AND user_id=$2 AND emoji=$3`,
		atoi(r.MessageID), atoi(r.UserID), r.Emoji)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() > 0 { // same emoji existed → toggled off
		return false, tx.Commit(ctx)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO reactions (chat_id, message_id, user_id, emoji, created_at)
		 VALUES ($1,$2,$3,$4,$5)
		 ON CONFLICT (message_id, user_id) DO UPDATE SET emoji=EXCLUDED.emoji, created_at=EXCLUDED.created_at`,
		atoi(r.ChatID), atoi(r.MessageID), atoi(r.UserID), r.Emoji, r.CreatedAt)
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (s *Store) ListReactions(ctx context.Context, _, messageID string) ([]*model.Reaction, error) {
	rows, err := s.reader().Query(ctx,
		`SELECT chat_id, message_id, user_id, emoji, created_at FROM reactions WHERE message_id=$1`,
		atoi(messageID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Reaction
	for rows.Next() {
		var r model.Reaction
		var cid, mid, uid int64
		if err := rows.Scan(&cid, &mid, &uid, &r.Emoji, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.ChatID, r.MessageID, r.UserID = itoa(cid), itoa(mid), itoa(uid)
		out = append(out, &r)
	}
	return out, rows.Err()
}

// --- ReadStore ---

func (s *Store) SetRead(ctx context.Context, rs *model.ReadState) error {
	// Monotonic upsert: only advance the cursor.
	_, err := s.pool.Exec(ctx,
		`INSERT INTO read_state (chat_id, user_id, up_to_seq, updated_at)
		 VALUES ($1,$2,$3,$4)
		 ON CONFLICT (chat_id, user_id) DO UPDATE SET
		   up_to_seq = GREATEST(read_state.up_to_seq, EXCLUDED.up_to_seq),
		   updated_at = EXCLUDED.updated_at`,
		atoi(rs.ChatID), atoi(rs.UserID), int64(rs.UpToSeq), rs.UpdatedAt)
	return err
}

func (s *Store) GetRead(ctx context.Context, chatID, userID string) (*model.ReadState, error) {
	var (
		rs       model.ReadState
		cid, uid int64
		seq      int64
	)
	err := s.reader().QueryRow(ctx,
		`SELECT chat_id, user_id, up_to_seq, updated_at FROM read_state WHERE chat_id=$1 AND user_id=$2`,
		atoi(chatID), atoi(userID)).Scan(&cid, &uid, &seq, &rs.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rs.ChatID, rs.UserID, rs.UpToSeq = itoa(cid), itoa(uid), uint64(seq)
	return &rs, nil
}

// wrap maps unique-violation errors to store.ErrConflict.
func wrap(err error) error {
	if err == nil {
		return nil
	}
	if isUniqueViolation(err) {
		return store.ErrConflict
	}
	return fmt.Errorf("postgres: %w", err)
}

// attachJSON marshals an attachment descriptor for the JSONB column (nil → NULL).
func attachJSON(a *model.Attachment) any {
	if a == nil {
		return nil
	}
	b, err := json.Marshal(a)
	if err != nil {
		return nil
	}
	return b
}

// --- CallStore ---

func (s *Store) CreateCall(ctx context.Context, c *model.Call) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO calls (id, chat_id, initiator_id, kind, state, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		atoi(c.ID), atoi(c.ChatID), atoi(c.InitiatorID), string(c.Kind), string(c.State), c.CreatedAt)
	return wrap(err)
}

func (s *Store) GetCall(ctx context.Context, id string) (*model.Call, error) {
	var (
		c             model.Call
		cid, chid, ii int64
		kind, state   string
	)
	err := s.pool.QueryRow(ctx,
		`SELECT id, chat_id, initiator_id, kind, state, created_at, ended_at FROM calls WHERE id=$1`, atoi(id)).
		Scan(&cid, &chid, &ii, &kind, &state, &c.CreatedAt, &c.EndedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.ID, c.ChatID, c.InitiatorID = itoa(cid), itoa(chid), itoa(ii)
	c.Kind, c.State = model.CallKind(kind), model.CallState(state)
	return &c, nil
}

func (s *Store) SetCallState(ctx context.Context, id string, state model.CallState, at int64) error {
	var ended any
	if state == model.CallEnded {
		ended = at
	} else {
		ended = int64(0)
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE calls SET state=$2, ended_at=$3 WHERE id=$1`, atoi(id), string(state), ended)
	return err
}

func (s *Store) UpsertParticipant(ctx context.Context, p *model.CallParticipant) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO call_participants (call_id, user_id, device_id, state, joined_at, left_at)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (call_id, user_id) DO UPDATE SET
		   device_id=EXCLUDED.device_id, state=EXCLUDED.state,
		   joined_at=GREATEST(call_participants.joined_at, EXCLUDED.joined_at),
		   left_at=GREATEST(call_participants.left_at, EXCLUDED.left_at)`,
		atoi(p.CallID), atoi(p.UserID), p.DeviceID, string(p.State), p.JoinedAt, p.LeftAt)
	return err
}

func (s *Store) ListParticipants(ctx context.Context, callID string) ([]*model.CallParticipant, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT call_id, user_id, device_id, state, joined_at, left_at FROM call_participants WHERE call_id=$1`,
		atoi(callID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.CallParticipant
	for rows.Next() {
		var (
			p        model.CallParticipant
			cid, uid int64
			state    string
		)
		if err := rows.Scan(&cid, &uid, &p.DeviceID, &state, &p.JoinedAt, &p.LeftAt); err != nil {
			return nil, err
		}
		p.CallID, p.UserID, p.State = itoa(cid), itoa(uid), model.ParticipantState(state)
		out = append(out, &p)
	}
	return out, rows.Err()
}

func (s *Store) ActiveCallForChat(ctx context.Context, chatID string) (*model.Call, error) {
	var id int64
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM calls WHERE chat_id=$1 AND state <> 'ended' ORDER BY created_at DESC LIMIT 1`,
		atoi(chatID)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetCall(ctx, itoa(id))
}

// --- PollStore ---

func (s *Store) CreatePoll(ctx context.Context, p *model.Poll) error {
	opts, err := json.Marshal(p.Options)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO polls (id, chat_id, message_id, creator_id, question, options, multi_choice, anonymous, closed, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		atoi(p.ID), atoi(p.ChatID), atoi(p.MessageID), atoi(p.CreatorID), p.Question, opts,
		p.MultiChoice, p.Anonymous, p.Closed, p.CreatedAt)
	return wrap(err)
}

func (s *Store) GetPoll(ctx context.Context, id string) (*model.Poll, error) {
	return s.scanPoll(ctx, `WHERE id=$1`, atoi(id))
}

func (s *Store) GetPollByMessage(ctx context.Context, messageID string) (*model.Poll, error) {
	return s.scanPoll(ctx, `WHERE message_id=$1`, atoi(messageID))
}

func (s *Store) scanPoll(ctx context.Context, where string, arg any) (*model.Poll, error) {
	var (
		p                  model.Poll
		pid, cid, mid, crt int64
		opts               []byte
	)
	err := s.reader().QueryRow(ctx,
		`SELECT id, chat_id, message_id, creator_id, question, options, multi_choice, anonymous, closed, created_at
		 FROM polls `+where, arg).
		Scan(&pid, &cid, &mid, &crt, &p.Question, &opts, &p.MultiChoice, &p.Anonymous, &p.Closed, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.ID, p.ChatID, p.MessageID, p.CreatorID = itoa(pid), itoa(cid), itoa(mid), itoa(crt)
	if err := json.Unmarshal(opts, &p.Options); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) ClosePoll(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE polls SET closed=TRUE WHERE id=$1`, atoi(id))
	return err
}

// Vote applies the poll's choice semantics atomically: single-choice replaces the
// voter's previous pick, multi-choice toggles the given option.
func (s *Store) Vote(ctx context.Context, v *model.PollVote, multiChoice bool) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	// Lock the poll row for the life of this transaction and re-read `closed`
	// inside it. The caller checked it too, but a concurrent ClosePoll could land
	// between that read and this write — and a vote counted into a closed poll is
	// a tally that changes after everyone was told it was final.
	var closed bool
	if err := tx.QueryRow(ctx,
		`SELECT closed FROM polls WHERE id=$1 FOR UPDATE`, atoi(v.PollID)).Scan(&closed); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, store.ErrNotFound
		}
		return false, err
	}
	if closed {
		return false, store.ErrPollClosed
	}

	if multiChoice {
		tag, err := tx.Exec(ctx,
			`DELETE FROM poll_votes WHERE poll_id=$1 AND user_id=$2 AND option_index=$3`,
			atoi(v.PollID), atoi(v.UserID), v.OptionIndex)
		if err != nil {
			return false, err
		}
		if tag.RowsAffected() > 0 { // toggled off
			return false, tx.Commit(ctx)
		}
	} else if _, err := tx.Exec(ctx,
		`DELETE FROM poll_votes WHERE poll_id=$1 AND user_id=$2`,
		atoi(v.PollID), atoi(v.UserID)); err != nil {
		return false, err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO poll_votes (poll_id, user_id, option_index, created_at) VALUES ($1,$2,$3,$4)
		 ON CONFLICT DO NOTHING`,
		atoi(v.PollID), atoi(v.UserID), v.OptionIndex, v.CreatedAt); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (s *Store) Tally(ctx context.Context, pollID string) (map[int32]int, error) {
	rows, err := s.reader().Query(ctx,
		`SELECT option_index, COUNT(*) FROM poll_votes WHERE poll_id=$1 GROUP BY option_index`, atoi(pollID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int32]int{}
	for rows.Next() {
		var idx int32
		var n int
		if err := rows.Scan(&idx, &n); err != nil {
			return nil, err
		}
		out[idx] = n
	}
	return out, rows.Err()
}

func (s *Store) VotedOptions(ctx context.Context, pollID, userID string) ([]int32, error) {
	rows, err := s.reader().Query(ctx,
		`SELECT option_index FROM poll_votes WHERE poll_id=$1 AND user_id=$2 ORDER BY option_index`,
		atoi(pollID), atoi(userID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int32
	for rows.Next() {
		var idx int32
		if err := rows.Scan(&idx); err != nil {
			return nil, err
		}
		out = append(out, idx)
	}
	return out, rows.Err()
}

// --- ContactStore ---

func (s *Store) UpsertContact(ctx context.Context, c *model.Contact) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO contacts (owner_id, user_id, name, blocked, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (owner_id, user_id) DO UPDATE SET
		   name=EXCLUDED.name, updated_at=EXCLUDED.updated_at`,
		atoi(c.OwnerID), atoi(c.UserID), c.Name, c.Blocked, c.CreatedAt, c.UpdatedAt)
	return wrap(err)
}

func (s *Store) DeleteContact(ctx context.Context, ownerID, userID string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM contacts WHERE owner_id=$1 AND user_id=$2`, atoi(ownerID), atoi(userID))
	return err
}

func (s *Store) GetContact(ctx context.Context, ownerID, userID string) (*model.Contact, error) {
	var (
		c        model.Contact
		oid, uid int64
	)
	err := s.reader().QueryRow(ctx,
		`SELECT owner_id, user_id, name, blocked, created_at, updated_at
		 FROM contacts WHERE owner_id=$1 AND user_id=$2`, atoi(ownerID), atoi(userID)).
		Scan(&oid, &uid, &c.Name, &c.Blocked, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.OwnerID, c.UserID = itoa(oid), itoa(uid)
	return &c, nil
}

func (s *Store) ListContacts(ctx context.Context, ownerID string, since int64, limit int) ([]*model.Contact, error) {
	rows, err := s.reader().Query(ctx,
		`SELECT owner_id, user_id, name, blocked, created_at, updated_at
		 FROM contacts WHERE owner_id=$1 AND updated_at > $2 ORDER BY updated_at LIMIT $3`,
		atoi(ownerID), since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Contact
	for rows.Next() {
		var (
			c        model.Contact
			oid, uid int64
		)
		if err := rows.Scan(&oid, &uid, &c.Name, &c.Blocked, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.OwnerID, c.UserID = itoa(oid), itoa(uid)
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (s *Store) SetBlocked(ctx context.Context, ownerID, userID string, blocked bool, at int64) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO contacts (owner_id, user_id, name, blocked, created_at, updated_at)
		 VALUES ($1,$2,'',$3,$4,$4)
		 ON CONFLICT (owner_id, user_id) DO UPDATE SET blocked=EXCLUDED.blocked, updated_at=EXCLUDED.updated_at`,
		atoi(ownerID), atoi(userID), blocked, at)
	return err
}

func (s *Store) IsBlocked(ctx context.Context, ownerID, userID string) (bool, error) {
	var blocked bool
	err := s.reader().QueryRow(ctx,
		`SELECT blocked FROM contacts WHERE owner_id=$1 AND user_id=$2`, atoi(ownerID), atoi(userID)).
		Scan(&blocked)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return blocked, err
}

// fwdChat/fwdMsg/fwdSender flatten the optional forward origin into columns.
func fwdChat(m *model.Message) int64 {
	if m.Forward == nil {
		return 0
	}
	return atoi(m.Forward.ChatID)
}
func fwdMsg(m *model.Message) int64 {
	if m.Forward == nil {
		return 0
	}
	return atoi(m.Forward.MessageID)
}
func fwdSender(m *model.Message) int64 {
	if m.Forward == nil {
		return 0
	}
	return atoi(m.Forward.SenderID)
}

// --- self-destruct reaper ---

// AvatarRefExists reports whether a blob is somebody's profile picture
// (AvatarRefFinder).
func (s *Store) AvatarRefExists(ctx context.Context, ref string) (bool, error) {
	if ref == "" {
		return false, nil
	}
	var one int
	err := s.reader().QueryRow(ctx,
		`SELECT 1 FROM users WHERE avatar_ref = $1 LIMIT 1`, ref).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// MediaRefChats reports the chats a blob is reachable from (MediaChatResolver).
//
// DISTINCT because a forward chain can put the same ref in one chat many times,
// and the caller only needs the set. LIMIT because a widely forwarded blob does
// not need every row to answer one membership question.
func (s *Store) MediaRefChats(ctx context.Context, ref string, limit int) ([]string, error) {
	if ref == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 32
	}
	rows, err := s.reader().Query(ctx,
		`SELECT DISTINCT chat_id FROM messages
		 WHERE deleted = FALSE
		   AND (media_ref = $1 OR attachment->>'media_ref' = $1 OR attachment->>'thumb_ref' = $1)
		 LIMIT $2`, ref, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var chatID string
		if err := rows.Scan(&chatID); err != nil {
			return nil, err
		}
		out = append(out, chatID)
	}
	return out, rows.Err()
}

func (s *Store) MediaRefExists(ctx context.Context, ref string) (bool, error) {
	if ref == "" {
		return false, nil
	}
	var one int
	err := s.reader().QueryRow(ctx,
		`SELECT 1 FROM messages
		 WHERE deleted = FALSE
		   AND (media_ref = $1 OR attachment->>'media_ref' = $1 OR attachment->>'thumb_ref' = $1)
		 LIMIT 1`, ref).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) ExpireMessages(ctx context.Context, now int64, limit int) ([]*model.Message, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx,
		`UPDATE messages SET deleted=TRUE, text='', media_ref='', attachment=NULL, edited_at=$1
		 WHERE id IN (
		   SELECT id FROM messages
		   WHERE expires_at > 0 AND expires_at <= $1 AND deleted = FALSE
		   ORDER BY expires_at LIMIT $2
		   FOR UPDATE SKIP LOCKED
		 )
		 RETURNING `+msgCols, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Message
	for rows.Next() {
		m, err := scanMsg(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// --- ScheduleStore ---

func (s *Store) CreateScheduled(ctx context.Context, m *model.ScheduledMessage) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO scheduled_messages (id, chat_id, sender_id, text, media_ref, attachment, reply_to, ttl_secs, send_at, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		atoi(m.ID), atoi(m.ChatID), atoi(m.SenderID), m.Text, m.MediaRef,
		attachJSON(m.Attachment), atoi(m.ReplyTo), m.TTLSeconds, m.SendAt, m.CreatedAt)
	return wrap(err)
}

func (s *Store) CancelScheduled(ctx context.Context, id, senderID string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM scheduled_messages WHERE id=$1 AND sender_id=$2 AND sent=FALSE`,
		atoi(id), atoi(senderID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) ListScheduled(ctx context.Context, senderID, chatID string) ([]*model.ScheduledMessage, error) {
	rows, err := s.reader().Query(ctx,
		`SELECT id, chat_id, sender_id, text, media_ref, attachment, reply_to, ttl_secs, send_at, created_at
		 FROM scheduled_messages WHERE sender_id=$1 AND chat_id=$2 AND sent=FALSE ORDER BY send_at`,
		atoi(senderID), atoi(chatID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanScheduled(rows)
}

// PurgeSentScheduled drops fired rows past their retention window. A pending
// send becomes a real message the moment it fires, so keeping the pending row
// afterwards stores the same content twice.
func (s *Store) PurgeSentScheduled(ctx context.Context, before int64, limit int) (int, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM scheduled_messages WHERE id IN (
		   SELECT id FROM scheduled_messages WHERE sent = TRUE AND send_at < $1 ORDER BY id LIMIT $2
		 )`, before, limit)
	if err != nil {
		return 0, wrap(err)
	}
	return int(tag.RowsAffected()), nil
}

func (s *Store) ClaimDueScheduled(ctx context.Context, now int64, limit int) ([]*model.ScheduledMessage, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx,
		`UPDATE scheduled_messages SET sent=TRUE
		 WHERE id IN (
		   SELECT id FROM scheduled_messages WHERE sent=FALSE AND send_at <= $1
		   ORDER BY send_at LIMIT $2 FOR UPDATE SKIP LOCKED
		 )
		 RETURNING id, chat_id, sender_id, text, media_ref, attachment, reply_to, ttl_secs, send_at, created_at`,
		now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanScheduled(rows)
}

func scanScheduled(rows pgx.Rows) ([]*model.ScheduledMessage, error) {
	var out []*model.ScheduledMessage
	for rows.Next() {
		var (
			m                   model.ScheduledMessage
			id, cid, sid, reply int64
			attach              []byte
		)
		if err := rows.Scan(&id, &cid, &sid, &m.Text, &m.MediaRef, &attach, &reply,
			&m.TTLSeconds, &m.SendAt, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.ID, m.ChatID, m.SenderID = itoa(id), itoa(cid), itoa(sid)
		if reply != 0 {
			m.ReplyTo = itoa(reply)
		}
		if len(attach) > 0 {
			var a model.Attachment
			if err := json.Unmarshal(attach, &a); err == nil {
				m.Attachment = &a
			}
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// --- PinStore ---

func (s *Store) Pin(ctx context.Context, p *model.PinnedMessage) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO pinned_messages (chat_id, message_id, pinned_by, pinned_at)
		 VALUES ($1,$2,$3,$4)
		 ON CONFLICT (chat_id, message_id) DO UPDATE SET pinned_by=EXCLUDED.pinned_by, pinned_at=EXCLUDED.pinned_at`,
		atoi(p.ChatID), atoi(p.MessageID), atoi(p.PinnedBy), p.PinnedAt)
	return wrap(err)
}

func (s *Store) Unpin(ctx context.Context, chatID, messageID string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM pinned_messages WHERE chat_id=$1 AND message_id=$2`, atoi(chatID), atoi(messageID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) ListPins(ctx context.Context, chatID string) ([]*model.PinnedMessage, error) {
	rows, err := s.reader().Query(ctx,
		`SELECT chat_id, message_id, pinned_by, pinned_at FROM pinned_messages
		 WHERE chat_id=$1 ORDER BY pinned_at DESC`, atoi(chatID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.PinnedMessage
	for rows.Next() {
		var p model.PinnedMessage
		var cid, mid, by int64
		if err := rows.Scan(&cid, &mid, &by, &p.PinnedAt); err != nil {
			return nil, err
		}
		p.ChatID, p.MessageID, p.PinnedBy = itoa(cid), itoa(mid), itoa(by)
		out = append(out, &p)
	}
	return out, rows.Err()
}

// --- DraftStore ---

func (s *Store) SetDraft(ctx context.Context, d *model.Draft) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO drafts (user_id, chat_id, text, reply_to, updated_at)
		 VALUES ($1,$2,$3,$4,$5)
		 ON CONFLICT (user_id, chat_id) DO UPDATE SET
		   text=EXCLUDED.text, reply_to=EXCLUDED.reply_to, updated_at=EXCLUDED.updated_at`,
		atoi(d.UserID), atoi(d.ChatID), d.Text, atoi(d.ReplyTo), d.UpdatedAt)
	return wrap(err)
}

func (s *Store) DeleteDraft(ctx context.Context, userID, chatID string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM drafts WHERE user_id=$1 AND chat_id=$2`, atoi(userID), atoi(chatID))
	return err
}

func (s *Store) ListDrafts(ctx context.Context, userID string, since int64, limit int) ([]*model.Draft, error) {
	rows, err := s.reader().Query(ctx,
		`SELECT user_id, chat_id, text, reply_to, updated_at FROM drafts
		 WHERE user_id=$1 AND updated_at > $2 ORDER BY updated_at LIMIT $3`, atoi(userID), since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Draft
	for rows.Next() {
		var d model.Draft
		var uid, cid, reply int64
		if err := rows.Scan(&uid, &cid, &d.Text, &reply, &d.UpdatedAt); err != nil {
			return nil, err
		}
		d.UserID, d.ChatID = itoa(uid), itoa(cid)
		if reply != 0 {
			d.ReplyTo = itoa(reply)
		}
		out = append(out, &d)
	}
	return out, rows.Err()
}

// --- InviteStore ---

// SetChatUsername claims a public handle. The unique index is case-insensitive,
// so "News" cannot shadow "news" — handles would otherwise be a phishing surface.
func (s *Store) SetChatUsername(ctx context.Context, chatID, username string) error {
	_, err := s.pool.Exec(ctx, `UPDATE chats SET username=$2 WHERE id=$1`, atoi(chatID), username)
	return wrap(err)
}

func (s *Store) GetChatByUsername(ctx context.Context, username string) (*model.Chat, error) {
	var (
		c        model.Chat
		cid, oid int64
		typ      string
		lastSeq  int64
	)
	err := s.reader().QueryRow(ctx,
		`SELECT id, type, title, owner_id, created_at, last_seq, username
		 FROM chats WHERE LOWER(username)=LOWER($1) AND username <> ''`, username).
		Scan(&cid, &typ, &c.Title, &oid, &c.CreatedAt, &lastSeq, &c.Username)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.ID, c.Type, c.OwnerID, c.LastSeq = itoa(cid), model.ChatType(typ), itoa(oid), uint64(lastSeq)
	return &c, nil
}

func (s *Store) CreateInvite(ctx context.Context, l *model.InviteLink) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO invite_links (code, chat_id, created_by, created_at, expires_at, max_uses)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		l.Code, atoi(l.ChatID), atoi(l.CreatedBy), l.CreatedAt, l.ExpiresAt, l.MaxUses)
	return wrap(err)
}

func (s *Store) GetInvite(ctx context.Context, code string) (*model.InviteLink, error) {
	var (
		l        model.InviteLink
		cid, cby int64
	)
	err := s.reader().QueryRow(ctx,
		`SELECT code, chat_id, created_by, created_at, expires_at, max_uses, uses, revoked
		 FROM invite_links WHERE code=$1`, code).
		Scan(&l.Code, &cid, &cby, &l.CreatedAt, &l.ExpiresAt, &l.MaxUses, &l.Uses, &l.Revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	l.ChatID, l.CreatedBy = itoa(cid), itoa(cby)
	return &l, nil
}

func (s *Store) RevokeInvite(ctx context.Context, code, chatID string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE invite_links SET revoked=TRUE WHERE code=$1 AND chat_id=$2`, code, atoi(chatID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) ListInvites(ctx context.Context, chatID string) ([]*model.InviteLink, error) {
	rows, err := s.reader().Query(ctx,
		`SELECT code, chat_id, created_by, created_at, expires_at, max_uses, uses, revoked
		 FROM invite_links WHERE chat_id=$1 AND revoked=FALSE ORDER BY created_at DESC`, atoi(chatID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.InviteLink
	for rows.Next() {
		var (
			l        model.InviteLink
			cid, cby int64
		)
		if err := rows.Scan(&l.Code, &cid, &cby, &l.CreatedAt, &l.ExpiresAt, &l.MaxUses, &l.Uses, &l.Revoked); err != nil {
			return nil, err
		}
		l.ChatID, l.CreatedBy = itoa(cid), itoa(cby)
		out = append(out, &l)
	}
	return out, rows.Err()
}

// UseInvite redeems a link ATOMICALLY: the validity checks live in the UPDATE's
// WHERE clause, so a link capped at N uses cannot be over-redeemed by concurrent
// joins (a check-then-update would race).
func (s *Store) UseInvite(ctx context.Context, code string, now int64) (*model.InviteLink, error) {
	var (
		l        model.InviteLink
		cid, cby int64
	)
	err := s.pool.QueryRow(ctx,
		`UPDATE invite_links SET uses = uses + 1
		 WHERE code = $1
		   AND revoked = FALSE
		   AND (expires_at = 0 OR expires_at > $2)
		   AND (max_uses  = 0 OR uses < max_uses)
		 RETURNING code, chat_id, created_by, created_at, expires_at, max_uses, uses, revoked`,
		code, now).
		Scan(&l.Code, &cid, &cby, &l.CreatedAt, &l.ExpiresAt, &l.MaxUses, &l.Uses, &l.Revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound // missing, revoked, expired, or exhausted
	}
	if err != nil {
		return nil, err
	}
	l.ChatID, l.CreatedBy = itoa(cid), itoa(cby)
	return &l, nil
}

// SetMemberRole promotes/demotes a member (MemberRoleStore).
func (s *Store) SetMemberRole(ctx context.Context, chatID, userID string, role model.MemberRole) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE chat_members SET role=$3 WHERE chat_id=$1 AND user_id=$2`,
		atoi(chatID), atoi(userID), string(role))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// --- SecretQueueStore ---

// EnqueueSecret stores one undelivered E2E envelope and trims the device's queue
// to maxPerDevice in the same round trip.
//
// The trim is part of the insert rather than a separate sweep because the cap is
// a safety property, not housekeeping: the device id is client-asserted, so
// anyone can address envelopes at any (user, device) pair that exists, and a
// queue that is only trimmed on a timer is unbounded between ticks. Deleting
// from the FRONT is deliberate — the oldest undelivered ciphertext is the one
// whose ratchet session is least likely to still exist on either side.
func (s *Store) EnqueueSecret(ctx context.Context, e *model.SecretEnvelope, maxPerDevice int) error {
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO secret_queue
		   (id, to_user_id, to_device_id, from_user_id, from_device_id, header, ciphertext, created_at, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		atoi(e.ID), atoi(e.ToUserID), e.ToDeviceID, atoi(e.FromUserID), e.FromDeviceID,
		e.Header, e.Ciphertext, e.CreatedAt, e.ExpiresAt); err != nil {
		return wrap(err)
	}
	if maxPerDevice <= 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx,
		`DELETE FROM secret_queue WHERE id IN (
		   SELECT id FROM secret_queue
		   WHERE to_user_id=$1 AND to_device_id=$2
		   ORDER BY id DESC OFFSET $3
		 )`, atoi(e.ToUserID), e.ToDeviceID, maxPerDevice)
	return wrap(err)
}

// PendingSecrets walks a device's queue oldest-first by keyset.
//
// Oldest first, and not for tidiness: ratchet messages decrypt in order far more
// cheaply than out of it. Handing the newest one over first would make the
// receiver derive and retain a skipped message key for every envelope behind it,
// which is the work the skipped-key cap exists to bound.
func (s *Store) PendingSecrets(ctx context.Context, toUserID, toDeviceID, afterID string, limit int) ([]*model.SecretEnvelope, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.reader().Query(ctx,
		`SELECT id, to_user_id, to_device_id, from_user_id, from_device_id,
		        header, ciphertext, created_at, expires_at
		 FROM secret_queue
		 WHERE to_user_id=$1 AND to_device_id=$2 AND id > $3
		 ORDER BY id LIMIT $4`,
		atoi(toUserID), toDeviceID, atoi(afterID), limit)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	var out []*model.SecretEnvelope
	for rows.Next() {
		var (
			id, toUID, fromUID int64
			e                  model.SecretEnvelope
		)
		if err := rows.Scan(&id, &toUID, &e.ToDeviceID, &fromUID, &e.FromDeviceID,
			&e.Header, &e.Ciphertext, &e.CreatedAt, &e.ExpiresAt); err != nil {
			return nil, err
		}
		e.ID, e.ToUserID, e.FromUserID = itoa(id), itoa(toUID), itoa(fromUID)
		out = append(out, &e)
	}
	return out, rows.Err()
}

// AckSecrets drops envelopes a device confirmed it has stored.
//
// The (to_user_id, to_device_id) predicate is the authorization, not a filter:
// an envelope id travels to the client in the delivery, so a delete scoped only
// by id would let any authenticated account discard another account's
// undelivered mail by naming ids it had seen — or guessed, since they are
// snowflakes.
func (s *Store) AckSecrets(ctx context.Context, toUserID, toDeviceID string, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	nums := make([]int64, 0, len(ids))
	for _, id := range ids {
		nums = append(nums, atoi(id))
	}
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM secret_queue WHERE to_user_id=$1 AND to_device_id=$2 AND id = ANY($3)`,
		atoi(toUserID), toDeviceID, nums)
	if err != nil {
		return 0, wrap(err)
	}
	return int(tag.RowsAffected()), nil
}

// PurgeExpiredSecrets collects envelopes nobody came back for.
//
// This runs whether or not the queue is under pressure. The rows are metadata
// about who messaged whom and when — which the stateless relay never wrote down
// — so keeping them past the point where the recipient could still use them
// trades a delivery guarantee nobody gains for a record of a conversation.
func (s *Store) PurgeExpiredSecrets(ctx context.Context, now int64, limit int) (int, error) {
	if limit <= 0 {
		limit = 1000
	}
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM secret_queue WHERE id IN (
		   SELECT id FROM secret_queue WHERE expires_at <= $1 ORDER BY expires_at LIMIT $2
		 )`, now, limit)
	if err != nil {
		return 0, wrap(err)
	}
	return int(tag.RowsAffected()), nil
}

// --- ChatSummaryReader / MemberFlagStore ---

/*
UserChatSummaries builds a page of the chat list in ONE query.

What it replaces, in chat.Service.UserChats: read every chat id the user belongs
to, sort them in Go, then per surviving row issue GetChat, GetMember and — for a
direct chat — ListMembers to find the peer. A 50-row page cost up to 150 round
trips, and because the sort happened after a full read, every page re-read the
entire membership: paging to the end of a 2000-chat list was quadratic.

It also fixes what the old page CONTAINED. There was no last message, no unread
count and no mute state, so a client had to call HISTORY per chat to draw its
list — the server's N+1 moved to the client and became N+1 over the network. And
the order was by chat id, which is creation order; a chat list is ordered by
activity, and that is not a presentation detail but what the list IS.

The LATERAL is why this is an optional capability rather than part of ChatStore:
it reads `messages`, which a sharded deployment does not co-locate with the chat
metadata. Where they are co-located (the single-Postgres default) this is one
query; where they are not, the capability is simply absent and the caller keeps
its row-at-a-time path.
*/
func (s *Store) UserChatSummaries(ctx context.Context, userID string, afterActivity int64, afterChatID string, limit int, includeArchived bool) ([]model.ChatSummary, error) {
	if limit <= 0 {
		limit = 50
	}
	// The cursor parameters are cast explicitly: `$2 = 0` alone makes Postgres
	// infer int4 from the literal, and the first page cursor holding a real
	// snowflake id (which needs 64 bits) then fails to encode.
	rows, err := s.reader().Query(ctx,
		`WITH mine AS (
		   SELECT m.chat_id, m.role, m.muted_until, m.pinned, m.archived
		   FROM chat_members m
		   WHERE m.user_id = $1 AND ($5 OR NOT m.archived)
		 )
		 SELECT c.id, c.type, c.title, c.owner_id, c.created_at, c.last_seq, c.username,
		        mine.role, mine.muted_until, mine.pinned, mine.archived,
		        lm.id, lm.sender_id, lm.seq, lm.text, lm.media_ref, lm.created_at,
		        COALESCE(lm.seq, 0) - LEAST(COALESCE(rs.up_to_seq, 0), COALESCE(lm.seq, 0)) AS unread,
		        peer.user_id,
		        COALESCE(lm.created_at, c.created_at) AS activity_at
		 FROM mine
		 JOIN chats c ON c.id = mine.chat_id
		 LEFT JOIN read_state rs ON rs.chat_id = c.id AND rs.user_id = $1
		 LEFT JOIN LATERAL (
		   SELECT id, sender_id, seq, text, media_ref, created_at
		   FROM messages
		   WHERE chat_id = c.id AND NOT deleted
		   ORDER BY seq DESC LIMIT 1
		 ) lm ON TRUE
		 LEFT JOIN LATERAL (
		   SELECT user_id FROM chat_members
		   WHERE chat_id = c.id AND user_id <> $1 LIMIT 1
		 ) peer ON c.type IN ('direct', 'secret')
		 WHERE ($2::bigint = 0 AND $3::bigint = 0)
		    OR (COALESCE(lm.created_at, c.created_at), c.id) < ($2::bigint, $3::bigint)
		 ORDER BY activity_at DESC, c.id DESC
		 LIMIT $4`,
		atoi(userID), afterActivity, atoi(afterChatID), limit, includeArchived)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()

	var out []model.ChatSummary
	for rows.Next() {
		var (
			id, ownerID, createdAt, lastSeq int64
			typ, title, username, role      string
			mutedUntil                      int64
			pinned, archived                bool
			// The last message is absent for an empty chat, so every column of it
			// is nullable and scanned into a pointer.
			lmID, lmSender, lmSeq, lmCreated *int64
			lmText, lmMedia                  *string
			unread                           int64
			peerID                           *int64
			activityAt                       int64
		)
		if err := rows.Scan(&id, &typ, &title, &ownerID, &createdAt, &lastSeq, &username,
			&role, &mutedUntil, &pinned, &archived,
			&lmID, &lmSender, &lmSeq, &lmText, &lmMedia, &lmCreated,
			&unread, &peerID, &activityAt); err != nil {
			return nil, err
		}
		sum := model.ChatSummary{
			Chat: &model.Chat{
				ID: itoa(id), Type: model.ChatType(typ), Title: title,
				OwnerID: itoa(ownerID), CreatedAt: createdAt,
				LastSeq: uint64(lastSeq), Username: username,
			},
			MyRole:         model.MemberRole(role),
			UnreadCount:    unread,
			LastActivityAt: activityAt,
			Flags: model.MemberFlags{
				MutedUntil: mutedUntil, Pinned: pinned, Archived: archived,
			},
		}
		if peerID != nil {
			sum.PeerID = itoa(*peerID)
		}
		if lmID != nil {
			sum.LastMessage = &model.Message{
				ID: itoa(*lmID), ChatID: sum.Chat.ID, SenderID: itoa(deref(lmSender)),
				Seq: uint64(deref(lmSeq)), Text: derefStr(lmText), MediaRef: derefStr(lmMedia),
				CreatedAt: deref(lmCreated),
			}
		}
		out = append(out, sum)
	}
	return out, rows.Err()
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// CountPinnedChatsExcept counts this user's pinned chats other than exceptChatID.
//
// Counted in the database rather than by reading rows back, because the answer is
// a number and the caller only wants to know whether there is room for one more.
func (s *Store) CountPinnedChatsExcept(ctx context.Context, userID, exceptChatID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM chat_members WHERE user_id=$1 AND pinned AND chat_id <> $2`,
		atoi(userID), atoi(exceptChatID)).Scan(&n)
	if err != nil {
		return 0, wrap(err)
	}
	return n, nil
}

// SetMemberFlags writes one member's private per-chat settings.
//
// The (chat_id, user_id) predicate is the authorization: flags are the member's
// own opinion about their own list, so there is no shape of this call that touches
// somebody else's row. It also means a non-member's write affects nothing rather
// than creating a membership.
func (s *Store) SetMemberFlags(ctx context.Context, chatID, userID string, f model.MemberFlags) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE chat_members
		 SET muted_until=$3::bigint, pinned=$4, archived=$5, muted=($3::bigint <> 0)
		 WHERE chat_id=$1 AND user_id=$2`,
		atoi(chatID), atoi(userID), f.MutedUntil, f.Pinned, f.Archived)
	if err != nil {
		return wrap(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) GetMemberFlags(ctx context.Context, chatID, userID string) (model.MemberFlags, error) {
	var f model.MemberFlags
	err := s.reader().QueryRow(ctx,
		`SELECT muted_until, pinned, archived FROM chat_members WHERE chat_id=$1 AND user_id=$2`,
		atoi(chatID), atoi(userID)).Scan(&f.MutedUntil, &f.Pinned, &f.Archived)
	if err != nil {
		return model.MemberFlags{}, wrap(err)
	}
	return f, nil
}

// --- TwoFactorStore ---

// PutTwoFactor writes an enrolment and its recovery codes in ONE transaction.
//
// One transaction because the two halves are useless apart: an enrolment without
// recovery codes locks a user out the moment they lose their phone, and codes
// without an enrolment are a set of credentials for a factor that does not exist.
// A partial write is the worst of both.
func (s *Store) PutTwoFactor(ctx context.Context, tf *model.TwoFactor) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return wrap(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`INSERT INTO user_two_factor (user_id, secret_enc, confirmed_at, created_at)
		 VALUES ($1,$2,$3,$4)
		 ON CONFLICT (user_id) DO UPDATE
		   SET secret_enc=EXCLUDED.secret_enc, confirmed_at=EXCLUDED.confirmed_at`,
		atoi(tf.UserID), tf.SecretEnc, tf.ConfirmedAt, tf.CreatedAt); err != nil {
		return wrap(err)
	}
	// Recovery codes are REPLACED wholesale, never merged. They are issued as a
	// set, and a client that re-enrols must not end up able to use codes from a
	// previous secret.
	if _, err := tx.Exec(ctx, `DELETE FROM user_recovery_codes WHERE user_id=$1`, atoi(tf.UserID)); err != nil {
		return wrap(err)
	}
	for _, h := range tf.RecoveryHashes {
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_recovery_codes (user_id, code_hash) VALUES ($1,$2)
			 ON CONFLICT DO NOTHING`, atoi(tf.UserID), h); err != nil {
			return wrap(err)
		}
	}
	return wrap(tx.Commit(ctx))
}

func (s *Store) GetTwoFactor(ctx context.Context, userID string) (*model.TwoFactor, error) {
	var tf model.TwoFactor
	err := s.reader().QueryRow(ctx,
		`SELECT secret_enc, confirmed_at, created_at FROM user_two_factor WHERE user_id=$1`,
		atoi(userID)).Scan(&tf.SecretEnc, &tf.ConfirmedAt, &tf.CreatedAt)
	if err != nil {
		return nil, wrap(err)
	}
	tf.UserID = userID

	rows, err := s.reader().Query(ctx,
		`SELECT code_hash FROM user_recovery_codes WHERE user_id=$1`, atoi(userID))
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		tf.RecoveryHashes = append(tf.RecoveryHashes, h)
	}
	return &tf, rows.Err()
}

func (s *Store) DeleteTwoFactor(ctx context.Context, userID string) error {
	// The codes go with it. ON DELETE CASCADE covers the user row disappearing,
	// but disabling the factor is not deleting the account, so this is explicit.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return wrap(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM user_recovery_codes WHERE user_id=$1`, atoi(userID)); err != nil {
		return wrap(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_two_factor WHERE user_id=$1`, atoi(userID)); err != nil {
		return wrap(err)
	}
	return wrap(tx.Commit(ctx))
}

// ConsumeRecoveryCode spends one code, atomically.
//
// A single DELETE, because a recovery code is single-use and check-then-write
// would let two concurrent logins both spend the same one — exactly the race an
// attacker replaying an observed code would want. RowsAffected is the answer.
func (s *Store) ConsumeRecoveryCode(ctx context.Context, userID, hash string) (bool, error) {
	ct, err := s.pool.Exec(ctx,
		`DELETE FROM user_recovery_codes WHERE user_id=$1 AND code_hash=$2`,
		atoi(userID), hash)
	if err != nil {
		return false, wrap(err)
	}
	return ct.RowsAffected() > 0, nil
}

// --- PasswordStore ---

// SetPasswordHash replaces an account's password hash.
//
// Until this existed a password could not be changed at all, which made a leaked
// one permanent: revoking every session does not stop whoever knows the password
// from signing in again a minute later.
func (s *Store) SetPasswordHash(ctx context.Context, userID, hash string) error {
	ct, err := s.pool.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1`, atoi(userID), hash)
	if err != nil {
		return wrap(err)
	}
	if ct.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}
