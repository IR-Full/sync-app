package memory

import (
	"context"
	"sort"
	"strings"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
)

// InviteStore.

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
