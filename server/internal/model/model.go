// Package model holds the core domain entities (Section 7 data model). IDs are
// strings carrying base-10 snowflakes so they stay opaque to clients and cheap
// to index. Timestamps are unix milliseconds.
package model

// ChatType distinguishes the conversation kinds.
type ChatType string

const (
	ChatDirect  ChatType = "direct"  // 1:1
	ChatGroup   ChatType = "group"   // many-to-many, bounded membership
	ChatChannel ChatType = "channel" // broadcast: few writers, huge read fanout
	// ChatSecret is a 1:1 conversation whose CONTENT never reaches the server in
	// readable form.
	//
	// It is a chat type rather than a side channel, and that is the whole change.
	// End-to-end chats used to exist only as a relay with no chat row behind it, so
	// they had no entry in the chat list, no history, no unread count and no
	// settings — every client put them in a modal window beside the product, which
	// is exactly how they were treated. Making it a type means the ordinary screens
	// work and only the guarantees differ.
	//
	// What the row holds and does not hold: membership, title, flags, ordering, and
	// the per-chat sequence — all the metadata a chat needs. NOT the messages. Those
	// live in the secret queue as ciphertext until the peer device collects them,
	// and nothing in the message log ever refers to this chat.
	ChatSecret ChatType = "secret"
)

// IsSecret reports whether a chat carries end-to-end content.
//
// A method rather than comparisons scattered around, because several paths have to
// refuse: server-side search cannot index what it cannot read, forwarding OUT of a
// secret chat would launder ciphertext into a cloud chat, and history has nothing
// to return. Each of those is a separate refusal and they must agree.
func (t ChatType) IsSecret() bool { return t == ChatSecret }

// Is1To1 reports whether the type is inherently two-party. Both direct and secret
// chats are, and membership changes are refused for both for the same reason: a
// third member has no session with anyone.
func (t ChatType) Is1To1() bool { return t == ChatDirect || t == ChatSecret }

// User is an account. PasswordHash is argon2id (never the raw password).
// AvatarRef points into the media service (the blob never lives here), so an
// avatar is stored, served, and garbage-collected by the same pipeline as any
// other attachment instead of being a second, special-cased image path.
type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	DisplayName  string `json:"display_name"`
	AvatarRef    string `json:"avatar_ref,omitempty"`
	PasswordHash string `json:"-"`
	CreatedAt    int64  `json:"created_at"`
	// Privacy gates what other accounts may see. Carried on the user rather than
	// fetched separately because every path that needs it is already reading the
	// row — presence fanout, a profile read, a group add.
	Privacy Privacy `json:"privacy"`
}

// ChatSummary is a chat as it appears in a user's chat list: the chat itself
// plus the two things that are only true FOR THAT USER — their role, and (in a
// 1:1 chat, which has no title) who the other party is.
type ChatSummary struct {
	Chat   *Chat      `json:"chat"`
	MyRole MemberRole `json:"my_role,omitempty"`
	PeerID string     `json:"peer_id,omitempty"`

	// Everything below is what a chat list actually shows, and none of it used to
	// be here. A client that wanted a preview, an unread badge or a sort order had
	// to fetch history per chat to work it out — so the N+1 the server had just
	// moved to the client and became N+1 over the network.
	//
	// LastMessage is the newest live message, or nil for an empty chat.
	LastMessage *Message `json:"last_message,omitempty"`
	// UnreadCount is how many messages sit above the caller's read cursor.
	UnreadCount int64 `json:"unread_count,omitempty"`
	// LastActivityAt is the sort key: the newest message's timestamp, falling back
	// to the chat's creation time so a brand-new empty chat appears at the top
	// rather than at the bottom.
	LastActivityAt int64 `json:"last_activity_at,omitempty"`
	// Flags are the caller's own per-chat settings.
	Flags MemberFlags `json:"flags"`
}

// MemberFlags are one member's private settings for a chat.
//
// They live on the membership row, not the chat: muting, pinning and archiving are
// each one person's opinion about a shared conversation. The `muted` column existed
// from the first migration and nothing ever read it — there was no protocol message
// to set it and the notification path never checked it, so muting a chat was
// impossible while looking like a supported feature.
type MemberFlags struct {
	// MutedUntil is a unix-millis deadline; 0 means not muted. A deadline rather
	// than a bool because "for eight hours" is what people want far more often
	// than "forever", and a bool cannot express it — while a deadline expresses
	// both (a very distant one is "forever").
	MutedUntil int64 `json:"muted_until,omitempty"`
	Pinned     bool  `json:"pinned,omitempty"`
	Archived   bool  `json:"archived,omitempty"`
}

// MutedAt reports whether notifications are suppressed at time now (unix millis).
func (f MemberFlags) MutedAt(now int64) bool {
	return f.MutedUntil != 0 && f.MutedUntil > now
}

// Device is one client installation bound to a user. Each device has its own
// sessions and its own delivery cursor so multi-device sync is per-device.
type Device struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	Platform  string `json:"platform"`
	PushToken string `json:"push_token,omitempty"`
	CreatedAt int64  `json:"created_at"`
	LastSeen  int64  `json:"last_seen"`
}

// Session is an authenticated login on a device. Token is an opaque bearer
// credential (hashed at rest in production). RevokedAt != 0 means invalid.
type Session struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	DeviceID    string `json:"device_id"`
	Token       string `json:"-"`
	ResumeToken string `json:"-"`
	CreatedAt   int64  `json:"created_at"`
	ExpiresAt   int64  `json:"expires_at"`
	RevokedAt   int64  `json:"revoked_at,omitempty"`
	// PrevResumeToken is the resume token this session most recently rotated away
	// from, and it is kept on purpose.
	//
	// A resume token used to live unchanged for the session's whole TTL, so one
	// captured token granted access for a fortnight and its use was
	// UNDETECTABLE — the real client kept working alongside whoever took it.
	// Remembering the consumed token turns that into a signal: a resume for a
	// token that has already been spent means two parties hold the chain.
	PrevResumeToken string `json:"-"`
	ResumeRotatedAt int64  `json:"resume_rotated_at,omitempty"`
}

// Chat is a conversation. OwnerID is the creator (channel/group admin seed).
type Chat struct {
	ID        string   `json:"id"`
	Type      ChatType `json:"type"`
	Title     string   `json:"title,omitempty"`
	OwnerID   string   `json:"owner_id"`
	CreatedAt int64    `json:"created_at"`
	// LastSeq is the highest assigned per-chat sequence (monotonic ordering).
	LastSeq uint64 `json:"last_seq"`
	// Username is the public handle (t.me/<username> style). Empty = private chat
	// reachable only by invite. Unique across chats when set.
	Username string `json:"username,omitempty"`
}

// MemberRole controls permissions within a chat.
type MemberRole string

const (
	RoleMember MemberRole = "member"
	RoleAdmin  MemberRole = "admin"
	RoleOwner  MemberRole = "owner"
)

// ChatMember links a user to a chat.
type ChatMember struct {
	ChatID   string     `json:"chat_id"`
	UserID   string     `json:"user_id"`
	Role     MemberRole `json:"role"`
	JoinedAt int64      `json:"joined_at"`
	// Muted disables push for this member.
	//
	// Kept for the column it maps to; MutedUntil in Flags is what the notification
	// path reads. A bool cannot express "for eight hours", which is what muting
	// usually means.
	Muted bool `json:"muted"`
	// Flags are this member's private per-chat settings (mute deadline, pin,
	// archive).
	Flags MemberFlags `json:"flags"`
}

// Message is a persisted chat message. Seq is the per-chat ordering position
// (strictly increasing, gap-free) which defines client-visible order. Deleted
// keeps a tombstone; text is cleared but the row and audit copy remain.
type Message struct {
	ID       string `json:"id"` // snowflake, globally unique + time-sortable
	ChatID   string `json:"chat_id"`
	SenderID string `json:"sender_id"`
	Seq      uint64 `json:"seq"`
	Text     string `json:"text,omitempty"`
	MediaRef string `json:"media_ref,omitempty"`
	// Attachment carries typed media metadata (voice/video-note/file/image).
	// MediaRef stays for backward compatibility with plain media messages.
	Attachment *Attachment `json:"attachment,omitempty"`
	ReplyTo    string      `json:"reply_to,omitempty"`
	// ThreadRoot is the message that starts the thread this reply belongs to
	// (empty for top-level messages). Set server-side from ReplyTo so a whole
	// reply chain shares one root and can be fetched as a thread.
	ThreadRoot string `json:"thread_root,omitempty"`
	// ReplyCount is how many replies this message has as a thread root.
	ReplyCount int32 `json:"reply_count,omitempty"`
	// Forward carries provenance when this message was forwarded from elsewhere.
	Forward *ForwardOrigin `json:"forward,omitempty"`
	// ExpiresAt is a self-destruct deadline in unix millis (0 = never).
	ExpiresAt int64 `json:"expires_at,omitempty"`
	Edited    bool  `json:"edited"`
	Deleted   bool  `json:"deleted"`
	CreatedAt int64 `json:"created_at"`
	EditedAt  int64 `json:"edited_at,omitempty"`
}

// AttachmentKind classifies a message attachment. The bytes always live in the
// media service (a message carries only a media_ref); the kind + metadata here
// are what let a client render a voice waveform, a round video note, or a file
// card without downloading the blob first.
type AttachmentKind string

const (
	AttachFile      AttachmentKind = "file"       // generic document
	AttachImage     AttachmentKind = "image"      // photo
	AttachVideo     AttachmentKind = "video"      // regular video
	AttachVoice     AttachmentKind = "voice"      // voice message (waveform + duration)
	AttachVideoNote AttachmentKind = "video_note" // round video note ("кружочек")
)

// Attachment is media metadata attached to a message.
type Attachment struct {
	Kind     AttachmentKind `json:"kind"`
	MediaRef string         `json:"media_ref"`
	Filename string         `json:"filename,omitempty"`
	MIME     string         `json:"mime,omitempty"`
	Size     int64          `json:"size,omitempty"`
	// DurationMs applies to voice/video/video_note.
	DurationMs int64 `json:"duration_ms,omitempty"`
	// Waveform is a downsampled amplitude envelope (0..100 per bucket) so a voice
	// message renders its bars instantly, before the audio is fetched.
	Waveform []int32 `json:"waveform,omitempty"`
	// Width/Height apply to image/video/video_note (a note is square).
	Width  int32 `json:"width,omitempty"`
	Height int32 `json:"height,omitempty"`
	// ThumbRef is an optional media_ref for a preview image.
	ThumbRef string `json:"thumb_ref,omitempty"`
}

// Reaction is one user's emoji reaction to a message. A user holds at most one
// reaction per message (re-reacting with the same emoji removes it — the toggle
// semantics clients expect), so (message_id, user_id) is the natural key.
type Reaction struct {
	ChatID    string `json:"chat_id"`
	MessageID string `json:"message_id"`
	UserID    string `json:"user_id"`
	Emoji     string `json:"emoji"`
	CreatedAt int64  `json:"created_at"`
}

// ReadState records how far a user has read in a chat (per user, per chat).
type ReadState struct {
	ChatID    string `json:"chat_id"`
	UserID    string `json:"user_id"`
	UpToSeq   uint64 `json:"up_to_seq"`
	UpdatedAt int64  `json:"updated_at"`
}

// Presence is a user's online/last-seen state (ephemeral; Redis-backed in prod).
type Presence struct {
	UserID     string `json:"user_id"`
	Online     bool   `json:"online"`
	LastSeenMs int64  `json:"last_seen_ms"`
}

// CallKind distinguishes audio-only from video calls.
type CallKind string

const (
	CallAudio CallKind = "audio"
	CallVideo CallKind = "video"
)

// CallState is the lifecycle of a call room.
type CallState string

const (
	CallRinging CallState = "ringing" // invited, nobody has joined yet
	CallActive  CallState = "active"  // at least one callee joined
	CallEnded   CallState = "ended"   // everyone left / declined / timed out
)

// ParticipantState is one user's status within a call.
type ParticipantState string

const (
	PartInvited  ParticipantState = "invited"
	PartJoined   ParticipantState = "joined"
	PartLeft     ParticipantState = "left"
	PartDeclined ParticipantState = "declined"
)

// Call is a voice/video call or conference room anchored to a chat. The server
// owns SIGNALING and room state only — audio/video never flows through it (media
// goes peer-to-peer or via an SFU; see internal/call).
type Call struct {
	ID          string    `json:"id"`
	ChatID      string    `json:"chat_id"`
	InitiatorID string    `json:"initiator_id"`
	Kind        CallKind  `json:"kind"`
	State       CallState `json:"state"`
	CreatedAt   int64     `json:"created_at"`
	EndedAt     int64     `json:"ended_at,omitempty"`
}

// CallParticipant is one user's membership in a call. Multiple devices of the
// same user may be invited; the first to accept takes the call.
type CallParticipant struct {
	CallID   string           `json:"call_id"`
	UserID   string           `json:"user_id"`
	DeviceID string           `json:"device_id,omitempty"`
	State    ParticipantState `json:"state"`
	JoinedAt int64            `json:"joined_at,omitempty"`
	LeftAt   int64            `json:"left_at,omitempty"`
}

// Poll is a question with fixed options, posted into a chat. It is anchored to a
// MESSAGE (the question text is a normal message), so a poll inherits chat
// ordering, history, and permissions for free — only the tally lives apart.
type Poll struct {
	ID        string   `json:"id"`
	ChatID    string   `json:"chat_id"`
	MessageID string   `json:"message_id"` // the message carrying the question
	CreatorID string   `json:"creator_id"`
	Question  string   `json:"question"`
	Options   []string `json:"options"`
	// MultiChoice lets a voter pick several options (votes toggle individually);
	// otherwise a new vote replaces the previous one.
	MultiChoice bool `json:"multi_choice"`
	// Anonymous hides who voted for what; the tally is still public.
	Anonymous bool  `json:"anonymous"`
	Closed    bool  `json:"closed"`
	CreatedAt int64 `json:"created_at"`
}

// PollVote is one user's choice of one option.
type PollVote struct {
	PollID      string `json:"poll_id"`
	UserID      string `json:"user_id"`
	OptionIndex int32  `json:"option_index"`
	CreatedAt   int64  `json:"created_at"`
}

// Contact is one entry in a user's address book. Name is the LOCAL name the
// owner gave the contact (their private label), which is why contacts are
// per-owner rows rather than a shared graph.
type Contact struct {
	OwnerID   string `json:"owner_id"`
	UserID    string `json:"user_id"`
	Name      string `json:"name,omitempty"`
	Blocked   bool   `json:"blocked,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// ForwardOrigin records where a forwarded message came from. It is a COPY of the
// provenance, not a reference: a forward must survive the original being deleted.
type ForwardOrigin struct {
	ChatID    string `json:"chat_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	SenderID  string `json:"sender_id,omitempty"`
}

// ScheduledMessage is a send that has not happened yet. It lives outside the
// message log until due, so it occupies no chat sequence number (a cancelled
// schedule must not leave a permanent gap) and is invisible to history/fanout.
type ScheduledMessage struct {
	ID         string      `json:"id"`
	ChatID     string      `json:"chat_id"`
	SenderID   string      `json:"sender_id"`
	Text       string      `json:"text,omitempty"`
	MediaRef   string      `json:"media_ref,omitempty"`
	Attachment *Attachment `json:"attachment,omitempty"`
	ReplyTo    string      `json:"reply_to,omitempty"`
	TTLSeconds int32       `json:"ttl_seconds,omitempty"`
	SendAt     int64       `json:"send_at"`
	CreatedAt  int64       `json:"created_at"`
	Sent       bool        `json:"sent,omitempty"`
}

// PinnedMessage marks a message as pinned in its chat. Pins are chat-wide (every
// member sees the same set), unlike drafts which are per-user.
type PinnedMessage struct {
	ChatID    string `json:"chat_id"`
	MessageID string `json:"message_id"`
	PinnedBy  string `json:"pinned_by"`
	PinnedAt  int64  `json:"pinned_at"`
}

// Draft is an unsent message a user is composing in a chat. Drafts are PRIVATE
// to the user but shared across THEIR devices — start typing on a phone, finish
// on a desktop.
type Draft struct {
	UserID    string `json:"user_id"`
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ReplyTo   string `json:"reply_to,omitempty"`
	UpdatedAt int64  `json:"updated_at"`
}

// InviteLink is a shareable join token for a chat. Links are revocable and may
// be bounded by uses and/or time — an unbounded, unrevocable link is a permanent
// hole in a private chat's membership.
type InviteLink struct {
	Code      string `json:"code"` // the opaque, unguessable token in the URL
	ChatID    string `json:"chat_id"`
	CreatedBy string `json:"created_by"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at,omitempty"` // 0 = never
	MaxUses   int32  `json:"max_uses,omitempty"`   // 0 = unlimited
	Uses      int32  `json:"uses"`
	Revoked   bool   `json:"revoked,omitempty"`
}

// SecretEnvelope is one end-to-end ciphertext held for a device that was not
// connected when it was relayed.
//
// It exists because SECRET_SEND used to be pure relay: the gateway asked the
// router which nodes held the recipient, published to each, and DISCARDED the
// return value. Zero nodes — the recipient offline — meant the ciphertext went
// nowhere, no push was queued, and the sender was told nothing. A secret chat
// between two people who are not online at the same moment delivered nothing at
// all, which is the one mode the whole X3DH/ratchet/pinning stack exists for.
//
// Header and Ciphertext are the Double Ratchet wire bytes, opaque here and
// unreadable by the server — exactly what it already relayed, now durable.
// They are []byte rather than the base64 strings the wire body carries: this is
// storage, and re-encoding bytes to text to put them in a BYTEA column would be
// a third copy of the same bits for no reason.
//
// ExpiresAt is not optional. A queue of undelivered ciphertext addressed by
// user+device is also a record of who messaged whom and when — metadata the
// relay never persisted — so it has to be collected on a schedule rather than
// kept until someone remembers to look.
type SecretEnvelope struct {
	ID           string `json:"id"`
	ToUserID     string `json:"to_user_id"`
	ToDeviceID   string `json:"to_device_id"`
	FromUserID   string `json:"from_user_id"`
	FromDeviceID string `json:"from_device_id"`
	Header       []byte `json:"header"`
	Ciphertext   []byte `json:"ciphertext"`
	CreatedAt    int64  `json:"created_at"`
	ExpiresAt    int64  `json:"expires_at"`
}

// TwoFactor is an account's second authentication factor.
//
// It exists because the first factor is interceptable and, until now, was the
// only one: a leaked password meant a lost account with no way to intervene —
// and no way to change the password either, since nothing could.
//
// SecretEnc is the TOTP shared secret ENCRYPTED at rest. Storing it in the clear
// would make a database leak equivalent to a leak of everyone's second factor,
// which is the one thing the factor is supposed to survive. It is not hashed
// (the way a password is) because verification needs the secret itself to
// recompute a code — that is the difference between a secret and a credential,
// and it is why the key lives outside the database.
//
// ConfirmedAt is the enrolment gate. A secret exists from the moment setup
// begins, but it is not ENFORCED until the user has proved they can produce a
// code from it. Without that step a mis-scanned QR code locks the account out of
// itself, which is the most common way 2FA rollouts go wrong.
type TwoFactor struct {
	UserID      string `json:"user_id"`
	SecretEnc   string `json:"secret_enc"`
	ConfirmedAt int64  `json:"confirmed_at,omitempty"`
	// RecoveryHashes are argon2id hashes of one-time recovery codes. Hashed and
	// not encrypted, because unlike the TOTP secret these are never needed back:
	// verification compares a hash, so a leak of the table yields nothing usable.
	RecoveryHashes []string `json:"recovery_hashes,omitempty"`
	CreatedAt      int64    `json:"created_at"`
}

// Enabled reports whether this factor is enforced at login.
func (t *TwoFactor) Enabled() bool { return t != nil && t.ConfirmedAt != 0 }
