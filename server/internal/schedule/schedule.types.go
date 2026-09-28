package schedule

import (
	"context"
	"log/slog"

	"github.com/SyncApp-chat/SyncApp/internal/message"
	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/pkg/id"
)

// Chats authorizes posting into the destination chat.
type Chats interface {
	CanPost(ctx context.Context, chatID, userID string) (bool, error)
}

// BlockGate reports whether a block between the two people in a 1:1 chat
// refuses this sender's message. Optional: without one, only CanPost is checked.
type BlockGate interface {
	Blocked(ctx context.Context, chatID, senderID string) (bool, error)
}

// Sender delivers a due message through the normal write path (so it gets a seq,
// an outbox event, and fanout exactly like a live send).
type Sender interface {
	Send(ctx context.Context, in message.SendInput) (*model.Message, bool, error)
	ExpireDue(ctx context.Context, now int64, limit int) (int, error)
}

// Service schedules sends and runs the dispatcher + expiry reaper.
type Service struct {
	store  store.ScheduleStore
	chats  Chats
	sender Sender
	blocks BlockGate
	ids    *id.Generator
	log    *slog.Logger
}

// Input describes a deferred send.
type Input struct {
	ChatID     string
	SenderID   string
	Text       string
	MediaRef   string
	Attachment *model.Attachment
	ReplyTo    string
	TTLSeconds int32
	SendAt     int64 // unix millis
}
