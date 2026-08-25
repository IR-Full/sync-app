package pin

import (
	"context"

	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
)

// Chats authorizes pinning and membership.
type Chats interface {
	CanPin(ctx context.Context, chatID, userID string) (bool, error)
	IsMember(ctx context.Context, chatID, userID string) (bool, error)
}

// Service manages pins and drafts.
type Service struct {
	pins   store.PinStore
	drafts store.DraftStore
	chats  Chats
	bus    eventbus.Bus
}
