// Package search is the Search Indexer (Section 12). It consumes message events
// off the bus and maintains a full-text index; queries are permission-scoped by
// chat membership so a user can only find messages in chats they belong to.
//
// The index storage is a pluggable Backend: an in-memory inverted index (single
// node) or a shared Postgres tsvector index (visible across all nodes). Secret
// (E2E) chats are never indexed — the server only has ciphertext.
package search

import (
	"context"
	"log/slog"

	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
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
	s.backend.Index(ctx, Doc{
		MessageID: b.MessageID, ChatID: b.ChatID, SenderID: b.SenderID,
		Seq: b.ChatSeq, Text: b.Text, CreatedAt: b.Timestamp,
	})
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

/*
Query returns hits the caller is allowed to see.

The old shape of this function was the bug: it asked the backend for the globally
best `limit*5` matches and then dropped the ones the caller was not a member of,
one IsMember round trip at a time. Two consequences, and the first is worse:

  - WRONG ANSWERS. On a system with more than a few users, the global top hundred
    matches for a common word belong to strangers, so the caller's own two hundred
    matching messages were filtered away to nothing. The search reported "no
    results" for messages it had indexed.
  - N+1. Up to a hundred membership probes per query, nearly all answering "no".

Now the scope travels INTO the backend and is applied before the limit, so the
limit bounds what the caller can see rather than what everyone collectively wrote.
*/
func (s *Service) Query(ctx context.Context, userID, query string, limit int) ([]Result, error) {
	return s.QueryFiltered(ctx, Query{UserID: userID, Text: query, Limit: limit})
}

// QueryFiltered is Query with the optional narrowing filters (one chat, one
// sender) that a client can ask for.
func (s *Service) QueryFiltered(ctx context.Context, q Query) ([]Result, error) {
	if q.Limit <= 0 {
		q.Limit = defaultLimit
	}
	if q.Limit > maxLimit {
		q.Limit = maxLimit
	}
	if q.UserID == "" {
		// No scope is not "search everything". An unscoped query is a bug in the
		// caller, and the safe reading of a bug here is that nothing is visible.
		return nil, nil
	}

	// A named chat is checked against membership rather than trusted. A chat id is
	// guessable, so treating ChatID as a mere filter would turn "search in this
	// chat" into a way to read one.
	if q.ChatID != "" {
		ok, err := s.chats.IsMember(ctx, q.ChatID, q.UserID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
		// With membership established, the single chat IS the scope — no need to
		// enumerate the rest of them.
		q.ChatIDs = []string{q.ChatID}
	} else {
		ids, err := s.chats.UserChatIDs(ctx, q.UserID)
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			return nil, nil // in no chats: nothing to find, and no query to run
		}
		q.ChatIDs = ids
	}

	docs, err := s.backend.Search(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(docs))
	for _, d := range docs {
		// A conversion rather than a positional literal: the two types have the
		// same fields, so a literal silently reorders if either one gains a field —
		// and positional literals give no hint which is which.
		out = append(out, Result(d))
	}
	return out, nil
}
