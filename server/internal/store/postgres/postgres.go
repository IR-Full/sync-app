// Package postgres implements the store interfaces on PostgreSQL via pgx. This
// is the production persistence for the MVP ("Postgres-only early stage" from
// Section 8). The message table is designed so it can later be lifted into a
// wide-column store without changing the MessageStore contract.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/SyncApp-chat/SyncApp/internal/envcfg"
	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// reader returns the pool for lag-tolerant reads: the replica if attached, else
// the primary.
func (s *Store) reader() *pgxpool.Pool {
	if s.readPool != nil {
		return s.readPool
	}
	return s.pool
}

// Connect opens a pool and pings it.
func Connect(ctx context.Context, dsn string) (*Store, error) {
	// pgx defaults to max(4, NumCPU) connections — far too small for a write-heavy
	// gateway, where it becomes the throughput ceiling under load. connectPool
	// sizes it for concurrency (override via ?pool_max_conns=N in the DSN).
	pool, err := connectPool(ctx, dsn)
	if err != nil {
		return nil, err
	}
	s := &Store{pool: pool, dsn: dsn}
	// Optional read replica: history/read-receipt queries go here to take read
	// load off the primary (Section 8 "read replicas"). Point it at a streaming
	// replica; a small lag is fine for these paths (live delivery + SEND_ACK cover
	// the just-sent case, so a client never depends on the replica for its own
	// latest write). If the replica is unreachable at boot we fail closed on it and
	// fall back to the primary rather than refuse to start.
	if rdsn := envcfg.Get("SYNCAPP_PG_REPLICA_DSN"); rdsn != "" {
		if rp, err := connectPool(ctx, rdsn); err != nil {
			// Non-fatal: log-less fallback to primary keeps the node serving.
			s.readPool = nil
		} else {
			s.readPool = rp
		}
	}
	// Group-commit batcher: coalesces concurrent message writes into shared
	// transactions so many messages amortize one commit fsync (the measured
	// single-node write bottleneck). Disable with SYNCAPP_WRITE_BATCH=off.
	if envcfg.Get("SYNCAPP_WRITE_BATCH") != "off" {
		s.batcher = newBatcher(s)
	}
	return s, nil
}

// connectPool opens and pings a sized pgx pool (shared by primary + replica).
func connectPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if cfg.MaxConns < 50 {
		cfg.MaxConns = 50
	}
	cfg.MinConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// Close releases the pools and stops the batcher.
func (s *Store) Close() {
	if s.batcher != nil {
		s.batcher.stop()
	}
	if s.readPool != nil {
		s.readPool.Close()
	}
	s.pool.Close()
}

// Stores returns a store.Stores bundle backed by this instance.
func (s *Store) Stores() store.Stores {
	return store.Stores{Users: s, Sessions: s, Chats: s, Messages: s, Reads: s, Reactions: s, Calls: s, Polls: s, Contacts: s, Schedule: s, Pins: s, Drafts: s, Invites: s, Outbox: s, SecretQ: s, TwoFactor: s, Billing: s}
}

// atoi/itoa convert between the model's string ids and BIGINT columns.
func atoi(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }
func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// --- UserStore ---

func (s *Store) CreateUser(ctx context.Context, u *model.User) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO users (id, username, display_name, avatar_ref, password_hash, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		atoi(u.ID), u.Username, u.DisplayName, u.AvatarRef, u.PasswordHash, u.CreatedAt)
	return wrap(err)
}

// UpdateProfile writes the public, mutable fields of an account.
func (s *Store) UpdateProfile(ctx context.Context, userID, displayName, avatarRef string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE users SET display_name=$2, avatar_ref=$3 WHERE id=$1`,
		atoi(userID), displayName, avatarRef)
	if err != nil {
		return wrap(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// DeleteAccount erases an account. Everything runs in ONE transaction: a
// half-erased account — say, sessions gone but the users row still present — is
// worse than either outcome, because the owner can no longer log in to ask for
// it again.
//
// Order matters twice over. devices and sessions carry a foreign key to
// users(id), so they go first. Messages are anonymised rather than deleted (see
// store.UserStore.DeleteAccount); dedup_key is cleared with them, since the
// partial unique index on (sender_id, dedup_key) would otherwise collide the
// moment a second account is erased with a colliding key under the shared
// sender id 0.
func (s *Store) DeleteAccount(ctx context.Context, userID string) error {
	uid := atoi(userID)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return wrap(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	// Ownership check first: without it a bad id silently "succeeds" and the
	// caller reports a deletion that never happened.
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT TRUE FROM users WHERE id=$1 FOR UPDATE`, uid).Scan(&exists); err != nil {
		return store.ErrNotFound
	}

	stmts := []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM sessions WHERE user_id=$1`, []any{uid}},
		{`DELETE FROM devices WHERE user_id=$1`, []any{uid}},
		// Both directions: entries this account made, and entries other people
		// made ABOUT it (including blocks, which must not outlive their subject).
		{`DELETE FROM contacts WHERE owner_id=$1 OR user_id=$1`, []any{uid}},
		{`DELETE FROM drafts WHERE user_id=$1`, []any{uid}},
		{`DELETE FROM read_state WHERE user_id=$1`, []any{uid}},
		{`DELETE FROM reactions WHERE user_id=$1`, []any{uid}},
		{`DELETE FROM poll_votes WHERE user_id=$1`, []any{uid}},
		{`DELETE FROM scheduled_messages WHERE sender_id=$1`, []any{uid}},
		{`DELETE FROM invite_links WHERE created_by=$1`, []any{uid}},
		{`DELETE FROM pinned_messages WHERE pinned_by=$1`, []any{uid}},
		{`DELETE FROM call_participants WHERE user_id=$1`, []any{uid}},
		{`UPDATE messages SET sender_id=0, text='', media_ref='', attachment=NULL,
		         dedup_key='', deleted=TRUE, edited_at=$2
		   WHERE sender_id=$1`, []any{uid, nowMs()}},
		// Forward provenance names the original sender; a forwarded copy that keeps
		// pointing at a deleted account re-identifies it from someone else's chat.
		{`UPDATE messages SET fwd_sender_id=0 WHERE fwd_sender_id=$1`, []any{uid}},
		{`DELETE FROM chat_members WHERE user_id=$1`, []any{uid}},
		{`DELETE FROM users WHERE id=$1`, []any{uid}},
	}
	for _, st := range stmts {
		if _, err := tx.Exec(ctx, st.sql, st.args...); err != nil {
			return wrap(err)
		}
	}
	return wrap(tx.Commit(ctx))
}

func (s *Store) GetUser(ctx context.Context, id string) (*model.User, error) {
	return s.scanUser(ctx, `WHERE id=$1`, atoi(id))
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*model.User, error) {
	return s.scanUser(ctx, `WHERE username=$1`, username)
}

func (s *Store) scanUser(ctx context.Context, where string, arg any) (*model.User, error) {
	var (
		u  model.User
		id int64
	)
	err := s.pool.QueryRow(ctx,
		`SELECT id, username, display_name, avatar_ref, password_hash, created_at,
		        privacy_last_seen, privacy_avatar, privacy_groups, privacy_push_preview
		   FROM users `+where, arg).
		Scan(&id, &u.Username, &u.DisplayName, &u.AvatarRef, &u.PasswordHash, &u.CreatedAt,
			&u.Privacy.LastSeen, &u.Privacy.Avatar, &u.Privacy.Groups, &u.Privacy.PushPreview)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.ID = itoa(id)
	// A row written before these columns existed, or by something else, reads as
	// the historical behaviour rather than as an error nobody can act on.
	u.Privacy = u.Privacy.Normalize()
	return &u, nil
}

// UpdatePrivacy writes a user's visibility settings.
func (s *Store) UpdatePrivacy(ctx context.Context, userID string, p model.Privacy) error {
	p = p.Normalize()
	ct, err := s.pool.Exec(ctx,
		`UPDATE users SET privacy_last_seen=$2, privacy_avatar=$3, privacy_groups=$4,
		     privacy_push_preview=$5 WHERE id=$1`,
		atoi(userID), string(p.LastSeen), string(p.Avatar), string(p.Groups), p.PushPreview)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// UpsertDevice registers or refreshes a device. A device id is asserted by the
// CLIENT (it arrives in HELLO), so the update is scoped to its owner: without
// the WHERE, anyone could name someone else's device id and rewrite that row —
// clearing the victim's push token, which is a quiet way to silence them. A
// conflicting id belonging to another account affects no rows and is reported as
// ErrConflict for the caller to route around.
//
// An empty push token means "not provided by this caller" (login does not carry
// one), never "clear it" — otherwise every re-login would unregister the device
// from push.
func (s *Store) UpsertDevice(ctx context.Context, d *model.Device) error {
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO devices (id, user_id, platform, push_token, created_at, last_seen)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (id) DO UPDATE SET platform=EXCLUDED.platform,
		   push_token=CASE WHEN EXCLUDED.push_token='' THEN devices.push_token ELSE EXCLUDED.push_token END,
		   last_seen=EXCLUDED.last_seen
		 WHERE devices.user_id=EXCLUDED.user_id`,
		atoi(d.ID), atoi(d.UserID), d.Platform, d.PushToken, d.CreatedAt, d.LastSeen)
	if err != nil {
		return wrap(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrConflict
	}
	return nil
}

// SetPushToken writes a device's push token, scoped to its owner.
func (s *Store) SetPushToken(ctx context.Context, userID, deviceID, token string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE devices SET push_token=$3 WHERE id=$2 AND user_id=$1`,
		atoi(userID), atoi(deviceID), token)
	if err != nil {
		return wrap(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) GetDevice(ctx context.Context, id string) (*model.Device, error) {
	var (
		d        model.Device
		did, uid int64
	)
	err := s.pool.QueryRow(ctx,
		`SELECT id, user_id, platform, push_token, created_at, last_seen FROM devices WHERE id=$1`, atoi(id)).
		Scan(&did, &uid, &d.Platform, &d.PushToken, &d.CreatedAt, &d.LastSeen)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.ID, d.UserID = itoa(did), itoa(uid)
	return &d, nil
}

func (s *Store) ListDevices(ctx context.Context, userID string) ([]*model.Device, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, platform, push_token, created_at, last_seen FROM devices WHERE user_id=$1`, atoi(userID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Device
	for rows.Next() {
		var d model.Device
		var did, uid int64
		if err := rows.Scan(&did, &uid, &d.Platform, &d.PushToken, &d.CreatedAt, &d.LastSeen); err != nil {
			return nil, err
		}
		d.ID, d.UserID = itoa(did), itoa(uid)
		out = append(out, &d)
	}
	return out, rows.Err()
}

// --- SessionStore ---

func (s *Store) CreateSession(ctx context.Context, sess *model.Session) error {
	var resume any
	if sess.ResumeToken != "" {
		resume = sess.ResumeToken
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO sessions (id, user_id, device_id, token, resume_token, created_at, expires_at, revoked_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		atoi(sess.ID), atoi(sess.UserID), atoi(sess.DeviceID), sess.Token, resume,
		sess.CreatedAt, sess.ExpiresAt, sess.RevokedAt)
	return wrap(err)
}

func (s *Store) GetSessionByToken(ctx context.Context, token string) (*model.Session, error) {
	return s.scanSession(ctx, `WHERE token=$1`, token)
}

func (s *Store) GetSessionByResumeToken(ctx context.Context, resume string) (*model.Session, error) {
	return s.scanSession(ctx, `WHERE resume_token=$1`, resume)
}

func (s *Store) scanSession(ctx context.Context, where string, arg any) (*model.Session, error) {
	var (
		sess         model.Session
		id, uid, did int64
		resume, prev *string
	)
	err := s.pool.QueryRow(ctx,
		`SELECT id, user_id, device_id, token, resume_token, created_at, expires_at, revoked_at,
		        prev_resume_token, resume_rotated_at
		 FROM sessions `+where, arg).
		Scan(&id, &uid, &did, &sess.Token, &resume, &sess.CreatedAt, &sess.ExpiresAt, &sess.RevokedAt,
			&prev, &sess.ResumeRotatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sess.ID, sess.UserID, sess.DeviceID = itoa(id), itoa(uid), itoa(did)
	if resume != nil {
		sess.ResumeToken = *resume
	}
	if prev != nil {
		sess.PrevResumeToken = *prev
	}
	return &sess, nil
}

// RotateResumeToken swaps the token, keeping the one it replaced.
//
// The `resume_token=$3` predicate is a compare-and-swap, and it is the whole
// safety property: two resumes racing on one token must not both succeed and hand
// out two live chains. The loser affects no rows and is told ErrNotFound, which is
// indistinguishable from a replay — and is correctly treated as one.
func (s *Store) RotateResumeToken(ctx context.Context, sessionID, oldHash, newHash string, at int64) error {
	ct, err := s.pool.Exec(ctx,
		`UPDATE sessions
		 SET prev_resume_token = resume_token, resume_token = $2, resume_rotated_at = $4
		 WHERE id = $1 AND resume_token = $3`,
		atoi(sessionID), newHash, oldHash, at)
	if err != nil {
		return wrap(err)
	}
	if ct.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// GetSessionByConsumedResumeToken finds the session a token USED to belong to.
//
// A hit means a resume arrived for a token that has already been spent, so two
// parties hold the chain — which is the theft signal the rotation exists to
// produce. ErrNotFound is the ordinary case of a token that was never valid.
func (s *Store) GetSessionByConsumedResumeToken(ctx context.Context, resume string) (*model.Session, error) {
	return s.scanSession(ctx, `WHERE prev_resume_token=$1`, resume)
}

func (s *Store) RevokeSession(ctx context.Context, id string, at int64) error {
	ct, err := s.pool.Exec(ctx, `UPDATE sessions SET revoked_at=$2 WHERE id=$1`, atoi(id), at)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// TouchSession extends a live session's expiry.
//
// The liveness conditions live in the WHERE clause rather than in a read
// followed by a write: two connections of the same account can touch the same
// session concurrently, and a check-then-update would let one of them revive a
// session the other just revoked. `expires_at < $2` also makes the statement
// idempotent and keeps it from writing when nothing would change.
//
// A row that does not match is not an error — it means the session is revoked,
// expired, or already fresh enough, all of which are fine.
func (s *Store) TouchSession(ctx context.Context, id string, expiresAt int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE sessions SET expires_at=$2
		 WHERE id=$1 AND revoked_at=0 AND expires_at > $3 AND expires_at < $2`,
		atoi(id), expiresAt, nowMs())
	return err
}

func (s *Store) ListSessions(ctx context.Context, userID string) ([]*model.Session, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, device_id, token, resume_token, created_at, expires_at, revoked_at
		 FROM sessions WHERE user_id=$1`, atoi(userID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Session
	for rows.Next() {
		var sess model.Session
		var id, uid, did int64
		var resume *string
		if err := rows.Scan(&id, &uid, &did, &sess.Token, &resume, &sess.CreatedAt, &sess.ExpiresAt, &sess.RevokedAt); err != nil {
			return nil, err
		}
		sess.ID, sess.UserID, sess.DeviceID = itoa(id), itoa(uid), itoa(did)
		if resume != nil {
			sess.ResumeToken = *resume
		}
		out = append(out, &sess)
	}
	return out, rows.Err()
}

// --- ChatStore ---

func (s *Store) CreateChat(ctx context.Context, c *model.Chat) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO chats (id, type, title, owner_id, created_at, last_seq)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		atoi(c.ID), string(c.Type), c.Title, atoi(c.OwnerID), c.CreatedAt, int64(c.LastSeq))
	return wrap(err)
}

func (s *Store) GetChat(ctx context.Context, id string) (*model.Chat, error) {
	var (
		c        model.Chat
		cid, oid int64
		typ      string
		lastSeq  int64
	)
	err := s.pool.QueryRow(ctx,
		`SELECT id, type, title, owner_id, created_at, last_seq, username FROM chats WHERE id=$1`, atoi(id)).
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

func (s *Store) GetOrCreateDirect(ctx context.Context, userA, userB, newID string) (*model.Chat, error) {
	return s.GetOrCreatePair(ctx, model.ChatDirect, userA, userB, newID)
}

// GetOrCreatePair is the canonical-pair lookup for any two-party chat type.
//
// The type is folded into the pair key rather than filtered on afterwards, because
// a direct chat and a secret chat with the same person are DIFFERENT
// conversations that must both be reachable. Filtering would make one of them
// unfindable, and keying only on the pair would make the second one collide with
// the first.
func (s *Store) GetOrCreatePair(ctx context.Context, typ model.ChatType, userA, userB, newID string) (*model.Chat, error) {
	key := pairKey(typ, userA, userB)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	// Rollback after a successful Commit is a no-op that returns
	// ErrTxClosed; there is nothing to do with it either way.
	defer func() { _ = tx.Rollback(ctx) }()

	var chatID int64
	err = tx.QueryRow(ctx, `SELECT chat_id FROM direct_index WHERE pair_key=$1`, key).Scan(&chatID)
	if err == nil {
		_ = tx.Commit(ctx)
		return s.GetChat(ctx, itoa(chatID))
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	now := nowMs()
	if _, err = tx.Exec(ctx,
		`INSERT INTO chats (id, type, title, owner_id, created_at, last_seq) VALUES ($1,$4,'',$2,$3,0)`,
		atoi(newID), atoi(userA), now, string(typ)); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO direct_index (pair_key, chat_id) VALUES ($1,$2)`, key, atoi(newID)); err != nil {
		return nil, err
	}
	// Seed membership. For a self-chat (userA == userB, e.g. "Saved Messages")
	// the pair collapses to one member; ON CONFLICT keeps it idempotent.
	members := []struct {
		id   string
		role string
	}{{userA, "owner"}}
	if userB != userA {
		members = append(members, struct{ id, role string }{userB, "member"})
	}
	for _, m := range members {
		if _, err = tx.Exec(ctx,
			`INSERT INTO chat_members (chat_id, user_id, role, joined_at) VALUES ($1,$2,$3,$4)
			 ON CONFLICT (chat_id, user_id) DO NOTHING`,
			atoi(newID), atoi(m.id), m.role, now); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &model.Chat{ID: newID, Type: model.ChatDirect, OwnerID: userA, CreatedAt: now}, nil
}

func (s *Store) GetDirect(ctx context.Context, userA, userB string) (*model.Chat, error) {
	var chatID int64
	err := s.pool.QueryRow(ctx, `SELECT chat_id FROM direct_index WHERE pair_key=$1`, directKey(userA, userB)).Scan(&chatID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetChat(ctx, itoa(chatID))
}

func (s *Store) AddMember(ctx context.Context, m *model.ChatMember) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO chat_members (chat_id, user_id, role, joined_at, muted)
		 VALUES ($1,$2,$3,$4,$5) ON CONFLICT (chat_id, user_id) DO NOTHING`,
		atoi(m.ChatID), atoi(m.UserID), string(m.Role), m.JoinedAt, m.Muted)
	return wrap(err)
}

func (s *Store) RemoveMember(ctx context.Context, chatID, userID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM chat_members WHERE chat_id=$1 AND user_id=$2`, atoi(chatID), atoi(userID))
	return err
}

func (s *Store) ListMembers(ctx context.Context, chatID string) ([]*model.ChatMember, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT chat_id, user_id, role, joined_at, muted FROM chat_members WHERE chat_id=$1`, atoi(chatID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.ChatMember
	for rows.Next() {
		var m model.ChatMember
		var cid, uid int64
		var role string
		if err := rows.Scan(&cid, &uid, &role, &m.JoinedAt, &m.Muted); err != nil {
			return nil, err
		}
		m.ChatID, m.UserID, m.Role = itoa(cid), itoa(uid), model.MemberRole(role)
		out = append(out, &m)
	}
	return out, rows.Err()
}

// ListMemberIDsPage walks membership by keyset. The (chat_id, user_id) primary
// key makes this an index range scan whose cost depends on the PAGE size, not on
// how deep into a million-member channel the page sits — which is exactly what
// OFFSET cannot promise.
func (s *Store) ListMemberIDsPage(ctx context.Context, chatID, afterUserID string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 500
	}
	var after int64
	if afterUserID != "" {
		after = atoi(afterUserID)
	}
	rows, err := s.reader().Query(ctx,
		`SELECT user_id FROM chat_members
		 WHERE chat_id=$1 AND user_id > $2 ORDER BY user_id LIMIT $3`,
		atoi(chatID), after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0, limit)
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		out = append(out, itoa(uid))
	}
	return out, rows.Err()
}

// ListMembersPage is ListMemberIDsPage with roles attached.
func (s *Store) ListMembersPage(ctx context.Context, chatID, afterUserID string, limit int) ([]*model.ChatMember, error) {
	if limit <= 0 {
		limit = 500
	}
	var after int64
	if afterUserID != "" {
		after = atoi(afterUserID)
	}
	rows, err := s.reader().Query(ctx,
		`SELECT chat_id, user_id, role, joined_at, muted FROM chat_members
		 WHERE chat_id=$1 AND user_id > $2 ORDER BY user_id LIMIT $3`,
		atoi(chatID), after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*model.ChatMember, 0, limit)
	for rows.Next() {
		var m model.ChatMember
		var cid, uid int64
		var role string
		if err := rows.Scan(&cid, &uid, &role, &m.JoinedAt, &m.Muted); err != nil {
			return nil, err
		}
		m.ChatID, m.UserID, m.Role = itoa(cid), itoa(uid), model.MemberRole(role)
		out = append(out, &m)
	}
	return out, rows.Err()
}

// GetMember reads one membership row by primary key.
func (s *Store) GetMember(ctx context.Context, chatID, userID string) (*model.ChatMember, error) {
	var m model.ChatMember
	var cid, uid int64
	var role string
	err := s.reader().QueryRow(ctx,
		`SELECT chat_id, user_id, role, joined_at, muted FROM chat_members
		 WHERE chat_id=$1 AND user_id=$2`, atoi(chatID), atoi(userID)).
		Scan(&cid, &uid, &role, &m.JoinedAt, &m.Muted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	m.ChatID, m.UserID, m.Role = itoa(cid), itoa(uid), model.MemberRole(role)
	return &m, nil
}

// CountMembersWithRole counts holders of a role in a chat.
func (s *Store) CountMembersWithRole(ctx context.Context, chatID string, role model.MemberRole) (int, error) {
	var n int
	err := s.reader().QueryRow(ctx,
		`SELECT count(*) FROM chat_members WHERE chat_id=$1 AND role=$2`,
		atoi(chatID), string(role)).Scan(&n)
	return n, err
}

func (s *Store) ListUserChats(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT chat_id FROM chat_members WHERE user_id=$1`, atoi(userID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var cid int64
		if err := rows.Scan(&cid); err != nil {
			return nil, err
		}
		out = append(out, itoa(cid))
	}
	return out, rows.Err()
}

func (s *Store) IsMember(ctx context.Context, chatID, userID string) (bool, error) {
	var one int
	err := s.pool.QueryRow(ctx,
		`SELECT 1 FROM chat_members WHERE chat_id=$1 AND user_id=$2`, atoi(chatID), atoi(userID)).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) BumpSeq(ctx context.Context, chatID string) (uint64, error) {
	var seq int64
	err := s.pool.QueryRow(ctx, seqBumpSQL, atoi(chatID)).Scan(&seq)
	return uint64(seq), err
}

// --- MessageStore ---

// InsertMessage runs seq allocation and the insert in one transaction so a chat
// never has an ordering gap, and dedup so retries are idempotent. On a dedup
// race the loser reads back the winning row.
// InsertMessage persists a message. It routes through the write batcher when
// enabled (group commit: many messages share one fsync), falling back to a
// single-message transaction. A dedup fast-path resolves retries without a write.
func (s *Store) InsertMessage(ctx context.Context, m *model.Message, dedupKey string, mkOb store.MakeOutbox) (*model.Message, bool, error) {
	if s.batcher != nil {
		// The batched path skips a dedup pre-SELECT (a round-trip on the hot path)
		// and relies on the unique index + per-job fallback to resolve the rare
		// concurrent duplicate.
		return s.batcher.submit(ctx, m, dedupKey, mkOb)
	}
	return s.insertOne(ctx, m, dedupKey, mkOb)
}

// insertOne persists a single message in its own transaction (the fallback and
// the batcher's per-job recovery path).
func (s *Store) insertOne(ctx context.Context, m *model.Message, dedupKey string, mkOb store.MakeOutbox) (*model.Message, bool, error) {
	// Fast path: already stored under this dedup key.
	if dedupKey != "" {
		if existing, err := s.getByDedup(ctx, m.SenderID, dedupKey); err == nil {
			return existing, true, nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, false, err
		}
	}
	var replyTo int64
	if m.ReplyTo != "" {
		replyTo = atoi(m.ReplyTo)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	// Rollback after a successful Commit is a no-op that returns
	// ErrTxClosed; there is nothing to do with it either way.
	defer func() { _ = tx.Rollback(ctx) }()

	var seq int64
	// Self-contained seq allocation (chat_seq upsert) so this works on a message
	// shard with no chats row. CanPost was already checked upstream.
	err = tx.QueryRow(ctx, seqBumpSQL, atoi(m.ChatID)).Scan(&seq)
	if err != nil {
		return nil, false, err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO messages (id, chat_id, sender_id, seq, text, media_ref, reply_to, dedup_key, created_at, attachment, thread_root, fwd_chat_id, fwd_msg_id, fwd_sender_id, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		atoi(m.ID), atoi(m.ChatID), atoi(m.SenderID), seq, m.Text, m.MediaRef, replyTo, dedupKey, m.CreatedAt,
		attachJSON(m.Attachment), atoi(m.ThreadRoot),
		fwdChat(m), fwdMsg(m), fwdSender(m), m.ExpiresAt)
	if err != nil {
		if isUniqueViolation(err) && dedupKey != "" {
			// Lost the dedup race; return the winner (seq bump rolls back).
			if existing, e2 := s.getByDedup(ctx, m.SenderID, dedupKey); e2 == nil {
				return existing, true, nil
			}
		}
		return nil, false, err
	}
	// Maintain the root's reply tally in the same transaction, so "N replies" can
	// never drift from the actual thread contents.
	if m.ThreadRoot != "" {
		if _, err = tx.Exec(ctx,
			`UPDATE messages SET reply_count = reply_count + 1 WHERE chat_id=$1 AND id=$2`,
			atoi(m.ChatID), atoi(m.ThreadRoot)); err != nil {
			return nil, false, err
		}
	}
	cp := *m
	cp.Seq = uint64(seq)
	// Stage the domain event in the same transaction (transactional outbox).
	if err = stageOutboxTx(ctx, tx, mkOb, &cp); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return &cp, false, nil
}

// traceColumn renders an outbox record's trace context for the `trace` column.
// Shared with the batched write path (batch.go), which staged outbox rows without
// it — so a trace followed send→outbox→fanout only while group commit was off,
// i.e. everywhere except the configuration production actually runs.
func traceColumn(rec *store.OutboxRecord) string {
	if len(rec.Trace) == 0 {
		return ""
	}
	b, _ := json.Marshal(rec.Trace)
	return string(b)
}

// stageOutboxTx writes the outbox event inside the caller's transaction.
func stageOutboxTx(ctx context.Context, tx pgx.Tx, mkOb store.MakeOutbox, m *model.Message) error {
	if mkOb == nil {
		return nil
	}
	rec := mkOb(m)
	if rec == nil {
		return nil
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO outbox (id, subject, key, payload, created_at, trace) VALUES ($1,$2,$3,$4,$5,$6)`,
		atoi(rec.ID), rec.Subject, rec.Key, rec.Data, nowMs(), traceColumn(rec)); err != nil {
		return err
	}
	// Wake the relay immediately on commit (LISTEN/NOTIFY) instead of waiting for
	// the next poll tick.
	_, err := tx.Exec(ctx, `NOTIFY SYNCAPP_outbox`)
	return err
}

// Listen returns a channel that receives a signal whenever an outbox row is
// committed (Postgres LISTEN/NOTIFY), letting the relay drain with low latency
// instead of tight polling. It holds one dedicated pooled connection.
func (s *Store) Listen(ctx context.Context) (<-chan struct{}, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, "LISTEN SYNCAPP_outbox"); err != nil {
		conn.Release()
		return nil, err
	}
	ch := make(chan struct{}, 1)
	go func() {
		defer conn.Release()
		for {
			if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
				return // ctx cancelled or connection lost
			}
			select {
			case ch <- struct{}{}:
			default: // a pending signal already queued; coalesce
			}
		}
	}()
	return ch, nil
}

func (s *Store) getByDedup(ctx context.Context, senderID, dedupKey string) (*model.Message, error) {
	return s.scanMessage(ctx, `WHERE sender_id=$1 AND dedup_key=$2`, atoi(senderID), dedupKey)
}

func (s *Store) GetMessage(ctx context.Context, chatID, id string) (*model.Message, error) {
	return s.scanMessage(ctx, `WHERE chat_id=$1 AND id=$2`, atoi(chatID), atoi(id))
}

// scanMsg decodes one message row selected with msgCols.
func scanMsg(sc rowScanner) (*model.Message, error) {
	var (
		m                               model.Message
		mid, cid, sid, seq, reply, root int64
		fwdChat, fwdMsg, fwdSender      int64
		attach                          []byte
	)
	if err := sc.Scan(&mid, &cid, &sid, &seq, &m.Text, &m.MediaRef, &reply, &m.Edited,
		&m.Deleted, &m.CreatedAt, &m.EditedAt, &attach, &root, &m.ReplyCount,
		&fwdChat, &fwdMsg, &fwdSender, &m.ExpiresAt); err != nil {
		return nil, err
	}
	m.ID, m.ChatID, m.SenderID, m.Seq = itoa(mid), itoa(cid), itoa(sid), uint64(seq)
	if reply != 0 {
		m.ReplyTo = itoa(reply)
	}
	// Forward provenance is a snapshot copied at forward time, so it survives the
	// original being deleted.
	if fwdChat != 0 || fwdMsg != 0 || fwdSender != 0 {
		m.Forward = &model.ForwardOrigin{ChatID: itoa(fwdChat), MessageID: itoa(fwdMsg), SenderID: itoa(fwdSender)}
	}
	if root != 0 {
		m.ThreadRoot = itoa(root)
	}
	if len(attach) > 0 {
		var a model.Attachment
		if err := json.Unmarshal(attach, &a); err == nil {
			m.Attachment = &a
		}
	}
	return &m, nil
}

func (s *Store) scanMessage(ctx context.Context, where string, args ...any) (*model.Message, error) {
	m, err := scanMsg(s.pool.QueryRow(ctx, `SELECT `+msgCols+` FROM messages `+where, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return m, err
}

func (s *Store) EditMessage(ctx context.Context, chatID, id, text string, at int64, mkOb store.MakeOutbox) (*model.Message, error) {
	return s.mutateMessage(ctx, mkOb,
		`UPDATE messages SET text=$3, edited=TRUE, edited_at=$4
		 WHERE chat_id=$1 AND id=$2 AND deleted=FALSE
		 RETURNING `+msgCols,
		atoi(chatID), atoi(id), text, at)
}

func (s *Store) DeleteMessage(ctx context.Context, chatID, id string, at int64, mkOb store.MakeOutbox) (*model.Message, error) {
	return s.mutateMessage(ctx, mkOb,
		`UPDATE messages SET deleted=TRUE, text='', media_ref='', edited_at=$3
		 WHERE chat_id=$1 AND id=$2
		 RETURNING `+msgCols,
		atoi(chatID), atoi(id), at)
}

// mutateMessage runs an UPDATE ... RETURNING inside a transaction and stages the
// resulting outbox event atomically.
func (s *Store) mutateMessage(ctx context.Context, mkOb store.MakeOutbox, sql string, args ...any) (*model.Message, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	// Rollback after a successful Commit is a no-op that returns
	// ErrTxClosed; there is nothing to do with it either way.
	defer func() { _ = tx.Rollback(ctx) }()

	m, err := scanMsg(tx.QueryRow(ctx, sql, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = stageOutboxTx(ctx, tx, mkOb, m); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

// Poll atomically CLAIMS up to limit unsent outbox records and returns them.
// The claim uses FOR UPDATE SKIP LOCKED so multiple relays across nodes never
// grab the same rows (no double-publish). A claim older than the reclaim window
// is retried (covers a relay that crashed after claiming but before MarkSent),
// preserving at-least-once. Consumers are idempotent, so a rare replay is safe.
func (s *Store) Poll(ctx context.Context, limit int) ([]store.OutboxRecord, error) {
	const reclaimAfterMs = 60_000
	now := nowMs()
	rows, err := s.pool.Query(ctx,
		`UPDATE outbox SET claimed_at = $1
		 WHERE id IN (
		   SELECT id FROM outbox
		   WHERE sent_at = 0 AND claimed_at < $2
		   ORDER BY id ASC
		   LIMIT $3
		   FOR UPDATE SKIP LOCKED
		 )
		 RETURNING id, subject, key, payload, trace`,
		now, now-reclaimAfterMs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.OutboxRecord
	for rows.Next() {
		var (
			rec   store.OutboxRecord
			id    int64
			trace string
		)
		if err := rows.Scan(&id, &rec.Subject, &rec.Key, &rec.Data, &trace); err != nil {
			return nil, err
		}
		rec.ID = itoa(id)
		if trace != "" {
			_ = json.Unmarshal([]byte(trace), &rec.Trace)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// PurgeSent deletes published rows in bounded chunks. The subselect keeps each
// statement short — an unbounded DELETE on a table this hot would hold locks long
// enough to be felt on the write path it exists to serve.
func (s *Store) PurgeSent(ctx context.Context, before int64, limit int) (int, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM outbox WHERE id IN (
		   SELECT id FROM outbox WHERE sent_at > 0 AND sent_at < $1 ORDER BY id LIMIT $2
		 )`, before, limit)
	if err != nil {
		return 0, wrap(err)
	}
	return int(tag.RowsAffected()), nil
}

func (s *Store) MarkSent(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	nums := make([]int64, len(ids))
	for i, id := range ids {
		nums[i] = atoi(id)
	}
	_, err := s.pool.Exec(ctx, `UPDATE outbox SET sent_at=$2 WHERE id = ANY($1)`, nums, nowMs())
	return err
}

func (s *Store) History(ctx context.Context, chatID string, beforeSeq uint64, limit int) ([]*model.Message, error) {
	if limit <= 0 {
		limit = 50
	}
	before := int64(beforeSeq)
	if before == 0 {
		before = 1 << 62 // effectively "latest"
	}
	// History backfill is lag-tolerant → serve from the read replica when present.
	rows, err := s.reader().Query(ctx,
		`SELECT `+msgCols+` FROM messages WHERE chat_id=$1 AND seq < $2 ORDER BY seq DESC LIMIT $3`,
		atoi(chatID), before, limit)
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

// Thread returns the replies under a thread root, oldest first (a thread reads
// forward, unlike history which pages backward).
func (s *Store) Thread(ctx context.Context, chatID, rootID string, afterSeq uint64, limit int) ([]*model.Message, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.reader().Query(ctx,
		`SELECT `+msgCols+` FROM messages
		 WHERE chat_id=$1 AND thread_root=$2 AND seq > $3 ORDER BY seq ASC LIMIT $4`,
		atoi(chatID), atoi(rootID), int64(afterSeq), limit)
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
