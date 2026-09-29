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

// Result-set bounds. The limit is what the caller may see, not what the index may
// scan — the scope is applied first, so these numbers are about frame size and
// screen space rather than about work.
const (
	defaultLimit = 20
	maxLimit     = 50
)

// Chats is the membership dependency for permission-scoping a query. An
// interface (not *chat.Service) so search can run against a local chat service or
// a gRPC chat client once split into separate processes.
type Chats interface {
	IsMember(ctx context.Context, chatID, userID string) (bool, error)
	// UserChatIDs lists the chats a user belongs to.
	//
	// Added because the alternative was asking IsMember once per candidate
	// document — and the candidates came from a global ranking, so the answer was
	// usually "no" a hundred times in a row. One lookup that says where the user
	// may look beats a hundred that say where they may not.
	UserChatIDs(ctx context.Context, userID string) ([]string, error)
}

// Doc is one indexed message.
type Doc struct {
	MessageID string
	ChatID    string
	SenderID  string
	Seq       uint64
	Text      string
	CreatedAt int64
}

/*
Query is a permission-SCOPED search request.

The signature used to be `Search(ctx, query string, limit int)`, and the scope was
applied afterwards, in Go, by the service. That is the bug this type exists to
make impossible: the backend returned the globally best `limit*5` matches and the
service then discarded everything the caller could not see, so on any system with
more than a handful of users a search for a common word returned NOTHING — the
asker's own messages were simply not in the global top hundred. Filtering after
the limit is not a slow version of filtering before it; it is a different, wrong
answer.

Both UserID and ChatIDs describe the same permission, because the two backends can
enforce it in different ways and each should use the strongest one available:

  - The Postgres backend joins chat_members on UserID. Exact, unbounded, and
    evaluated before LIMIT by the database.
  - The in-memory backend cannot join anything, so it filters against ChatIDs.

A backend MUST apply at least one of them. A Query with neither set matches
nothing, rather than matching everything — the failure mode of a scope that is
accidentally empty should be an empty result, not a data leak.
*/
type Query struct {
	Text string
	// UserID is whose permissions bound the result. Required.
	UserID string
	// ChatIDs is the resolved membership, for backends that cannot join.
	ChatIDs []string
	// ChatID narrows to one conversation ("search in this chat"). It is still
	// checked against membership — a chat id is guessable, and a filter is not an
	// authorization.
	ChatID string
	// SenderID narrows to messages from one person.
	SenderID string
	Limit    int
}

// Backend stores the index and answers scoped queries.
type Backend interface {
	Index(ctx context.Context, d Doc)
	Delete(ctx context.Context, messageID string)
	// Search returns hits the querying user is allowed to see, ranked best first,
	// at most Limit of them. The scope is applied BEFORE the limit.
	Search(ctx context.Context, q Query) ([]Doc, error)
}

// Service wires the indexing pipeline (bus → backend) and scoped queries.
type Service struct {
	backend Backend
	chats   Chats
	log     *slog.Logger
}

// Result is one search hit.
type Result struct {
	MessageID string `json:"message_id"`
	ChatID    string `json:"chat_id"`
	SenderID  string `json:"sender_id"`
	Seq       uint64 `json:"seq"`
	Text      string `json:"text"`
	CreatedAt int64  `json:"created_at,omitempty"`
}

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
