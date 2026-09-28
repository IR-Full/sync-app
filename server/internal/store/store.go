// Package store defines the persistence contracts each service owns. Splitting
// by aggregate (users, sessions, chats, messages, read-state) mirrors the
// service/database ownership boundaries in Section 6/8: in production these back
// onto different engines (Postgres for metadata, a wide-column store for the
// message log, Redis for presence). The interfaces let us start Postgres-only
// and swap the message store later without touching business logic.
package store

import (
	"context"

	"github.com/SyncApp-chat/SyncApp/internal/model"
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

	// UpdatePrivacy writes a user's visibility settings. Takes the whole struct
	// rather than a patch for the same reason UpdateProfile takes final values:
	// the caller has already decided what "leave unchanged" means, and a store
	// that guessed from empty strings could never express "nobody".
	UpdatePrivacy(ctx context.Context, userID string, p model.Privacy) error

	// DeleteAccount erases an account and everything that belongs to it, in one
	// transaction: sessions, devices (and their push tokens), the address book on
	// both sides of every entry, drafts, read cursors, reactions, votes, pending
	// sends, invite links, pins, call participation, chat memberships, and the
	// users row itself.
	//
	// Messages are ANONYMISED rather than dropped. A message is content in
	// somebody else's conversation: deleting the rows outright would silently
	// rewrite chats belonging to people who did not ask for anything, and leaving
	// the sender id would keep the account identifiable after its row is gone.
	// So the text, media and sender are erased in place and the row is
	// tombstoned — the conversation keeps its shape, the person disappears from
	// it. This is the split the privacy policy has to describe, so it is stated
	// here rather than left to whichever backend implements it.
	DeleteAccount(ctx context.Context, userID string) error

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
	// TouchSession pushes a live session's expiry out, which is what makes the
	// TTL a rolling window rather than a countdown from login. It must NOT
	// resurrect a revoked or already-expired session: "still in use" is a claim
	// about a session that is currently valid, and extending a dead one would
	// undo a logout.
	TouchSession(ctx context.Context, id string, expiresAt int64) error
	// RotateResumeToken swaps a session's resume token, remembering the one it
	// replaced.
	//
	// A compare-and-swap on oldHash, not a blind write: two resumes racing on the
	// same token must not both succeed and hand out two live chains. The loser
	// gets ErrNotFound and, because the winner has already moved the token on, is
	// indistinguishable from a replay — which is the correct reading of it.
	//
	// The previous token is kept because that is what makes theft VISIBLE. A
	// resume for an already-consumed token means two parties hold the chain, and
	// the safe response is to end it rather than guess which one is the owner.
	RotateResumeToken(ctx context.Context, sessionID, oldHash, newHash string, at int64) error
	// GetSessionByConsumedResumeToken finds the session a token USED to belong to.
	// A hit is the theft signal above; ErrNotFound is the ordinary case of a
	// token that was never valid.
	GetSessionByConsumedResumeToken(ctx context.Context, resume string) (*model.Session, error)
}

// ChatStore owns chats and membership (Chat Service data). BumpSeq atomically
// allocates the next per-chat sequence — the single source of message ordering.
type ChatStore interface {
	CreateChat(ctx context.Context, c *model.Chat) error
	GetChat(ctx context.Context, id string) (*model.Chat, error)
	// GetOrCreateDirect returns the canonical 1:1 chat for a user pair, creating
	// it if absent (idempotent on the unordered pair).
	GetOrCreateDirect(ctx context.Context, userA, userB string, newID string) (*model.Chat, error)
	// GetOrCreatePair is GetOrCreateDirect for any inherently two-party type.
	//
	// It exists because a secret chat is also canonical per pair, and canonical
	// matters more there than for a direct chat: a second secret chat with the same
	// person would appear as a second row in the list with its own unread badge and
	// its own history, and nothing in the product could explain which was which.
	//
	// The type is part of the key, so a secret chat and an ordinary one with the
	// same person coexist — which they must, since choosing the secret one is the
	// whole point of having it.
	GetOrCreatePair(ctx context.Context, typ model.ChatType, userA, userB string, newID string) (*model.Chat, error)
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
	// InsertMessage persists a message.
	//
	// It ALLOCATES THE SEQUENCE ITSELF. The caller leaves m.Seq zero and reads the
	// assigned value off the returned message; calling BumpSeq beforehand burns a
	// sequence per write. The allocation has to happen here because it shares the
	// insert's transaction: that is what lets a deduped write roll the bump back,
	// and it is the only reason a retried send leaves no hole in an ordering that
	// promises to be gap-free.
	//
	// Implementations must enforce idempotency on (SenderID, dedupKey): a repeat
	// returns the already-stored message with dup=true instead of inserting a
	// second row. When not a duplicate and mkOb is non-nil, the returned outbox
	// record is persisted atomically with the message — and mkOb runs AFTER the
	// sequence is assigned, so the event body can carry it.
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
	SecretQ   SecretQueueStore
	TwoFactor TwoFactorStore
	Billing   BillingStore
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

// MediaChatResolver is an optional MessageStore capability: which chats can
// reach a blob.
//
// Separate from MediaReferencer because the questions differ. Collection asks
// "is this referenced at all", and one live reference anywhere is enough.
// Authorisation asks "may THIS person fetch it", which can only be answered
// against the chats the blob actually appears in — and there may be several,
// because a forward carries the original's ref into another chat. Membership in
// any one of them is sufficient: someone who can read a forward of a picture
// can already see the picture.
//
// `limit` bounds the answer. A blob forwarded into a thousand chats does not
// need a thousand rows to decide one membership question.
type MediaChatResolver interface {
	MediaRefChats(ctx context.Context, ref string, limit int) ([]string, error)
}

// AvatarRefFinder is an optional UserStore capability: is this blob somebody's
// profile picture?
//
// Needed by the media authorizer. An avatar is not reachable from any message,
// so the chat-membership rule would deny every one of them — and a profile is
// readable by anyone who can name the account, so the picture that goes with it
// carries no additional secret.
type AvatarRefFinder interface {
	AvatarRefExists(ctx context.Context, ref string) (bool, error)
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

// SecretQueueStore holds end-to-end ciphertext for devices that were offline
// when it was relayed, until they come back and confirm they have it.
//
// Addressed by (user, device) rather than by chat, because that is how the E2E
// relay is addressed: a secret message is encrypted to ONE device's ratchet
// session, and no other device of the same account can read it.
//
// Delivery is at-least-once and the client acknowledges explicitly. The
// alternative — deleting on send — loses the message when the socket dies
// between the write and the client persisting it, which is the same failure
// this queue exists to fix, just moved one step later.
type SecretQueueStore interface {
	// EnqueueSecret stores one envelope for later delivery. Implementations
	// enforce maxPerDevice by dropping the OLDEST envelope for that device: an
	// unbounded queue addressed by a client-asserted device id is a way to fill
	// the database on someone else's behalf, and the oldest undelivered
	// ciphertext is the one whose ratchet session is least likely to still exist.
	EnqueueSecret(ctx context.Context, e *model.SecretEnvelope, maxPerDevice int) error
	// PendingSecrets returns queued envelopes for one device, OLDEST FIRST and
	// after the given id (empty = from the start), at most limit. Oldest first
	// because ratchet messages decrypt in order far more cheaply than out of it:
	// delivering the newest first would make the receiver derive and store a
	// skipped key for every message behind it.
	PendingSecrets(ctx context.Context, toUserID, toDeviceID, afterID string, limit int) ([]*model.SecretEnvelope, error)
	// AckSecrets deletes envelopes the device confirmed, and is SCOPED BY OWNER:
	// an envelope id travels to the client, so an unscoped delete would let any
	// account drop another's undelivered mail by naming its ids.
	AckSecrets(ctx context.Context, toUserID, toDeviceID string, ids []string) (int, error)
	// PurgeExpiredSecrets drops envelopes past their expiry, at most limit per
	// call, returning how many went.
	PurgeExpiredSecrets(ctx context.Context, now int64, limit int) (int, error)
}

// ChatSummaryReader is an optional ChatStore capability: the whole chat list in
// one query.
//
// It is optional rather than part of ChatStore because it can only be answered
// where the chat metadata, the membership, the read cursors and the MESSAGE LOG
// are reachable from the same query. That holds for the single-Postgres
// deployment (and for the in-memory store), and it deliberately does not hold for
// a backend whose message log lives on separate shards — which is why
// chat.Service keeps the row-at-a-time path as a fallback rather than assuming
// this exists.
//
// What it replaces: a loop that read ALL of a user's chat ids, sorted them in Go,
// then issued two or three queries per row to build each summary. A 50-row page
// cost up to 150 round trips, every page re-read the entire membership, and the
// order was by chat id — which is creation order, not activity.
type ChatSummaryReader interface {
	// UserChatSummaries returns one page of the caller's chats, newest activity
	// first, with the last message, unread count and the caller's own flags.
	//
	// The cursor is (afterActivity, afterChatID) — the last row of the previous
	// page. Keyset, not OFFSET: the list reorders as messages arrive, so an offset
	// would skip and repeat rows exactly when the chat is busy. Pass (0, "") for
	// the first page.
	//
	// includeArchived selects which pile is being listed. Archiving is a per-member
	// flag, so this is a filter on the caller's own rows and not a different set of
	// chats.
	UserChatSummaries(ctx context.Context, userID string, afterActivity int64, afterChatID string, limit int, includeArchived bool) ([]model.ChatSummary, error)
}

// MemberFlagStore is an optional ChatStore capability: a member's private
// per-chat settings (mute deadline, pin, archive).
//
// Separate from MemberRoleStore because the two answer to different authorities.
// A role is granted by a chat admin and visible to everyone; a flag is the
// member's own preference about their own list, and nobody else may set or see it.
type MemberFlagStore interface {
	// SetMemberFlags replaces one member's flags. Scoped by (chat, user) so a
	// caller can only ever write their own row.
	SetMemberFlags(ctx context.Context, chatID, userID string, f model.MemberFlags) error
	// GetMemberFlags reads them back. ErrNotFound when the user is not a member.
	GetMemberFlags(ctx context.Context, chatID, userID string) (model.MemberFlags, error)
}

// TwoFactorStore owns second-factor enrolment.
//
// A separate store rather than columns on the users row, for two reasons. The
// recovery codes are a LIST that shrinks as they are used, which a column cannot
// express; and the encrypted secret is the one field in the account whose
// exposure is worth a different retention and audit policy than a display name.
type TwoFactorStore interface {
	// PutTwoFactor writes (or replaces) an account's enrolment.
	PutTwoFactor(ctx context.Context, tf *model.TwoFactor) error
	// GetTwoFactor reads it, or ErrNotFound when the account has none.
	GetTwoFactor(ctx context.Context, userID string) (*model.TwoFactor, error)
	// DeleteTwoFactor removes it (disabling the second factor).
	DeleteTwoFactor(ctx context.Context, userID string) error
	// ConsumeRecoveryCode atomically removes one recovery hash, reporting whether
	// it was there.
	//
	// Atomic because a recovery code is single-use and the check-then-write shape
	// would let two concurrent logins both spend the same one — which is exactly
	// the race an attacker replaying an observed code would want.
	ConsumeRecoveryCode(ctx context.Context, userID, hash string) (bool, error)
}

// PasswordStore is an optional UserStore capability: replacing a password hash.
//
// Optional only because the interface predates it. Without it a password cannot
// be changed at all — which was the state of the system: a leaked password meant
// the account was gone, because revoking sessions does not stop whoever knows the
// password from logging in again.
type PasswordStore interface {
	SetPasswordHash(ctx context.Context, userID, hash string) error
}

// BillingStore owns subscriptions and payments.
//
// Kept as one store rather than two because every interesting operation touches
// both: a payment succeeding is what activates a subscription, and the two writes
// have to be one transaction or a paid customer can end up on the free tier.
type BillingStore interface {
	// GetSubscription returns an account's subscription, or ErrNotFound.
	GetSubscription(ctx context.Context, userID string) (*model.Subscription, error)
	// PutSubscription writes (or replaces) it.
	PutSubscription(ctx context.Context, sub *model.Subscription) error

	// CreatePayment records an attempt. Implementations enforce idempotency on
	// (user_id, idempotency_key): a retried checkout returns the already-stored
	// payment with dup=true instead of starting a second one.
	//
	// Same shape as InsertMessage's dedup, and for a stronger reason — a duplicate
	// message is noise, a duplicate payment is somebody's money.
	CreatePayment(ctx context.Context, p *model.Payment) (stored *model.Payment, dup bool, err error)
	// GetPaymentByRef finds a payment by the PROVIDER's reference, which is how a
	// webhook identifies what it is talking about.
	GetPaymentByRef(ctx context.Context, provider, providerRef string) (*model.Payment, error)
	// AttachProviderRef records the provider's id once the charge call returns it.
	//
	// Separate from CreatePayment because the row must exist BEFORE the provider is
	// called: a callback can arrive before the charge response does, and a webhook
	// that finds no row has to either drop the money or invent an account for it.
	AttachProviderRef(ctx context.Context, paymentID, providerRef string) error

	// ApplyPaymentStatus advances a payment and, when it settles, the subscription
	// with it — in ONE transaction.
	//
	// The transition is checked against the stored status inside that transaction,
	// which is what makes duplicate and out-of-order callbacks safe: providers retry,
	// and a `pending` notification arriving after `succeeded` would otherwise un-pay
	// a paid subscription. changed=false means the callback was not news.
	ApplyPaymentStatus(ctx context.Context, provider, providerRef string, next model.PaymentStatus, at int64, sub *model.Subscription) (changed bool, err error)

	// ListPayments returns an account's payments, newest first — the receipts a
	// user is entitled to see.
	ListPayments(ctx context.Context, userID string, limit int) ([]*model.Payment, error)
	// ExpireSubscriptions moves lapsed rows to canceled, returning the affected
	// accounts so their entitlements can be re-announced.
	ExpireSubscriptions(ctx context.Context, now int64, limit int) ([]string, error)
}
