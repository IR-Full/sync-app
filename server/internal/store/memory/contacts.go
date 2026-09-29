package memory

import (
	"context"
	"sort"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
)

// ContactStore.

func (s *Store) UpsertContact(_ context.Context, c *model.Contact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.contacts[c.OwnerID] == nil {
		s.contacts[c.OwnerID] = map[string]*model.Contact{}
	}
	cp := *c
	if prev, ok := s.contacts[c.OwnerID][c.UserID]; ok {
		cp.CreatedAt = prev.CreatedAt // keep the original add time
		cp.Blocked = prev.Blocked     // naming a contact must not unblock them
	}
	s.contacts[c.OwnerID][c.UserID] = &cp
	return nil
}

func (s *Store) DeleteContact(_ context.Context, ownerID, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.contacts[ownerID]; m != nil {
		delete(m, userID)
	}
	return nil
}

func (s *Store) GetContact(_ context.Context, ownerID, userID string) (*model.Contact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.contacts[ownerID][userID]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (s *Store) ListContacts(_ context.Context, ownerID string, since int64, limit int) ([]*model.Contact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.Contact
	for _, c := range s.contacts[ownerID] {
		if c.UpdatedAt <= since {
			continue
		}
		cp := *c
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt < out[j].UpdatedAt })
	return truncate(out, limit), nil
}

func (s *Store) SetBlocked(_ context.Context, ownerID, userID string, blocked bool, at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.contacts[ownerID] == nil {
		s.contacts[ownerID] = map[string]*model.Contact{}
	}
	c, ok := s.contacts[ownerID][userID]
	if !ok {
		c = &model.Contact{OwnerID: ownerID, UserID: userID, CreatedAt: at}
		s.contacts[ownerID][userID] = c
	}
	c.Blocked = blocked
	c.UpdatedAt = at
	return nil
}

func (s *Store) IsBlocked(_ context.Context, ownerID, userID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.contacts[ownerID][userID]
	return ok && c.Blocked, nil
}
