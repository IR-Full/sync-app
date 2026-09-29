package memory

import (
	"context"
	"sort"

	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/store"
)

// MessageStore.

// InsertMessage allocates the per-chat Seq and appends atomically under the
// store lock, so retries dedup without consuming a sequence (no ordering gaps).
// A non-nil mkOb stages the outbox event under the same lock (atomic with the
// insert), mirroring the Postgres transactional outbox.
func (s *Store) InsertMessage(_ context.Context, m *model.Message, dedupKey string, mkOb store.MakeOutbox) (*model.Message, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dk := m.SenderID + "|" + dedupKey
	if dedupKey != "" {
		if existingID, ok := s.dedup[dk]; ok {
			for _, ex := range s.messages[m.ChatID] {
				if ex.ID == existingID {
					cp := *ex
					return &cp, true, nil
				}
			}
		}
	}
	c, ok := s.chats[m.ChatID]
	if !ok {
		return nil, false, store.ErrNotFound
	}
	c.LastSeq++
	cp := *m
	cp.Seq = c.LastSeq
	s.messages[m.ChatID] = append(s.messages[m.ChatID], &cp)
	if m.ThreadRoot != "" {
		for _, ex := range s.messages[m.ChatID] {
			if ex.ID == m.ThreadRoot {
				ex.ReplyCount++
				break
			}
		}
	}
	if dedupKey != "" {
		s.dedup[dk] = m.ID
	}
	out := cp
	s.stageOutboxLocked(mkOb, &out)
	return &out, false, nil
}

// stageOutboxLocked appends an outbox record if mkOb yields one. Caller holds mu.
func (s *Store) stageOutboxLocked(mkOb store.MakeOutbox, m *model.Message) {
	if mkOb == nil {
		return
	}
	if rec := mkOb(m); rec != nil {
		s.outbox = append(s.outbox, *rec)
	}
}

func (s *Store) GetMessage(_ context.Context, chatID, id string) (*model.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.messages[chatID] {
		if m.ID == id {
			cp := *m
			return &cp, nil
		}
	}
	return nil, store.ErrNotFound
}

func (s *Store) EditMessage(_ context.Context, chatID, id, text string, at int64, mkOb store.MakeOutbox) (*model.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.messages[chatID] {
		if m.ID == id {
			m.Text = text
			m.Edited = true
			m.EditedAt = at
			cp := *m
			s.stageOutboxLocked(mkOb, &cp)
			return &cp, nil
		}
	}
	return nil, store.ErrNotFound
}

func (s *Store) DeleteMessage(_ context.Context, chatID, id string, at int64, mkOb store.MakeOutbox) (*model.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.messages[chatID] {
		if m.ID == id {
			m.Deleted = true
			m.Text = ""
			m.MediaRef = ""
			m.Attachment = nil
			m.EditedAt = at
			cp := *m
			s.stageOutboxLocked(mkOb, &cp)
			return &cp, nil
		}
	}
	return nil, store.ErrNotFound
}

// Poll returns up to limit unsent outbox records (FIFO).
func (s *Store) Poll(_ context.Context, limit int) ([]store.OutboxRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.OutboxRecord
	for _, rec := range s.outbox {
		if s.outboxSent[rec.ID] {
			continue
		}
		out = append(out, rec)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// PurgeSent drops sent records the prefix compaction could not reach (a sent
// record sitting behind an unsent one). The durable store is where retention
// actually matters; this keeps the two implementations honest about the contract.
func (s *Store) PurgeSent(_ context.Context, _ int64, limit int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := make([]store.OutboxRecord, 0, len(s.outbox))
	n := 0
	for _, rec := range s.outbox {
		if n < limit && s.outboxSent[rec.ID] {
			delete(s.outboxSent, rec.ID)
			n++
			continue
		}
		kept = append(kept, rec)
	}
	s.outbox = kept
	return n, nil
}

// MarkSent marks records delivered and compacts the fully-drained prefix.
func (s *Store) MarkSent(_ context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		s.outboxSent[id] = true
	}
	// Compact: drop leading records that are all sent.
	i := 0
	for i < len(s.outbox) && s.outboxSent[s.outbox[i].ID] {
		delete(s.outboxSent, s.outbox[i].ID)
		i++
	}
	s.outbox = s.outbox[i:]
	return nil
}

// AvatarRefExists reports whether a blob is somebody's profile picture
// (AvatarRefFinder).
func (s *Store) AvatarRefExists(_ context.Context, ref string) (bool, error) {
	if ref == "" {
		return false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		if u.AvatarRef == ref {
			return true, nil
		}
	}
	return false, nil
}

// MediaRefChats reports the chats a blob is reachable from (MediaChatResolver).
// Deduplicated: a chat carrying the same ref twice is still one chat.
func (s *Store) MediaRefChats(_ context.Context, ref string, limit int) ([]string, error) {
	if ref == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 32
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := make(map[string]bool)
	out := make([]string, 0, limit)
	for chatID, msgs := range s.messages {
		if seen[chatID] {
			continue
		}
		for _, m := range msgs {
			if m.Deleted {
				continue
			}
			hit := m.MediaRef == ref ||
				(m.Attachment != nil && (m.Attachment.MediaRef == ref || m.Attachment.ThumbRef == ref))
			if !hit {
				continue
			}
			seen[chatID] = true
			out = append(out, chatID)
			break
		}
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// MediaRefExists mirrors the Postgres capability: is this blob still reachable
// from a live message (its own ref, or an attachment's)?
func (s *Store) MediaRefExists(_ context.Context, ref string) (bool, error) {
	if ref == "" {
		return false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, msgs := range s.messages {
		for _, m := range msgs {
			if m.Deleted {
				continue
			}
			if m.MediaRef == ref {
				return true, nil
			}
			if m.Attachment != nil && (m.Attachment.MediaRef == ref || m.Attachment.ThumbRef == ref) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (s *Store) History(_ context.Context, chatID string, beforeSeq uint64, limit int) ([]*model.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := s.messages[chatID]
	// Copy + sort ascending by Seq, then select the window newest-first.
	tmp := make([]*model.Message, 0, len(all))
	for _, m := range all {
		if beforeSeq == 0 || m.Seq < beforeSeq {
			cp := *m
			tmp = append(tmp, &cp)
		}
	}
	sort.Slice(tmp, func(i, j int) bool { return tmp[i].Seq > tmp[j].Seq })
	if limit > 0 && len(tmp) > limit {
		tmp = tmp[:limit]
	}
	return tmp, nil
}
