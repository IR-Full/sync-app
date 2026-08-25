package call

import (
	"context"

	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
	"github.com/SyncApp-chat/SyncApp/pkg/id"
)

// Chats supplies membership and the participant roster for a chat.
type Chats interface {
	IsMember(ctx context.Context, chatID, userID string) (bool, error)
	MemberIDs(ctx context.Context, chatID string) ([]string, error)
}

// Service manages call rooms and broadcasts their state.
type Service struct {
	store store.CallStore
	chats Chats
	bus   eventbus.Bus
	ids   *id.Generator
}
