package memory

import (
	"context"
	"sort"

	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/store"
)

// ChatStore.

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

func (s *Store) GetOrCreateDirect(ctx context.Context, userA, userB, newID string) (*model.Chat, error) {
	return s.GetOrCreatePair(ctx, model.ChatDirect, userA, userB, newID)
}

// GetOrCreatePair is the canonical-pair lookup for any two-party chat type.
//
// The type is folded into the key rather than filtered on afterwards, because a
// direct chat and a secret chat with the same person are DIFFERENT conversations
// that must both be reachable: filtering makes one unfindable, and keying on the
// pair alone makes the second collide with the first.
func (s *Store) GetOrCreatePair(_ context.Context, typ model.ChatType, userA, userB, newID string) (*model.Chat, error) {
	if typ == "" {
		typ = model.ChatDirect
	}
	key := pairKey(typ, userA, userB)
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.directIndex[key]; ok {
		cp := *s.chats[id]
		return &cp, nil
	}
	now := nowMs()
	c := &model.Chat{ID: newID, Type: typ, OwnerID: userA, CreatedAt: now}
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
