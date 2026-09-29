package memory

import (
	"context"
	"sort"

	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/store"
)

// ScheduleStore.

func (s *Store) CreateScheduled(_ context.Context, m *model.ScheduledMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *m
	s.scheduled[m.ID] = &cp
	return nil
}

func (s *Store) CancelScheduled(_ context.Context, id, senderID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.scheduled[id]
	if !ok || m.SenderID != senderID || m.Sent {
		return store.ErrNotFound
	}
	delete(s.scheduled, id)
	return nil
}

func (s *Store) ListScheduled(_ context.Context, senderID, chatID string) ([]*model.ScheduledMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.ScheduledMessage
	for _, m := range s.scheduled {
		if m.SenderID != senderID || m.ChatID != chatID || m.Sent {
			continue
		}
		cp := *m
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SendAt < out[j].SendAt })
	return out, nil
}

// PurgeSentScheduled drops fired rows past their retention window (see the
// Postgres implementation for why they are not kept).
func (s *Store) PurgeSentScheduled(_ context.Context, before int64, limit int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, m := range s.scheduled {
		if n >= limit {
			break
		}
		if m.Sent && m.SendAt < before {
			delete(s.scheduled, id)
			n++
		}
	}
	return n, nil
}

func (s *Store) ClaimDueScheduled(_ context.Context, now int64, limit int) ([]*model.ScheduledMessage, error) {
	if limit <= 0 {
		limit = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*model.ScheduledMessage
	for _, m := range s.scheduled {
		if m.Sent || m.SendAt > now {
			continue
		}
		m.Sent = true // claimed under the lock → never dispatched twice
		cp := *m
		out = append(out, &cp)
		if len(out) >= limit {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SendAt < out[j].SendAt })
	return out, nil
}

// ExpireMessages tombstones self-destructed messages (Expirer).
func (s *Store) ExpireMessages(_ context.Context, now int64, limit int) ([]*model.Message, error) {
	if limit <= 0 {
		limit = 200
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*model.Message
	for _, msgs := range s.messages {
		for _, m := range msgs {
			if m.ExpiresAt == 0 || m.ExpiresAt > now || m.Deleted {
				continue
			}
			m.Deleted, m.Text, m.MediaRef, m.Attachment, m.EditedAt = true, "", "", nil, now
			cp := *m
			out = append(out, &cp)
			if len(out) >= limit {
				return out, nil
			}
		}
	}
	return out, nil
}
