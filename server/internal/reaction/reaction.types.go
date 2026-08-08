package reaction

import (
	"context"

	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/pkg/eventbus"
)

// Chats is the membership check the service needs (interface so it works against
// the local chat service or a gRPC chat client).
type Chats interface {
	IsMember(ctx context.Context, chatID, userID string) (bool, error)
}

// Service applies and broadcasts reactions.
type Service struct {
	store store.ReactionStore
	chats Chats
	bus   eventbus.Bus
}
