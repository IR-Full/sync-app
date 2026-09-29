package memory

import (
	"context"

	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/store"
)

// TwoFactorStore.

func (s *Store) PutTwoFactor(_ context.Context, tf *model.TwoFactor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *tf
	cp.RecoveryHashes = append([]string(nil), tf.RecoveryHashes...)
	s.twoFactor[tf.UserID] = &cp
	return nil
}

func (s *Store) GetTwoFactor(_ context.Context, userID string) (*model.TwoFactor, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tf, ok := s.twoFactor[userID]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *tf
	cp.RecoveryHashes = append([]string(nil), tf.RecoveryHashes...)
	return &cp, nil
}

func (s *Store) DeleteTwoFactor(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.twoFactor, userID)
	return nil
}

// ConsumeRecoveryCode removes one hash under the lock, so two concurrent logins
// cannot both spend the same single-use code.
func (s *Store) ConsumeRecoveryCode(_ context.Context, userID, hash string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tf, ok := s.twoFactor[userID]
	if !ok {
		return false, nil
	}
	for i, h := range tf.RecoveryHashes {
		if h != hash {
			continue
		}
		tf.RecoveryHashes = append(tf.RecoveryHashes[:i], tf.RecoveryHashes[i+1:]...)
		return true, nil
	}
	return false, nil
}
