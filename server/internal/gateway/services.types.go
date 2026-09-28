package gateway

import (
	"context"

	"github.com/SyncApp-chat/SyncApp/internal/auth"
	"github.com/SyncApp-chat/SyncApp/internal/billing"
	"github.com/SyncApp-chat/SyncApp/internal/chat"
	"github.com/SyncApp-chat/SyncApp/internal/media"
	"github.com/SyncApp-chat/SyncApp/internal/message"
	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/poll"
	"github.com/SyncApp-chat/SyncApp/internal/schedule"
	"github.com/SyncApp-chat/SyncApp/internal/search"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
)

// These interfaces are the seam between the realtime gateway and the domain
// services. The gateway depends only on them, never on concrete service types, so
// each service can run EITHER in-process (a *auth.Service, *chat.Service, …) OR as
// a separate process behind a gRPC client that satisfies the same interface. This
// is what turns the modular monolith into a microservice fleet without touching
// the gateway's handler code — cmd/server wires local impls, cmd/gatewayd wires
// gRPC clients, both satisfy these.

// AuthService is the identity/session API the gateway calls.
//
// The session-management half is part of THIS interface rather than an optional
// capability discovered by type assertion, and that is deliberate. A capability
// the monolith has and the gRPC client does not is a feature that vanishes when
// somebody enables the split — the topology drift the risk register names. Being
// on the interface makes forgetting it a compile error in both wirings.
type AuthService interface {
	Register(ctx context.Context, username, password, displayName, deviceID, platform string) (*model.Session, *model.User, error)
	Login(ctx context.Context, username, password, deviceID, platform string) (*model.Session, *model.User, error)
	Authenticate(ctx context.Context, token string) (*auth.Identity, error)
	Resume(ctx context.Context, resumeToken string) (*auth.Identity, error)

	// ListSessions returns the user's LIVE sessions (revoked and expired filtered
	// out): "where am I signed in".
	ListSessions(ctx context.Context, userID string) ([]*model.Session, error)
	// RevokeOwned kills one session, but only if it belongs to userID — a session
	// id is not a secret, so an unscoped revoke would let any account sign out any
	// other. Returns store.ErrNotFound for both "no such session" and "not yours",
	// so the id space is not an existence oracle.
	RevokeOwned(ctx context.Context, userID, sessionID string) error
	// RevokeAll kills every live session, sparing keepSessionID (pass "" for none).
	// Returns how many were actually killed.
	RevokeAll(ctx context.Context, userID, keepSessionID string) (int, error)
	// DeleteAccount erases the account after re-confirming the password.
	DeleteAccount(ctx context.Context, userID, password string) error

	// LoginWithCode is Login plus a second factor. On the interface rather than
	// discovered by assertion, for the reason stated above: a capability the
	// monolith has and the gRPC wiring does not is a feature that vanishes when
	// somebody enables the split.
	LoginWithCode(ctx context.Context, username, password, code, deviceID, platform string) (*model.Session, *model.User, error)
	// ChangePassword replaces the password after re-confirming the old one, then
	// revokes every other session. Returns how many were killed.
	//
	// Until this existed a password could not be changed at all, so a leaked one
	// meant a permanently lost account: revoking sessions does not stop whoever
	// knows the password from signing in again.
	ChangePassword(ctx context.Context, userID, oldPassword, newPassword, keepSessionID string) (int, error)

	// The second factor. BeginTOTP mints a secret; ConfirmTOTP enforces it and
	// returns the recovery codes once; DisableTOTP needs the password AND a code.
	BeginTOTP(ctx context.Context, userID, issuer string) (secret, uri string, err error)
	ConfirmTOTP(ctx context.Context, userID, code string) ([]string, error)
	DisableTOTP(ctx context.Context, userID, password, code string) error
	TwoFactorEnabled(ctx context.Context, userID string) bool
	RecoveryCodesLeft(ctx context.Context, userID string) int
}

// ChatService is the chat/membership API the gateway calls.
type ChatService interface {
	EnsureDirect(ctx context.Context, userA, userB string) (*model.Chat, error)
	// EnsureSecret returns the canonical SECRET chat for a pair. It coexists with
	// the ordinary direct chat with that person — choosing the secret one is the
	// whole point of having it.
	EnsureSecret(ctx context.Context, userA, userB string) (*model.Chat, error)
	FindDirect(ctx context.Context, userA, userB string) (*model.Chat, error)
	Get(ctx context.Context, chatID string) (*model.Chat, error)
	Members(ctx context.Context, chatID string) ([]*model.ChatMember, error)
	// IsMember answers one membership question without materialising a roster.
	// The distinction matters for a channel: Members loads everyone, which is the
	// wrong shape for "may this one person see this one thing".
	IsMember(ctx context.Context, chatID, userID string) (bool, error)
	// UserChats pages the caller's chat list. It is the only way a fresh install
	// learns which chats it is in — everything else about a chat arrives as a
	// consequence of traffic.
	UserChats(ctx context.Context, userID, after string, limit int) ([]model.ChatSummary, error)
	// UserChatPage is UserChats with the cursor and filters a real list needs:
	// activity order, an archived pile, and a composite cursor that survives the
	// list reordering underneath it.
	UserChatPage(ctx context.Context, userID string, p chat.ChatPage) ([]model.ChatSummary, error)
	// SetChatFlags writes the caller's OWN mute/pin/archive for a chat. Scoped by
	// (chat, user), so there is no shape of the call that touches another member's
	// row — these are one person's settings about a shared conversation.
	SetChatFlags(ctx context.Context, chatID, userID string, f model.MemberFlags) (model.MemberFlags, error)
	// CountPinnedChats backs the MaxPinnedChats entitlement. The ceiling is applied
	// at the gateway rather than in the chat service, because only the gateway
	// knows the caller's tier — the chat service has no business importing billing.
	CountPinnedChatsExcept(ctx context.Context, userID, exceptChatID string) (int, error)
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
	InitUpload(userID, filename, contentType string, size, limit int64) (media.Ticket, error)
	DownloadURL(userID, ref string) (url string, expiresAtMs int64, err error)
}

// SearchService is the full-text query API (optional).
type SearchService interface {
	Query(ctx context.Context, userID, query string, limit int) ([]search.Result, error)
	// QueryFiltered adds the optional narrowing a client can ask for (one chat,
	// one sender). It is on the interface rather than discovered by type assertion
	// for the reason stated on AuthService: a capability the monolith has and the
	// gRPC wiring does not is a feature that disappears when somebody enables the
	// split.
	QueryFiltered(ctx context.Context, q search.Query) ([]search.Result, error)
}

// BillingService owns subscriptions and entitlements (optional).
//
// Optional, and its absence is a coherent deployment rather than a broken one:
// everybody is on the free tier, which is a tier. The gateway reads entitlements on
// several paths, so the degradation has to be a real answer and not an error.
type BillingService interface {
	// SellsTiers reports whether an acquirer is configured. False means this
	// deployment has no tiers at all rather than a free one — see the comment on
	// model.UngatedEntitlements.
	SellsTiers() bool
	Plans(country string) []billing.PlanOffer
	Checkout(ctx context.Context, req billing.CheckoutRequest) (*billing.Checkout, error)
	Subscription(ctx context.Context, userID string) *model.Subscription
	Entitlements(ctx context.Context, userID string) model.Entitlements
	Cancel(ctx context.Context, userID string) (*model.Subscription, error)
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
	// IsContact reports whether userID is in ownerID's address book — the check
	// behind a "visible to my contacts" privacy setting. Asked in that direction
	// deliberately: the owner's list decides, not the viewer's.
	IsContact(ctx context.Context, ownerID, userID string) (bool, error)
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
