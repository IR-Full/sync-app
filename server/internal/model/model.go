// Package model holds the core domain entities (Section 7 data model). IDs are
// strings carrying base-10 snowflakes so they stay opaque to clients and cheap
// to index. Timestamps are unix milliseconds.
package model

// ChatType distinguishes the three conversation kinds.
type ChatType string

const (
	ChatDirect  ChatType = "direct"  // 1:1
	ChatGroup   ChatType = "group"   // many-to-many, bounded membership
	ChatChannel ChatType = "channel" // broadcast: few writers, huge read fanout
)

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
}

// ChatSummary is a chat as it appears in a user's chat list: the chat itself
// plus the two things that are only true FOR THAT USER — their role, and (in a
// 1:1 chat, which has no title) who the other party is.
type ChatSummary struct {
	Chat   *Chat      `json:"chat"`
	MyRole MemberRole `json:"my_role,omitempty"`
	PeerID string     `json:"peer_id,omitempty"`
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
	Muted bool `json:"muted"`
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
