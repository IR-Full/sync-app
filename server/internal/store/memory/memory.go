// Package memory is an in-process implementation of the store interfaces. It
// backs local dev, unit tests, and `go run` without Docker. It is NOT durable
// and NOT multi-node — production uses the postgres package. Every method is
// mutex-guarded so it is safe under the gateway's concurrent goroutines.
package memory

import (
	"context"
	"sort"
	"strings"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store"
)

// New returns an empty in-memory store usable for every store interface.
func New() *Store {
	return &Store{
		users:       map[string]*model.User{},
		usersByName: map[string]string{},
		devices:     map[string]*model.Device{},
		sessions:    map[string]*model.Session{},
		tokenIndex:  map[string]string{},
		resumeIndex: map[string]string{},
		chats:       map[string]*model.Chat{},
		directIndex: map[string]string{},
		members:     map[string]map[string]*model.ChatMember{},
		messages:    map[string][]*model.Message{},
		dedup:       map[string]string{},
		reads:       map[string]map[string]*model.ReadState{},
		reactions:   map[string]map[string]*model.Reaction{},
		calls:       map[string]*model.Call{},
		callParts:   map[string]map[string]*model.CallParticipant{},
		polls:       map[string]*model.Poll{},
		pollByMsg:   map[string]string{},
		pollVotes:   map[string]map[string]map[int32]bool{},
		contacts:    map[string]map[string]*model.Contact{},
		scheduled:   map[string]*model.ScheduledMessage{},
		pins:        map[string]map[string]*model.PinnedMessage{},
		drafts:      map[string]map[string]*model.Draft{},
		invites:     map[string]*model.InviteLink{},
		outboxSent:  map[string]bool{},
	}
}

// Stores returns a store.Stores bundle backed by this instance.
func (s *Store) Stores() store.Stores {
	return store.Stores{Users: s, Sessions: s, Chats: s, Messages: s, Reads: s, Reactions: s, Calls: s, Polls: s, Contacts: s, Schedule: s, Pins: s, Drafts: s, Invites: s, Outbox: s}
}

// --- UserStore ---

func (s *Store) CreateUser(_ context.Context, u *model.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.usersByName[u.Username]; ok {
		return store.ErrConflict
	}
	cp := *u
	s.users[u.ID] = &cp
	s.usersByName[u.Username] = u.ID
	return nil
}

func (s *Store) GetUser(_ context.Context, id string) (*model.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *u
	return &cp, nil
}

func (s *Store) GetUserByUsername(_ context.Context, username string) (*model.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.usersByName[username]
	if !ok {
		return nil, store.ErrNotFound
	}
	u := s.users[id]
	cp := *u
	return &cp, nil
}

func (s *Store) UpdateProfile(_ context.Context, userID, displayName, avatarRef string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[userID]
	if !ok {
		return store.ErrNotFound
	}
	u.DisplayName = displayName
	u.AvatarRef = avatarRef
	return nil
}

// UpsertDevice mirrors the Postgres semantics: a device id already owned by
// another user is refused (ErrConflict) rather than taken over, and an empty
// push token leaves the stored one alone. See the Postgres implementation for
// why — the id is client-asserted.
func (s *Store) UpsertDevice(_ context.Context, d *model.Device) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *d
	if cur, ok := s.devices[d.ID]; ok {
		if cur.UserID != d.UserID {
			return store.ErrConflict
		}
		if cp.PushToken == "" {
			cp.PushToken = cur.PushToken
		}
		cp.CreatedAt = cur.CreatedAt
	}
	s.devices[d.ID] = &cp
	return nil
}

// SetPushToken writes a device's push token, scoped to its owner.
func (s *Store) SetPushToken(_ context.Context, userID, deviceID, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devices[deviceID]
	if !ok || d.UserID != userID {
		return store.ErrNotFound
	}
	d.PushToken = token
	return nil
}

func (s *Store) GetDevice(_ context.Context, id string) (*model.Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.devices[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *d
	return &cp, nil
}

func (s *Store) ListDevices(_ context.Context, userID string) ([]*model.Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.Device
	for _, d := range s.devices {
		if d.UserID == userID {
			cp := *d
			out = append(out, &cp)
		}
	}
	return out, nil
}

// --- SessionStore ---

func (s *Store) CreateSession(_ context.Context, sess *model.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *sess
	s.sessions[sess.ID] = &cp
	s.tokenIndex[sess.Token] = sess.ID
	if sess.ResumeToken != "" {
		s.resumeIndex[sess.ResumeToken] = sess.ID
	}
	return nil
}

func (s *Store) GetSessionByToken(_ context.Context, token string) (*model.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.tokenIndex[token]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *s.sessions[id]
	return &cp, nil
}

func (s *Store) GetSessionByResumeToken(_ context.Context, resume string) (*model.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.resumeIndex[resume]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *s.sessions[id]
	return &cp, nil
}

func (s *Store) RevokeSession(_ context.Context, id string, at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return store.ErrNotFound
	}
	sess.RevokedAt = at
	return nil
}

func (s *Store) ListSessions(_ context.Context, userID string) ([]*model.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.Session
	for _, sess := range s.sessions {
		if sess.UserID == userID {
			cp := *sess
			out = append(out, &cp)
		}
	}
	return out, nil
}

// --- ChatStore ---

func (s *Store) CreateChat(_ context.Context, c *model.Chat) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *c
	s.chats[c.ID] = &cp
	return nil
}

func (s *Store) GetChat(_ context.Context, id string) (*model.Chat, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.chats[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (s *Store) GetOrCreateDirect(_ context.Context, userA, userB, newID string) (*model.Chat, error) {
	key := directKey(userA, userB)
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.directIndex[key]; ok {
		cp := *s.chats[id]
		return &cp, nil
	}
	now := nowMs()
	c := &model.Chat{ID: newID, Type: model.ChatDirect, OwnerID: userA, CreatedAt: now}
	s.chats[newID] = c
	s.directIndex[key] = newID
	// Seed both members.
	s.addMemberLocked(&model.ChatMember{ChatID: newID, UserID: userA, Role: model.RoleOwner, JoinedAt: now})
	s.addMemberLocked(&model.ChatMember{ChatID: newID, UserID: userB, Role: model.RoleMember, JoinedAt: now})
	cp := *c
	return &cp, nil
}

func (s *Store) GetDirect(_ context.Context, userA, userB string) (*model.Chat, error) {
	key := directKey(userA, userB)
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.directIndex[key]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *s.chats[id]
	return &cp, nil
}

func (s *Store) AddMember(_ context.Context, m *model.ChatMember) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addMemberLocked(m)
	return nil
}

func (s *Store) addMemberLocked(m *model.ChatMember) {
	if s.members[m.ChatID] == nil {
		s.members[m.ChatID] = map[string]*model.ChatMember{}
	}
	cp := *m
	s.members[m.ChatID][m.UserID] = &cp
}

func (s *Store) RemoveMember(_ context.Context, chatID, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mm := s.members[chatID]; mm != nil {
		delete(mm, userID)
	}
	return nil
}

func (s *Store) ListMembers(_ context.Context, chatID string) ([]*model.ChatMember, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.ChatMember
	for _, m := range s.members[chatID] {
		cp := *m
		out = append(out, &cp)
	}
	return out, nil
}

// ListMemberIDsPage mirrors the Postgres keyset walk (ordered ids after a
// cursor). Ordering is lexicographic here; snowflake ids are fixed-width decimal
// in practice, so the two agree.
func (s *Store) ListMemberIDsPage(_ context.Context, chatID, afterUserID string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 500
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.members[chatID]))
	for uid := range s.members[chatID] {
		if uid > afterUserID {
			ids = append(ids, uid)
		}
	}
	sort.Strings(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return ids, nil
}

// ListMembersPage mirrors the Postgres keyset walk, with roles attached.
func (s *Store) ListMembersPage(_ context.Context, chatID, afterUserID string, limit int) ([]*model.ChatMember, error) {
	if limit <= 0 {
		limit = 500
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.members[chatID]))
	for uid := range s.members[chatID] {
		if uid > afterUserID {
			ids = append(ids, uid)
		}
	}
	sort.Strings(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	out := make([]*model.ChatMember, 0, len(ids))
	for _, uid := range ids {
		cp := *s.members[chatID][uid]
		out = append(out, &cp)
	}
	return out, nil
}

// GetMember reads one membership row.
func (s *Store) GetMember(_ context.Context, chatID, userID string) (*model.ChatMember, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.members[chatID][userID]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *m
	return &cp, nil
}

// CountMembersWithRole counts holders of a role in a chat.
func (s *Store) CountMembersWithRole(_ context.Context, chatID string, role model.MemberRole) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, m := range s.members[chatID] {
		if m.Role == role {
			n++
		}
	}
	return n, nil
}

func (s *Store) ListUserChats(_ context.Context, userID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for chatID, mm := range s.members {
		if _, ok := mm[userID]; ok {
			out = append(out, chatID)
		}
	}
	return out, nil
}

func (s *Store) IsMember(_ context.Context, chatID, userID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	mm := s.members[chatID]
	if mm == nil {
		return false, nil
	}
	_, ok := mm[userID]
	return ok, nil
}

func (s *Store) BumpSeq(_ context.Context, chatID string) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chats[chatID]
	if !ok {
		return 0, store.ErrNotFound
	}
	c.LastSeq++
	return c.LastSeq, nil
}

// --- MessageStore ---

// InsertMessage allocates the per-chat Seq and appends atomically under the
// store lock, so retries dedup without consuming a sequence (no ordering gaps).
// A non-nil mkOb stages the outbox event under the same lock (atomic with the
// insert), mirroring the Postgres transactional outbox.
func (s *Store) InsertMessage(_ context.Context, m *model.Message, dedupKey string, mkOb store.MakeOutbox) (*model.Message, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dk := m.SenderID + "|" + dedupKey
	if dedupKey != "" {
		if existingID, ok := s.dedup[dk]; ok {
			for _, ex := range s.messages[m.ChatID] {
				if ex.ID == existingID {
					cp := *ex
					return &cp, true, nil
				}
			}
		}
	}
	c, ok := s.chats[m.ChatID]
	if !ok {
		return nil, false, store.ErrNotFound
	}
	c.LastSeq++
	cp := *m
	cp.Seq = c.LastSeq
	s.messages[m.ChatID] = append(s.messages[m.ChatID], &cp)
	if m.ThreadRoot != "" {
		for _, ex := range s.messages[m.ChatID] {
			if ex.ID == m.ThreadRoot {
				ex.ReplyCount++
				break
			}
		}
	}
	if dedupKey != "" {
		s.dedup[dk] = m.ID
	}
	out := cp
	s.stageOutboxLocked(mkOb, &out)
	return &out, false, nil
}

// stageOutboxLocked appends an outbox record if mkOb yields one. Caller holds mu.
func (s *Store) stageOutboxLocked(mkOb store.MakeOutbox, m *model.Message) {
	if mkOb == nil {
		return
	}
	if rec := mkOb(m); rec != nil {
		s.outbox = append(s.outbox, *rec)
	}
}

func (s *Store) GetMessage(_ context.Context, chatID, id string) (*model.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.messages[chatID] {
		if m.ID == id {
			cp := *m
			return &cp, nil
		}
	}
	return nil, store.ErrNotFound
}

func (s *Store) EditMessage(_ context.Context, chatID, id, text string, at int64, mkOb store.MakeOutbox) (*model.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.messages[chatID] {
		if m.ID == id {
			m.Text = text
			m.Edited = true
			m.EditedAt = at
			cp := *m
			s.stageOutboxLocked(mkOb, &cp)
			return &cp, nil
		}
	}
	return nil, store.ErrNotFound
}

func (s *Store) DeleteMessage(_ context.Context, chatID, id string, at int64, mkOb store.MakeOutbox) (*model.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.messages[chatID] {
		if m.ID == id {
			m.Deleted = true
			m.Text = ""
			m.MediaRef = ""
			m.EditedAt = at
			cp := *m
			s.stageOutboxLocked(mkOb, &cp)
			return &cp, nil
		}
	}
	return nil, store.ErrNotFound
}

// Poll returns up to limit unsent outbox records (FIFO).
func (s *Store) Poll(_ context.Context, limit int) ([]store.OutboxRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.OutboxRecord
	for _, rec := range s.outbox {
		if s.outboxSent[rec.ID] {
			continue
		}
		out = append(out, rec)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// PurgeSent drops sent records the prefix compaction could not reach (a sent
// record sitting behind an unsent one). The durable store is where retention
// actually matters; this keeps the two implementations honest about the contract.
func (s *Store) PurgeSent(_ context.Context, _ int64, limit int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := make([]store.OutboxRecord, 0, len(s.outbox))
	n := 0
	for _, rec := range s.outbox {
		if n < limit && s.outboxSent[rec.ID] {
			delete(s.outboxSent, rec.ID)
			n++
			continue
		}
		kept = append(kept, rec)
	}
	s.outbox = kept
	return n, nil
}

// MarkSent marks records delivered and compacts the fully-drained prefix.
func (s *Store) MarkSent(_ context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		s.outboxSent[id] = true
	}
	// Compact: drop leading records that are all sent.
	i := 0
	for i < len(s.outbox) && s.outboxSent[s.outbox[i].ID] {
		delete(s.outboxSent, s.outbox[i].ID)
		i++
	}
	s.outbox = s.outbox[i:]
	return nil
}

// MediaRefExists mirrors the Postgres capability: is this blob still reachable
// from a live message (its own ref, or an attachment's)?
func (s *Store) MediaRefExists(_ context.Context, ref string) (bool, error) {
	if ref == "" {
		return false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, msgs := range s.messages {
		for _, m := range msgs {
			if m.Deleted {
				continue
			}
			if m.MediaRef == ref {
				return true, nil
			}
			if m.Attachment != nil && (m.Attachment.MediaRef == ref || m.Attachment.ThumbRef == ref) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (s *Store) History(_ context.Context, chatID string, beforeSeq uint64, limit int) ([]*model.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := s.messages[chatID]
	// Copy + sort ascending by Seq, then select the window newest-first.
	tmp := make([]*model.Message, 0, len(all))
	for _, m := range all {
		if beforeSeq == 0 || m.Seq < beforeSeq {
			cp := *m
			tmp = append(tmp, &cp)
		}
	}
	sort.Slice(tmp, func(i, j int) bool { return tmp[i].Seq > tmp[j].Seq })
	if limit > 0 && len(tmp) > limit {
		tmp = tmp[:limit]
	}
	return tmp, nil
}

// --- ReactionStore ---

func (s *Store) SetReaction(_ context.Context, r *model.Reaction) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	byUser, ok := s.reactions[r.MessageID]
	if !ok {
		byUser = map[string]*model.Reaction{}
		s.reactions[r.MessageID] = byUser
	}
	// Toggle: same emoji again removes it; a different emoji replaces it.
	if prev, exists := byUser[r.UserID]; exists && prev.Emoji == r.Emoji {
		delete(byUser, r.UserID)
		if len(byUser) == 0 {
			delete(s.reactions, r.MessageID)
		}
		return false, nil
	}
	cp := *r
	byUser[r.UserID] = &cp
	return true, nil
}

func (s *Store) ListReactions(_ context.Context, _, messageID string) ([]*model.Reaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*model.Reaction, 0, len(s.reactions[messageID]))
	for _, r := range s.reactions[messageID] {
		cp := *r
		out = append(out, &cp)
	}
	return out, nil
}

// --- ReadStore ---

func (s *Store) SetRead(_ context.Context, rs *model.ReadState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reads[rs.ChatID] == nil {
		s.reads[rs.ChatID] = map[string]*model.ReadState{}
	}
	// Monotonic: never move a read cursor backwards.
	if cur, ok := s.reads[rs.ChatID][rs.UserID]; ok && cur.UpToSeq >= rs.UpToSeq {
		return nil
	}
	cp := *rs
	s.reads[rs.ChatID][rs.UserID] = &cp
	return nil
}

func (s *Store) GetRead(_ context.Context, chatID, userID string) (*model.ReadState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	mm := s.reads[chatID]
	if mm == nil {
		return nil, store.ErrNotFound
	}
	rs, ok := mm[userID]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *rs
	return &cp, nil
}

// Thread returns the replies under a thread root, oldest first (ThreadReader).
func (s *Store) Thread(_ context.Context, chatID, rootID string, afterSeq uint64, limit int) ([]*model.Message, error) {
	if limit <= 0 {
		limit = 50
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.Message
	for _, m := range s.messages[chatID] {
		if m.ThreadRoot != rootID || m.Seq <= afterSeq {
			continue
		}
		cp := *m
		out = append(out, &cp)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// --- CallStore ---

func (s *Store) CreateCall(_ context.Context, c *model.Call) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *c
	s.calls[c.ID] = &cp
	return nil
}

func (s *Store) GetCall(_ context.Context, id string) (*model.Call, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.calls[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (s *Store) SetCallState(_ context.Context, id string, state model.CallState, at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.calls[id]
	if !ok {
		return store.ErrNotFound
	}
	c.State = state
	if state == model.CallEnded {
		c.EndedAt = at
	}
	return nil
}

func (s *Store) UpsertParticipant(_ context.Context, p *model.CallParticipant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.callParts[p.CallID] == nil {
		s.callParts[p.CallID] = map[string]*model.CallParticipant{}
	}
	cp := *p
	if prev, ok := s.callParts[p.CallID][p.UserID]; ok {
		if cp.JoinedAt == 0 {
			cp.JoinedAt = prev.JoinedAt
		}
		if cp.LeftAt == 0 {
			cp.LeftAt = prev.LeftAt
		}
	}
	s.callParts[p.CallID][p.UserID] = &cp
	return nil
}

func (s *Store) ListParticipants(_ context.Context, callID string) ([]*model.CallParticipant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*model.CallParticipant, 0, len(s.callParts[callID]))
	for _, p := range s.callParts[callID] {
		cp := *p
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserID < out[j].UserID })
	return out, nil
}

func (s *Store) ActiveCallForChat(_ context.Context, chatID string) (*model.Call, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var newest *model.Call
	for _, c := range s.calls {
		if c.ChatID != chatID || c.State == model.CallEnded {
			continue
		}
		if newest == nil || c.CreatedAt > newest.CreatedAt {
			newest = c
		}
	}
	if newest == nil {
		return nil, store.ErrNotFound
	}
	cp := *newest
	return &cp, nil
}

// --- PollStore ---

func (s *Store) CreatePoll(_ context.Context, p *model.Poll) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *p
	s.polls[p.ID] = &cp
	s.pollByMsg[p.MessageID] = p.ID
	return nil
}

func (s *Store) GetPoll(_ context.Context, id string) (*model.Poll, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.polls[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (s *Store) GetPollByMessage(ctx context.Context, messageID string) (*model.Poll, error) {
	s.mu.RLock()
	id, ok := s.pollByMsg[messageID]
	s.mu.RUnlock()
	if !ok {
		return nil, store.ErrNotFound
	}
	return s.GetPoll(ctx, id)
}

func (s *Store) ClosePoll(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.polls[id]
	if !ok {
		return store.ErrNotFound
	}
	p.Closed = true
	return nil
}

func (s *Store) Vote(_ context.Context, v *model.PollVote, multiChoice bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pollVotes[v.PollID] == nil {
		s.pollVotes[v.PollID] = map[string]map[int32]bool{}
	}
	cur := s.pollVotes[v.PollID][v.UserID]
	if cur == nil {
		cur = map[int32]bool{}
		s.pollVotes[v.PollID][v.UserID] = cur
	}
	if multiChoice {
		if cur[v.OptionIndex] { // toggle off
			delete(cur, v.OptionIndex)
			return false, nil
		}
	} else {
		clear(cur) // single choice replaces
	}
	cur[v.OptionIndex] = true
	return true, nil
}

func (s *Store) Tally(_ context.Context, pollID string) (map[int32]int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[int32]int{}
	for _, opts := range s.pollVotes[pollID] {
		for idx := range opts {
			out[idx]++
		}
	}
	return out, nil
}

func (s *Store) VotedOptions(_ context.Context, pollID, userID string) ([]int32, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []int32
	for idx := range s.pollVotes[pollID][userID] {
		out = append(out, idx)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// --- ContactStore ---

func (s *Store) UpsertContact(_ context.Context, c *model.Contact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.contacts[c.OwnerID] == nil {
		s.contacts[c.OwnerID] = map[string]*model.Contact{}
	}
	cp := *c
	if prev, ok := s.contacts[c.OwnerID][c.UserID]; ok {
		cp.CreatedAt = prev.CreatedAt // keep the original add time
		cp.Blocked = prev.Blocked     // naming a contact must not unblock them
	}
	s.contacts[c.OwnerID][c.UserID] = &cp
	return nil
}

func (s *Store) DeleteContact(_ context.Context, ownerID, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.contacts[ownerID]; m != nil {
		delete(m, userID)
	}
	return nil
}

func (s *Store) GetContact(_ context.Context, ownerID, userID string) (*model.Contact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.contacts[ownerID][userID]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (s *Store) ListContacts(_ context.Context, ownerID string, since int64, limit int) ([]*model.Contact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.Contact
	for _, c := range s.contacts[ownerID] {
		if c.UpdatedAt <= since {
			continue
		}
		cp := *c
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt < out[j].UpdatedAt })
	return truncate(out, limit), nil
}

func (s *Store) SetBlocked(_ context.Context, ownerID, userID string, blocked bool, at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.contacts[ownerID] == nil {
		s.contacts[ownerID] = map[string]*model.Contact{}
	}
	c, ok := s.contacts[ownerID][userID]
	if !ok {
		c = &model.Contact{OwnerID: ownerID, UserID: userID, CreatedAt: at}
		s.contacts[ownerID][userID] = c
	}
	c.Blocked = blocked
	c.UpdatedAt = at
	return nil
}

func (s *Store) IsBlocked(_ context.Context, ownerID, userID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.contacts[ownerID][userID]
	return ok && c.Blocked, nil
}

// --- ScheduleStore ---

func (s *Store) CreateScheduled(_ context.Context, m *model.ScheduledMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *m
	s.scheduled[m.ID] = &cp
	return nil
}

func (s *Store) CancelScheduled(_ context.Context, id, senderID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.scheduled[id]
	if !ok || m.SenderID != senderID || m.Sent {
		return store.ErrNotFound
	}
	delete(s.scheduled, id)
	return nil
}

func (s *Store) ListScheduled(_ context.Context, senderID, chatID string) ([]*model.ScheduledMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.ScheduledMessage
	for _, m := range s.scheduled {
		if m.SenderID != senderID || m.ChatID != chatID || m.Sent {
			continue
		}
		cp := *m
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SendAt < out[j].SendAt })
	return out, nil
}

// PurgeSentScheduled drops fired rows past their retention window (see the
// Postgres implementation for why they are not kept).
func (s *Store) PurgeSentScheduled(_ context.Context, before int64, limit int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, m := range s.scheduled {
		if n >= limit {
			break
		}
		if m.Sent && m.SendAt < before {
			delete(s.scheduled, id)
			n++
		}
	}
	return n, nil
}

func (s *Store) ClaimDueScheduled(_ context.Context, now int64, limit int) ([]*model.ScheduledMessage, error) {
	if limit <= 0 {
		limit = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*model.ScheduledMessage
	for _, m := range s.scheduled {
		if m.Sent || m.SendAt > now {
			continue
		}
		m.Sent = true // claimed under the lock → never dispatched twice
		cp := *m
		out = append(out, &cp)
		if len(out) >= limit {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SendAt < out[j].SendAt })
	return out, nil
}

// ExpireMessages tombstones self-destructed messages (Expirer).
func (s *Store) ExpireMessages(_ context.Context, now int64, limit int) ([]*model.Message, error) {
	if limit <= 0 {
		limit = 200
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*model.Message
	for _, msgs := range s.messages {
		for _, m := range msgs {
			if m.ExpiresAt == 0 || m.ExpiresAt > now || m.Deleted {
				continue
			}
			m.Deleted, m.Text, m.MediaRef, m.Attachment, m.EditedAt = true, "", "", nil, now
			cp := *m
			out = append(out, &cp)
			if len(out) >= limit {
				return out, nil
			}
		}
	}
	return out, nil
}

// --- PinStore ---

func (s *Store) Pin(_ context.Context, p *model.PinnedMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pins[p.ChatID] == nil {
		s.pins[p.ChatID] = map[string]*model.PinnedMessage{}
	}
	cp := *p
	s.pins[p.ChatID][p.MessageID] = &cp
	return nil
}

func (s *Store) Unpin(_ context.Context, chatID, messageID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.pins[chatID]
	if m == nil {
		return store.ErrNotFound
	}
	if _, ok := m[messageID]; !ok {
		return store.ErrNotFound
	}
	delete(m, messageID)
	return nil
}

func (s *Store) ListPins(_ context.Context, chatID string) ([]*model.PinnedMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.PinnedMessage
	for _, p := range s.pins[chatID] {
		cp := *p
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PinnedAt > out[j].PinnedAt })
	return out, nil
}

// --- DraftStore ---

func (s *Store) SetDraft(_ context.Context, d *model.Draft) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.drafts[d.UserID] == nil {
		s.drafts[d.UserID] = map[string]*model.Draft{}
	}
	cp := *d
	s.drafts[d.UserID][d.ChatID] = &cp
	return nil
}

func (s *Store) DeleteDraft(_ context.Context, userID, chatID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.drafts[userID]; m != nil {
		delete(m, chatID)
	}
	return nil
}

func (s *Store) ListDrafts(_ context.Context, userID string, since int64, limit int) ([]*model.Draft, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.Draft
	for _, d := range s.drafts[userID] {
		if d.UpdatedAt <= since {
			continue
		}
		cp := *d
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt < out[j].UpdatedAt })
	return truncate(out, limit), nil
}

// truncate caps a sync page, mirroring the SQL LIMIT the Postgres store applies.
func truncate[T any](rows []T, limit int) []T {
	if limit > 0 && len(rows) > limit {
		return rows[:limit]
	}
	return rows
}

// --- InviteStore ---

func (s *Store) SetChatUsername(_ context.Context, chatID, username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chats[chatID]
	if !ok {
		return store.ErrNotFound
	}
	// Case-insensitive uniqueness, matching the Postgres index.
	if username != "" {
		for id, other := range s.chats {
			if id != chatID && strings.EqualFold(other.Username, username) {
				return store.ErrConflict
			}
		}
	}
	c.Username = username
	return nil
}

func (s *Store) GetChatByUsername(_ context.Context, username string) (*model.Chat, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.chats {
		if c.Username != "" && strings.EqualFold(c.Username, username) {
			cp := *c
			return &cp, nil
		}
	}
	return nil, store.ErrNotFound
}

func (s *Store) CreateInvite(_ context.Context, l *model.InviteLink) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.invites[l.Code]; ok {
		return store.ErrConflict
	}
	cp := *l
	s.invites[l.Code] = &cp
	return nil
}

func (s *Store) GetInvite(_ context.Context, code string) (*model.InviteLink, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l, ok := s.invites[code]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *l
	return &cp, nil
}

func (s *Store) RevokeInvite(_ context.Context, code, chatID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.invites[code]
	if !ok || l.ChatID != chatID {
		return store.ErrNotFound
	}
	l.Revoked = true
	return nil
}

func (s *Store) ListInvites(_ context.Context, chatID string) ([]*model.InviteLink, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.InviteLink
	for _, l := range s.invites {
		if l.ChatID != chatID || l.Revoked {
			continue
		}
		cp := *l
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

// UseInvite redeems under the write lock, so a capped link cannot be
// over-redeemed by concurrent joins.
func (s *Store) UseInvite(_ context.Context, code string, now int64) (*model.InviteLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.invites[code]
	if !ok || l.Revoked {
		return nil, store.ErrNotFound
	}
	if l.ExpiresAt != 0 && l.ExpiresAt <= now {
		return nil, store.ErrNotFound
	}
	if l.MaxUses != 0 && l.Uses >= l.MaxUses {
		return nil, store.ErrNotFound
	}
	l.Uses++
	cp := *l
	return &cp, nil
}

// SetMemberRole promotes/demotes a member (MemberRoleStore).
func (s *Store) SetMemberRole(_ context.Context, chatID, userID string, role model.MemberRole) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.members[chatID]
	if m == nil {
		return store.ErrNotFound
	}
	mem, ok := m[userID]
	if !ok {
		return store.ErrNotFound
	}
	mem.Role = role
	return nil
}
