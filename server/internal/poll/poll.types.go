package poll

import (
	"context"

	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/id"
)

// Chats supplies the membership check.
type Chats interface {
	IsMember(ctx context.Context, chatID, userID string) (bool, error)
}

// Service creates polls, records votes, and broadcasts tallies.
type Service struct {
	store store.PollStore
	chats Chats
	bus   eventbus.Bus
	ids   *id.Generator
}

// CreateInput describes a new poll. MessageID is the message that carries the
// question (created by the caller through the normal write path).
type CreateInput struct {
	ChatID      string
	MessageID   string
	CreatorID   string
	Question    string
	Options     []string
	MultiChoice bool
	Anonymous   bool
}
