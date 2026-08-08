package message

import (
	"context"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/id"
)

// Chats is the chat-authorization dependency the message service needs. It is an
// interface (not *chat.Service) so the service can run against either the local
// chat service OR a gRPC chat client when messaged and chatd are split apart.
type Chats interface {
	CanPost(ctx context.Context, chatID, userID string) (bool, error)
	IsMember(ctx context.Context, chatID, userID string) (bool, error)
}

// Service implements message write/read and read receipts.
type Service struct {
	msgs  store.MessageStore
	reads store.ReadStore
	chats Chats
	bus   eventbus.Bus
	ids   *id.Generator
	media MediaCollector // optional: releases blobs when a message goes
}

// SendInput is a request to post a message.
type SendInput struct {
	SenderID   string
	ChatID     string
	DedupKey   string
	Text       string
	MediaRef   string
	ReplyTo    string
	Attachment *model.Attachment
	// Forward marks this send as a copy of another message (provenance only).
	Forward *model.ForwardOrigin
	// TTLSeconds self-destructs the message that many seconds after it lands.
	TTLSeconds int32
}

// MediaCollector releases the bytes behind a message once nothing points at them.
// Optional: without one, blobs are collected later by the media sweep instead of
// promptly. The service never deletes a blob directly, because a forward carries
// a COPY of the original's ref — "is this still referenced?" is a question only
// the collector (with the message log behind it) can answer.
type MediaCollector interface {
	DeleteIfUnreferenced(ctx context.Context, reason string, refs ...string)
}
