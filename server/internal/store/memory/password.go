package memory

import (
	"context"

	"github.com/IR-Full/sync-app/server/internal/store"
)

// PasswordStore.

func (s *Store) SetPasswordHash(_ context.Context, userID, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[userID]
	if !ok {
		return store.ErrNotFound
	}
	u.PasswordHash = hash
	return nil
}
