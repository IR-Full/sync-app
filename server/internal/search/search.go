// Package search is the Search Indexer (Section 12). It consumes message events
// off the bus and maintains a full-text index; queries are permission-filtered
// by chat membership so a user can only find messages in chats they belong to.
//
// The index storage is a pluggable Backend: an in-memory inverted index (single
// node) or a shared Postgres tsvector index (visible across all nodes). Secret
// (E2E) chats are never indexed — the server only has ciphertext.
package search

import (
	"context"
	"log/slog"

	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// New builds the search service over a backend.
func New(backend Backend, chats Chats, log *slog.Logger) *Service {
	return &Service{backend: backend, chats: chats, log: log}
}

// Start subscribes to message events (idempotent handlers).
func (s *Service) Start(bus eventbus.Bus) error {
	if err := bus.Subscribe(eventbus.SubjMessageCreated, "search", s.onUpsert); err != nil {
		return err
	}
	if err := bus.Subscribe(eventbus.SubjMessageEdited, "search", s.onUpsert); err != nil {
		return err
	}
	return bus.Subscribe(eventbus.SubjMessageDeleted, "search", s.onDelete)
}

func (s *Service) onUpsert(ctx context.Context, e eventbus.Event) error {
	var b wire.NewMessageBody
	if err := wire.Unmarshal(e.Data, &b); err != nil {
		return err
	}
	if b.Deleted {
		s.backend.Delete(ctx, b.MessageID)
		return nil
	}
	s.backend.Index(ctx, Doc{MessageID: b.MessageID, ChatID: b.ChatID, SenderID: b.SenderID, Seq: b.ChatSeq, Text: b.Text})
	return nil
}

func (s *Service) onDelete(ctx context.Context, e eventbus.Event) error {
	var b wire.NewMessageBody
	if err := wire.Unmarshal(e.Data, &b); err != nil {
		return err
	}
	s.backend.Delete(ctx, b.MessageID)
	return nil
}

// Query returns permission-filtered hits (only chats the user belongs to),
// recency-ranked. It over-fetches from the backend to survive filtering.
func (s *Service) Query(ctx context.Context, userID, query string, limit int) ([]Result, error) {
	if limit <= 0 {
		limit = 20
	}
	docs, err := s.backend.Search(ctx, query, limit*5)
	if err != nil {
		return nil, err
	}
	memberCache := map[string]bool{}
	var out []Result
	for _, d := range docs {
		ok, cached := memberCache[d.ChatID]
		if !cached {
			m, err := s.chats.IsMember(ctx, d.ChatID, userID)
			if err != nil {
				return nil, err
			}
			ok = m
			memberCache[d.ChatID] = m
		}
		if ok {
			out = append(out, Result{d.MessageID, d.ChatID, d.SenderID, d.Seq, d.Text})
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}
