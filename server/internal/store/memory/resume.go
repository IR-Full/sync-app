package memory

import (
	"context"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
)

// Resume-token rotation.

func (s *Store) RotateResumeToken(_ context.Context, sessionID, oldHash, newHash string, at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return store.ErrNotFound
	}
	// Compare-and-swap. Two resumes racing on the same token must not both
	// succeed: the loser sees a token that has already moved on, which is
	// indistinguishable from a replay — and is correctly treated as one.
	if sess.ResumeToken != oldHash {
		return store.ErrNotFound
	}
	delete(s.resumeIndex, oldHash)
	// The consumed token is REMEMBERED, not forgotten. That is what turns a stolen
	// token from an invisible second reader into a detectable one.
	s.prevResumeIndex[oldHash] = sessionID
	sess.PrevResumeToken = oldHash
	sess.ResumeToken = newHash
	sess.ResumeRotatedAt = at
	s.resumeIndex[newHash] = sessionID
	return nil
}

func (s *Store) GetSessionByConsumedResumeToken(_ context.Context, resume string) (*model.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.prevResumeIndex[resume]
	if !ok {
		return nil, store.ErrNotFound
	}
	sess, ok := s.sessions[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *sess
	return &cp, nil
}
