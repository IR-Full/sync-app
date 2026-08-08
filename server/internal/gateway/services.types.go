package gateway

import (
	"context"

	"github.com/synapse-chat/synapse/internal/auth"
	"github.com/synapse-chat/synapse/internal/media"
	"github.com/synapse-chat/synapse/internal/message"
	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/poll"
	"github.com/synapse-chat/synapse/internal/schedule"
	"github.com/synapse-chat/synapse/internal/search"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// These interfaces are the seam between the realtime gateway and the domain
// services. The gateway depends only on them, never on concrete service types, so
// each service can run EITHER in-process (a *auth.Service, *chat.Service, …) OR as
// a separate process behind a gRPC client that satisfies the same interface. This
// is what turns the modular monolith into a microservice fleet without touching
// the gateway's handler code — cmd/server wires local impls, cmd/gatewayd wires
// gRPC clients, both satisfy these.

// AuthService is the identity/session API the gateway calls.
type AuthService interface {
	Register(ctx context.Context, username, password, displayName, deviceID, platform string) (*model.Session, *model.User, error)
	Login(ctx context.Context, username, password, deviceID, platform string) (*model.Session, *model.User, error)
	Authenticate(ctx context.Context, token string) (*auth.Identity, error)
	Resume(ctx context.Context, resumeToken string) (*auth.Identity, error)
}

// ChatService is the chat/membership API the gateway calls.
type ChatService interface {
	EnsureDirect(ctx context.Context, userA, userB string) (*model.Chat, error)
	FindDirect(ctx context.Context, userA, userB string) (*model.Chat, error)
	Get(ctx context.Context, chatID string) (*model.Chat, error)
	Members(ctx context.Context, chatID string) ([]*model.ChatMember, error)
	// UserChats pages the caller's chat list by chat id (after="" starts at the
	// beginning). It is the only way a fresh install learns which chats it is
	// in — everything else about a chat arrives as a consequence of traffic.
	UserChats(ctx context.Context, userID, after string, limit int) ([]model.ChatSummary, error)
	CreateGroup(ctx context.Context, ownerID, title string, typ model.ChatType, members []string) (*model.Chat, error)
}

// MessageBroker is the unified message-mutation entry point (create/edit/delete).
type MessageBroker interface {
	Submit(ctx context.Context, cmd message.Command) (message.Result, error)
}

// MessageReader is the message read/sync API (history + read receipts).
type MessageReader interface {
	History(ctx context.Context, userID, chatID string, beforeSeq uint64, limit int) ([]*model.Message, error)
	Thread(ctx context.Context, userID, chatID, rootID string, afterSeq uint64, limit int) ([]*model.Message, error)
	Forward(ctx context.Context, userID, srcChatID, srcMsgID, dstChatID, dedupKey string) (*model.Message, bool, error)
	MarkRead(ctx context.Context, userID, chatID string, upToSeq uint64) error
}

// ReactionService toggles emoji reactions on messages (optional).
type ReactionService interface {
	Toggle(ctx context.Context, chatID, messageID, userID, emoji string, now int64) (added bool, counts map[string]int, err error)
}

// PresenceService is the presence/typing API the gateway calls.
type PresenceService interface {
	Online(ctx context.Context, userID string) error
	Heartbeat(ctx context.Context, userID string) error
	Offline(ctx context.Context, userID string) error
	Typing(ctx context.Context, chatID, userID string, active bool) error
}

// MediaService issues upload/download tickets (optional).
type MediaService interface {
	InitUpload(userID, filename, contentType string, size int64) (media.Ticket, error)
	DownloadURL(userID, ref string) (url string, expiresAtMs int64, err error)
}

// SearchService is the full-text query API (optional).
type SearchService interface {
	Query(ctx context.Context, userID, query string, limit int) ([]search.Result, error)
}

// CallService owns call/conference signaling (optional). The server never
// carries media — see internal/call.
type CallService interface {
	Invite(ctx context.Context, chatID, initiatorID, deviceID string, kind model.CallKind, now int64) (*model.Call, error)
	Accept(ctx context.Context, callID, userID, deviceID string, now int64) (*model.Call, error)
	Decline(ctx context.Context, callID, userID string, now int64) error
	Hangup(ctx context.Context, callID, userID string, now int64) error
	Get(ctx context.Context, callID string) (*model.Call, error)
	Participants(ctx context.Context, callID string) ([]*model.CallParticipant, error)
	InCall(ctx context.Context, callID, userID string) (bool, error)
}

// PollService creates polls, records votes, and renders tallies (optional).
type PollService interface {
	Create(ctx context.Context, in poll.CreateInput, now int64) (*model.Poll, error)
	Vote(ctx context.Context, pollID, userID string, option int32, now int64) (*model.Poll, error)
	Close(ctx context.Context, pollID, userID string) (*model.Poll, error)
	Results(ctx context.Context, pollID, userID string) (wire.PollStateBody, error)
}

// ContactService owns the per-user address book and block list (optional).
type ContactService interface {
	Add(ctx context.Context, ownerID, target, name string, now int64) (*model.Contact, error)
	Remove(ctx context.Context, ownerID, target string) error
	Sync(ctx context.Context, ownerID string, since int64) ([]*model.Contact, int64, error)
	SetBlocked(ctx context.Context, ownerID, target string, blocked bool, now int64) error
	// BlocksBetween reports whether either side blocked the other (delivery gate).
	BlocksBetween(ctx context.Context, a, b string) (bool, error)
}

// ScheduleService defers sends and lists/cancels pending ones (optional).
type ScheduleService interface {
	Schedule(ctx context.Context, in schedule.Input, now int64) (*model.ScheduledMessage, error)
	List(ctx context.Context, senderID, chatID string) ([]*model.ScheduledMessage, error)
	Cancel(ctx context.Context, id, senderID string) error
}

// PinService owns pinned messages (chat-wide) and drafts (per-user, synced
// across that user's devices). Optional.
type PinService interface {
	Pin(ctx context.Context, chatID, messageID, userID string, now int64) error
	Unpin(ctx context.Context, chatID, messageID, userID string) error
	ListPins(ctx context.Context, chatID, userID string) ([]*model.PinnedMessage, error)
	SetDraft(ctx context.Context, userID, chatID, text, replyTo string, now int64) error
	SyncDrafts(ctx context.Context, userID string, since int64) ([]*model.Draft, int64, error)
}

// InviteService owns public handles, invite links, and admin rights (optional).
type InviteService interface {
	SetUsername(ctx context.Context, chatID, actorID, username string) error
	CreateLink(ctx context.Context, chatID, actorID string, expiresAt int64, maxUses int32, now int64) (*model.InviteLink, error)
	RevokeLink(ctx context.Context, chatID, actorID, code string) error
	ListLinks(ctx context.Context, chatID, actorID string) ([]*model.InviteLink, error)
	Join(ctx context.Context, code, userID string, now int64) (*model.Chat, error)
	JoinPublic(ctx context.Context, username, userID string, now int64) (*model.Chat, error)
	SetRole(ctx context.Context, chatID, actorID, targetID string, role model.MemberRole) error
}
