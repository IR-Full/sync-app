package memory

import (
	"context"
	"sort"

	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/store"
)

// CallStore.

func (s *Store) CreateCall(_ context.Context, c *model.Call) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *c
	s.calls[c.ID] = &cp
	return nil
}

func (s *Store) GetCall(_ context.Context, id string) (*model.Call, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.calls[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (s *Store) SetCallState(_ context.Context, id string, state model.CallState, at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.calls[id]
	if !ok {
		return store.ErrNotFound
	}
	c.State = state
	if state == model.CallEnded {
		c.EndedAt = at
	}
	return nil
}

func (s *Store) UpsertParticipant(_ context.Context, p *model.CallParticipant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.callParts[p.CallID] == nil {
		s.callParts[p.CallID] = map[string]*model.CallParticipant{}
	}
	cp := *p
	if prev, ok := s.callParts[p.CallID][p.UserID]; ok {
		if cp.JoinedAt == 0 {
			cp.JoinedAt = prev.JoinedAt
		}
		if cp.LeftAt == 0 {
			cp.LeftAt = prev.LeftAt
		}
	}
	s.callParts[p.CallID][p.UserID] = &cp
	return nil
}

func (s *Store) ListParticipants(_ context.Context, callID string) ([]*model.CallParticipant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*model.CallParticipant, 0, len(s.callParts[callID]))
	for _, p := range s.callParts[callID] {
		cp := *p
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserID < out[j].UserID })
	return out, nil
}

func (s *Store) ActiveCallForChat(_ context.Context, chatID string) (*model.Call, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var newest *model.Call
	for _, c := range s.calls {
		if c.ChatID != chatID || c.State == model.CallEnded {
			continue
		}
		if newest == nil || c.CreatedAt > newest.CreatedAt {
			newest = c
		}
	}
	if newest == nil {
		return nil, store.ErrNotFound
	}
	cp := *newest
	return &cp, nil
}
