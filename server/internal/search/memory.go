package search

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// maxDocs bounds the in-memory index so it cannot grow without limit. Once full,
// the oldest indexed message is evicted (FIFO). Production uses the Postgres
// backend (shared across nodes) and drops this cap.
const maxDocs = 100_000

// memoryBackend is an in-process inverted index (single node / dev / tests).
type memoryBackend struct {
	mu       sync.RWMutex
	docs     map[string]*Doc
	inverted map[string]map[string]struct{}
	order    []string
}

// NewMemoryBackend returns an in-process search backend.
func NewMemoryBackend() Backend {
	return &memoryBackend{docs: map[string]*Doc{}, inverted: map[string]map[string]struct{}{}}
}

func (s *memoryBackend) Index(_ context.Context, d Doc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, existing := s.docs[d.MessageID]
	if existing {
		s.removeTokensLocked(s.docs[d.MessageID])
	}
	cp := d
	s.docs[d.MessageID] = &cp
	for _, tok := range tokenize(d.Text) {
		if s.inverted[tok] == nil {
			s.inverted[tok] = map[string]struct{}{}
		}
		s.inverted[tok][d.MessageID] = struct{}{}
	}
	if !existing {
		s.order = append(s.order, d.MessageID)
		for len(s.docs) > maxDocs && len(s.order) > 0 {
			oldest := s.order[0]
			s.order = s.order[1:]
			if od, ok := s.docs[oldest]; ok {
				s.removeTokensLocked(od)
				delete(s.docs, oldest)
			}
		}
	}
}

func (s *memoryBackend) Delete(_ context.Context, messageID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.docs[messageID]; ok {
		s.removeTokensLocked(d)
		delete(s.docs, messageID)
	}
}

func (s *memoryBackend) removeTokensLocked(d *Doc) {
	for _, tok := range tokenize(d.Text) {
		if set := s.inverted[tok]; set != nil {
			delete(set, d.MessageID)
			if len(set) == 0 {
				delete(s.inverted, tok)
			}
		}
	}
}

func (s *memoryBackend) Search(_ context.Context, q Query) ([]Doc, error) {
	tokens := tokenize(q.Text)
	if len(tokens) == 0 {
		return nil, nil
	}
	// The scope. This backend cannot join a membership table, so it filters against
	// the ids the service resolved — but it filters BEFORE the limit, which is the
	// property that matters and the one the previous design got wrong.
	//
	// An empty scope matches nothing rather than everything: a scope that came out
	// empty by accident should produce an empty result, not the whole index.
	if len(q.ChatIDs) == 0 {
		return nil, nil
	}
	allowed := make(map[string]struct{}, len(q.ChatIDs))
	for _, id := range q.ChatIDs {
		allowed[id] = struct{}{}
	}

	limit := q.Limit
	if limit <= 0 {
		limit = defaultLimit
	}

	s.mu.RLock()
	var candidates map[string]struct{}
	for i, tok := range tokens {
		set := s.inverted[tok]
		if set == nil {
			s.mu.RUnlock()
			return nil, nil
		}
		if i == 0 {
			candidates = make(map[string]struct{}, len(set))
			for id := range set {
				candidates[id] = struct{}{}
			}
		} else {
			for id := range candidates {
				if _, ok := set[id]; !ok {
					delete(candidates, id)
				}
			}
		}
	}
	out := make([]Doc, 0, len(candidates))
	for id := range candidates {
		d := s.docs[id]
		if d == nil {
			continue
		}
		if _, ok := allowed[d.ChatID]; !ok {
			continue
		}
		if q.ChatID != "" && d.ChatID != q.ChatID {
			continue
		}
		if q.SenderID != "" && d.SenderID != q.SenderID {
			continue
		}
		out = append(out, *d)
	}
	s.mu.RUnlock()

	// Recency by wall clock, then by id. Sorting by Seq ranked a chat with a
	// million messages above every other chat, because Seq counts within a chat
	// and says nothing across them.
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].MessageID > out[j].MessageID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// tokenize lowercases and splits text into word tokens (ascii alnum), deduped.
func tokenize(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r < 128
	})
	seen := map[string]struct{}{}
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) < 2 {
			continue
		}
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	return out
}
