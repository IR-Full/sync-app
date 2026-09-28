package search

import (
	"context"
	"log/slog"
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
