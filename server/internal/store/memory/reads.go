package memory

import (
	"context"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
)

// ReadStore.

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
