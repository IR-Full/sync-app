// Package store defines the persistence contracts each service owns. Splitting
// by aggregate (users, sessions, chats, messages, read-state) mirrors the
// service/database ownership boundaries in Section 6/8: in production these back
// onto different engines (Postgres for metadata, a wide-column store for the
// message log, Redis for presence). The interfaces let us start Postgres-only
// and swap the message store later without touching business logic.
package store

import (
	"context"

	"github.com/synapse-chat/synapse/internal/model"
)

// UserStore owns accounts and devices (User Service data).
type UserStore interface {
	CreateUser(ctx context.Context, u *model.User) error
	GetUser(ctx context.Context, id string) (*model.User, error)
	GetUserByUsername(ctx context.Context, username string) (*model.User, error)

	// UpdateProfile writes the mutable, PUBLIC parts of an account (display name
	// and avatar reference). It takes final values rather than a patch: the
	// caller has already read the user to decide what "leave unchanged" means,
	// and a store that guessed from empty strings could never clear a field.
	UpdateProfile(ctx context.Context, userID, displayName, avatarRef string) error

	UpsertDevice(ctx context.Context, d *model.Device) error
	GetDevice(ctx context.Context, id string) (*model.Device, error)
	ListDevices(ctx context.Context, userID string) ([]*model.Device, error)
	// SetPushToken registers (or clears, with "") a device's push token. Scoped by
	// owner: the device id is client-asserted, so an unscoped write would let one
	// account point another account's notifications at its own token.
	SetPushToken(ctx context.Context, userID, deviceID, token string) error
}

// SessionStore owns login sessions (Session Service data).
type SessionStore interface {
	CreateSession(ctx context.Context, s *model.Session) error
	GetSessionByToken(ctx context.Context, token string) (*model.Session, error)
	GetSessionByResumeToken(ctx context.Context, resume string) (*model.Session, error)
	RevokeSession(ctx context.Context, id string, at int64) error
	ListSessions(ctx context.Context, userID string) ([]*model.Session, error)
}

// ChatStore owns chats and membership (Chat Service data). BumpSeq atomically
// allocates the next per-chat sequence — the single source of message ordering.
type ChatStore interface {
	CreateChat(ctx context.Context, c *model.Chat) error
	GetChat(ctx context.Context, id string) (*model.Chat, error)
	// GetOrCreateDirect returns the canonical 1:1 chat for a user pair, creating
	// it if absent (idempotent on the unordered pair).
	GetOrCreateDirect(ctx context.Context, userA, userB string, newID string) (*model.Chat, error)
	// GetDirect returns the existing 1:1 chat for a pair, or ErrNotFound. It never
	// creates — used to distinguish "message an existing chat" from "start a new
	// one" so only new-chat creation is rate-limited.
	GetDirect(ctx context.Context, userA, userB string) (*model.Chat, error)

	AddMember(ctx context.Context, m *model.ChatMember) error
	RemoveMember(ctx context.Context, chatID, userID string) error
	ListMembers(ctx context.Context, chatID string) ([]*model.ChatMember, error)
	// ListMemberIDsPage returns member ids after `afterUserID` (empty = from the
	// start), ordered, at most `limit`. Keyset paging, not OFFSET: a channel can
	// have millions of members, and the only way to walk them without holding the
	// whole set in memory is to remember the last id and ask for the next page.
	ListMemberIDsPage(ctx context.Context, chatID, afterUserID string, limit int) ([]string, error)
	// ListMembersPage is the same walk with roles attached, used to fill (and
	// bound) the authorization cache.
	ListMembersPage(ctx context.Context, chatID, afterUserID string, limit int) ([]*model.ChatMember, error)
	// GetMember reads ONE membership row. Authorization asks about a single user,
	// so on a chat too large to cache wholesale this is the query that answers it —
	// a primary-key probe instead of a scan of the membership.
	GetMember(ctx context.Context, chatID, userID string) (*model.ChatMember, error)
	// CountMembersWithRole counts holders of a role. "Is this the last owner?" is a
	// counting question; answering it by listing everyone makes the cost of
	// demoting someone depend on how popular their chat is.
	CountMembersWithRole(ctx context.Context, chatID string, role model.MemberRole) (int, error)
	ListUserChats(ctx context.Context, userID string) ([]string, error)
	IsMember(ctx context.Context, chatID, userID string) (bool, error)

	// BumpSeq allocates and returns the next sequence for a chat atomically.
	BumpSeq(ctx context.Context, chatID string) (uint64, error)
}

// OutboxRecord is a domain event staged for the event bus. It is written in the
// SAME transaction as the message that produced it (transactional outbox), so a
// crash between the DB commit and the bus publish cannot lose the event — a
// relay replays anything not yet marked sent. This turns delivery into a durable
// at-least-once path instead of best-effort.
type OutboxRecord struct {
	ID      string
	Subject string
	Key     string
	Data    []byte
	Trace   map[string]string // W3C trace context, carried to the bus by the relay
}

// MakeOutbox builds the outbox record for a just-persisted message. The store
// invokes it INSIDE the write transaction, after the row (and its Seq) exist, so
// the event body can include the final Seq. Returning nil stages no event.
type MakeOutbox func(stored *model.Message) *OutboxRecord

// MessageStore owns the message log (Message Write/Read Service data). It is the
// append-heavy hot path; in production this is the wide-column store.
type MessageStore interface {
	// InsertMessage persists a message. Implementations must enforce idempotency
	// on (SenderID, dedupKey): a repeat returns the already-stored message with
	// dup=true instead of inserting a second row. When not a duplicate and mkOb is
	// non-nil, the returned outbox record is persisted atomically with the message.
	InsertMessage(ctx context.Context, m *model.Message, dedupKey string, mkOb MakeOutbox) (stored *model.Message, dup bool, err error)
	GetMessage(ctx context.Context, chatID, id string) (*model.Message, error)
	EditMessage(ctx context.Context, chatID, id, text string, at int64, mkOb MakeOutbox) (*model.Message, error)
	DeleteMessage(ctx context.Context, chatID, id string, at int64, mkOb MakeOutbox) (*model.Message, error)
	// History returns messages with Seq < beforeSeq (beforeSeq==0 means latest),
	// newest first, up to limit.
	History(ctx context.Context, chatID string, beforeSeq uint64, limit int) ([]*model.Message, error)
}

// OutboxStore is drained by the relay: it reads unsent records and marks them
// sent once published to the bus.
type OutboxStore interface {
	Poll(ctx context.Context, limit int) ([]OutboxRecord, error)
	MarkSent(ctx context.Context, ids []string) error
	// PurgeSent removes records published before `before` (unix millis), at most
	// `limit` per call, returning how many went.
	//
	// A staged event is a COPY of the message it announces, so without collection
	// the outbox becomes a second, permanent message log — the handoff table ends
	// up larger than the data it was meant to hand off. Marking a row sent is not
	// enough; something has to delete it.
	PurgeSent(ctx context.Context, before int64, limit int) (int, error)
}

// ReactionStore owns emoji reactions on messages.
type ReactionStore interface {
	// SetReaction applies toggle semantics: reacting with the emoji the user
	// already has REMOVES it (added=false); any other emoji replaces the user's
	// previous reaction (added=true). One reaction per (message, user).
	SetReaction(ctx context.Context, r *model.Reaction) (added bool, err error)
	// ListReactions returns every reaction on a message.
	ListReactions(ctx context.Context, chatID, messageID string) ([]*model.Reaction, error)
}

// ReadStore owns per-user read cursors (Message Read Service data).
type ReadStore interface {
	SetRead(ctx context.Context, rs *model.ReadState) error
	GetRead(ctx context.Context, chatID, userID string) (*model.ReadState, error)
}

// Stores bundles the aggregate stores for convenient wiring.
type Stores struct {
	Users     UserStore
	Sessions  SessionStore
	Chats     ChatStore
	Messages  MessageStore
	Reads     ReadStore
	Reactions ReactionStore
	Calls     CallStore
	Polls     PollStore
	Contacts  ContactStore
	Schedule  ScheduleStore
	Pins      PinStore
	Drafts    DraftStore
	Invites   InviteStore
	Outbox    OutboxStore
}

// ThreadReader is an optional MessageStore capability: fetching a reply branch
// under a thread root. Backends that cannot index threads simply do not
// implement it (the service reports "not found" rather than failing the build).
type ThreadReader interface {
	Thread(ctx context.Context, chatID, rootID string, afterSeq uint64, limit int) ([]*model.Message, error)
}

// CallStore owns call rooms and their participants (Call Service data). Calls are
// low-volume compared to messages, so they live with the metadata store; keeping
// them durable also gives call history for free.
type CallStore interface {
	CreateCall(ctx context.Context, c *model.Call) error
	GetCall(ctx context.Context, id string) (*model.Call, error)
	SetCallState(ctx context.Context, id string, state model.CallState, at int64) error
	UpsertParticipant(ctx context.Context, p *model.CallParticipant) error
	ListParticipants(ctx context.Context, callID string) ([]*model.CallParticipant, error)
	// ActiveCallForChat returns the chat's in-progress call (ringing or active),
	// so a second caller joins the existing room instead of starting a rival one.
	ActiveCallForChat(ctx context.Context, chatID string) (*model.Call, error)
}

// PollStore owns polls and their votes.
type PollStore interface {
	CreatePoll(ctx context.Context, p *model.Poll) error
	GetPoll(ctx context.Context, id string) (*model.Poll, error)
	GetPollByMessage(ctx context.Context, messageID string) (*model.Poll, error)
	ClosePoll(ctx context.Context, id string) error
	// Vote records a choice. Single-choice polls replace the voter's previous
	// pick; multi-choice polls toggle the given option. Returns whether the
	// option ended up selected.
	Vote(ctx context.Context, v *model.PollVote, multiChoice bool) (selected bool, err error)
	// Tally returns vote counts per option index.
	Tally(ctx context.Context, pollID string) (map[int32]int, error)
	// VotedOptions returns the option indexes a user picked (for their own UI).
	VotedOptions(ctx context.Context, pollID, userID string) ([]int32, error)
}

// ContactStore owns per-user address books and block lists.
type ContactStore interface {
	UpsertContact(ctx context.Context, c *model.Contact) error
	DeleteContact(ctx context.Context, ownerID, userID string) error
	GetContact(ctx context.Context, ownerID, userID string) (*model.Contact, error)
	// ListContacts returns the owner's contacts changed since a timestamp
	// (since=0 = everything), which is what makes incremental sync possible.
	// It returns at most `limit` rows, oldest change first. Callers ask for one
	// row more than they intend to deliver: that extra row is how the service
	// sees a page boundary and cuts on a timestamp group (see contact.Sync).
	ListContacts(ctx context.Context, ownerID string, since int64, limit int) ([]*model.Contact, error)
	// SetBlocked flips the block flag, creating the row if needed (you can block
	// someone who was never a contact).
	SetBlocked(ctx context.Context, ownerID, userID string, blocked bool, at int64) error
	// IsBlocked reports whether owner blocked user — the check the message path
	// consults before delivering.
	IsBlocked(ctx context.Context, ownerID, userID string) (bool, error)
}

// ScheduleStore owns pending (not yet sent) messages.
type ScheduleStore interface {
	CreateScheduled(ctx context.Context, m *model.ScheduledMessage) error
	CancelScheduled(ctx context.Context, id, senderID string) error
	ListScheduled(ctx context.Context, senderID, chatID string) ([]*model.ScheduledMessage, error)
	// ClaimDueScheduled atomically marks due messages as sent and returns them, so
	// concurrent dispatchers never send the same message twice.
	ClaimDueScheduled(ctx context.Context, now int64, limit int) ([]*model.ScheduledMessage, error)
	// PurgeSentScheduled removes fired rows older than `before`. A sent row is
	// kept only as a short audit trail of "this went out"; the message itself
	// lives in the message log from that moment on.
	PurgeSentScheduled(ctx context.Context, before int64, limit int) (int, error)
}

// Expirer is an optional MessageStore capability: tombstoning self-destructed
// messages. Backends without it simply never expire.
type Expirer interface {
	ExpireMessages(ctx context.Context, now int64, limit int) ([]*model.Message, error)
}

// MediaReferencer is an optional MessageStore capability: reporting whether a
// media ref is still reachable from a LIVE message. The media collector needs
// it, and only the message log can answer it — a blob may be shared by a forward
// long after its original was deleted.
type MediaReferencer interface {
	MediaRefExists(ctx context.Context, ref string) (bool, error)
}

// PinStore owns pinned messages (chat-wide).
type PinStore interface {
	Pin(ctx context.Context, p *model.PinnedMessage) error
	Unpin(ctx context.Context, chatID, messageID string) error
	ListPins(ctx context.Context, chatID string) ([]*model.PinnedMessage, error)
}

// DraftStore owns per-user, per-chat unsent text, synced across the user's own
// devices. ListDrafts(since) powers incremental sync like contacts.
type DraftStore interface {
	SetDraft(ctx context.Context, d *model.Draft) error
	DeleteDraft(ctx context.Context, userID, chatID string) error
	ListDrafts(ctx context.Context, userID string, since int64, limit int) ([]*model.Draft, error)
}

// InviteStore owns chat public handles and invite links.
type InviteStore interface {
	// SetChatUsername claims a public handle for a chat (empty clears it).
	// Returns ErrConflict if the handle is taken.
	SetChatUsername(ctx context.Context, chatID, username string) error
	GetChatByUsername(ctx context.Context, username string) (*model.Chat, error)

	CreateInvite(ctx context.Context, l *model.InviteLink) error
	GetInvite(ctx context.Context, code string) (*model.InviteLink, error)
	RevokeInvite(ctx context.Context, code, chatID string) error
	ListInvites(ctx context.Context, chatID string) ([]*model.InviteLink, error)
	// UseInvite atomically increments the use count if the link is still valid,
	// so a capped link cannot be over-redeemed by concurrent joins.
	UseInvite(ctx context.Context, code string, now int64) (*model.InviteLink, error)
}

// MemberRoleStore updates a member's role (admin rights).
type MemberRoleStore interface {
	SetMemberRole(ctx context.Context, chatID, userID string, role model.MemberRole) error
}
