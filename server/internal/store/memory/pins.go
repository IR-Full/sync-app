package memory

import (
	"context"
	"sort"

	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/store"
)

// PinStore.

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
