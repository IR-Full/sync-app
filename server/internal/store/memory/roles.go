package memory

import (
	"context"
	"sort"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
)

// PlatformRoleStore.

func (s *Store) PlatformRole(_ context.Context, userID string) (model.PlatformRole, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.platformRoles[userID].Role, nil
}

func (s *Store) ListPlatformRoles(_ context.Context) ([]model.PlatformRoleGrant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.PlatformRoleGrant, 0, len(s.platformRoles))
	for _, g := range s.platformRoles {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserID < out[j].UserID })
	return out, nil
}

func (s *Store) SetPlatformRole(_ context.Context, g model.PlatformRoleGrant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[g.UserID]; !ok {
		return store.ErrNotFound
	}
	s.platformRoles[g.UserID] = g
	return nil
}

func (s *Store) RemovePlatformRole(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.platformRoles, userID)
	return nil
}
