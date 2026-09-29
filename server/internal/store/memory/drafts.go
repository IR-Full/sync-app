package memory

import (
	"context"
	"sort"

	"github.com/IR-Full/sync-app/server/internal/model"
)

// DraftStore.

func (s *Store) SetDraft(_ context.Context, d *model.Draft) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.drafts[d.UserID] == nil {
		s.drafts[d.UserID] = map[string]*model.Draft{}
	}
	cp := *d
	s.drafts[d.UserID][d.ChatID] = &cp
	return nil
}

func (s *Store) DeleteDraft(_ context.Context, userID, chatID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.drafts[userID]; m != nil {
		delete(m, chatID)
	}
	return nil
}

func (s *Store) ListDrafts(_ context.Context, userID string, since int64, limit int) ([]*model.Draft, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.Draft
	for _, d := range s.drafts[userID] {
		if d.UpdatedAt <= since {
			continue
		}
		cp := *d
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt < out[j].UpdatedAt })
	return truncate(out, limit), nil
}

// truncate caps a sync page, mirroring the SQL LIMIT the Postgres store applies.
func truncate[T any](rows []T, limit int) []T {
	if limit > 0 && len(rows) > limit {
		return rows[:limit]
	}
	return rows
}
