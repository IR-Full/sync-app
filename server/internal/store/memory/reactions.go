package memory

import (
	"context"

	"github.com/SyncApp-chat/SyncApp/internal/model"
)

// ReactionStore.

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
