package memory

import (
	"context"

	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/store"
)

// SessionStore.

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

// TouchSession extends a live session's expiry. A revoked or expired session is
// left alone: reviving one would turn "log out" into "log out until the device
// reconnects", which is not a logout.
func (s *Store) TouchSession(_ context.Context, id string, expiresAt int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return store.ErrNotFound
	}
	if sess.RevokedAt != 0 || (sess.ExpiresAt != 0 && sess.ExpiresAt <= nowMs()) {
		return nil
	}
	if expiresAt > sess.ExpiresAt {
		sess.ExpiresAt = expiresAt
	}
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
