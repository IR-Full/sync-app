package search

import (
	"context"
	"log/slog"
)

// Chats is the membership dependency for permission-filtering hits. An interface
// (not *chat.Service) so search can run against a local chat service or a gRPC
// chat client once split into separate processes.
type Chats interface {
	IsMember(ctx context.Context, chatID, userID string) (bool, error)
}

// Doc is one indexed message.
type Doc struct {
	MessageID string
	ChatID    string
	SenderID  string
	Seq       uint64
	Text      string
}

// Backend stores the index. Search returns candidate docs (recency-ranked),
// before permission filtering.
type Backend interface {
	Index(ctx context.Context, d Doc)
	Delete(ctx context.Context, messageID string)
	Search(ctx context.Context, query string, limit int) ([]Doc, error)
}

// Service wires the indexing pipeline (bus → backend) and permission-filtered
// queries.
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
}
