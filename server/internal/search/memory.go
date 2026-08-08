package search

import (
	"context"
	"sort"
	"strings"
)

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

func (s *memoryBackend) Search(_ context.Context, query string, limit int) ([]Doc, error) {
	tokens := tokenize(query)
	if len(tokens) == 0 {
		return nil, nil
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
			candidates = map[string]struct{}{}
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
		out = append(out, *s.docs[id])
	}
	s.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool { return out[i].Seq > out[j].Seq })
	if limit > 0 && len(out) > limit {
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
