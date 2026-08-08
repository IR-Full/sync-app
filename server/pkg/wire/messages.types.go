package wire

// Body payloads. In this MVP bodies are JSON for readability and zero codegen;
// the framing/envelope above is the actual "custom binary protocol". Swapping
// these structs to protobuf is a body-only change (production recommendation).

// HelloBody is the first client message. It negotiates version and capabilities
// and optionally requests session resume.
type HelloBody struct {
	ClientVersion string `json:"client_version"`
	DeviceID      string `json:"device_id"`
	Platform      string `json:"platform"` // ios | android | web | desktop | cli
	Caps          Cap    `json:"caps"`
	ResumeToken   string `json:"resume_token,omitempty"`
}

// WelcomeBody accepts the connection and states negotiated parameters.
type WelcomeBody struct {
	ServerVersion   string `json:"server_version"`
	SessionID       string `json:"session_id"`
	Caps            Cap    `json:"caps"` // intersection the server agreed to
	HeartbeatMs     int    `json:"heartbeat_ms"`
	MaxInflight     int    `json:"max_inflight"` // backpressure window
	ResumeSupported bool   `json:"resume_supported"`
}

// AuthBody carries a bearer session token, or username/password credentials.
// Register=true means "create this account" (fails if it already exists);
// Register=false means "log in" (fails if the account does not exist). The two
// intents are explicit so the server never silently creates accounts.
type AuthBody struct {
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Register bool   `json:"register,omitempty"`
	// DisplayName is honoured on registration only. Afterwards the name is
	// changed with MsgProfileSet, so there is exactly one writer of the field
	// and no ambiguity about whether a login re-asserts it.
	DisplayName string `json:"display_name,omitempty"`
}

// AuthOKBody confirms identity and returns a fresh token for later resumes. It
// also carries WHO the session belongs to: a client that authenticated by token
// (every launch after the first) otherwise knows its user id and nothing else
// about itself, and no other message would tell it.
type AuthOKBody struct {
	UserID      string `json:"user_id"`
	DeviceID    string `json:"device_id"`
	SessionID   string `json:"session_id"`
	Token       string `json:"token"`
	ResumeToken string `json:"resume_token"`
	Username    string `json:"username,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	AvatarRef   string `json:"avatar_ref,omitempty"`
}

// SendBody is a client's request to post a message into a chat. DedupKey is a
// client-generated idempotency key: the server maps (device, DedupKey) → the
// server message id so retries never duplicate.
type SendBody struct {
	ChatID   string `json:"chat_id"`
	DedupKey string `json:"dedup_key"`
	Text     string `json:"text,omitempty"`
	MediaRef string `json:"media_ref,omitempty"` // reference into media service
	ReplyTo  string `json:"reply_to,omitempty"`
	// TTLSeconds self-destructs the message N seconds after it lands (0 = never).
	TTLSeconds int32 `json:"ttl_seconds,omitempty"`
	// Attachment carries typed media metadata (voice message, round video note,
	// file, image). The bytes are uploaded through the media pipeline first; this
	// only describes them.
	Attachment *Attachment `json:"attachment,omitempty"`
}

// Attachment describes media attached to a message. Kind drives client
// rendering: "voice" shows a waveform player, "video_note" a round video,
// "file" a document card.
type Attachment struct {
	Kind       string  `json:"kind"`
	MediaRef   string  `json:"media_ref"`
	Filename   string  `json:"filename,omitempty"`
	MIME       string  `json:"mime,omitempty"`
	Size       int64   `json:"size,omitempty"`
	DurationMs int64   `json:"duration_ms,omitempty"`
	Waveform   []int32 `json:"waveform,omitempty"`
	Width      int32   `json:"width,omitempty"`
	Height     int32   `json:"height,omitempty"`
	ThumbRef   string  `json:"thumb_ref,omitempty"`
}

// SendAckBody confirms durable persistence and returns ordering metadata.
type SendAckBody struct {
	DedupKey  string `json:"dedup_key"`
	MessageID string `json:"message_id"` // server-assigned snowflake
	ChatID    string `json:"chat_id"`
	ChatSeq   uint64 `json:"chat_seq"`  // monotonic per-chat ordering position
	Timestamp int64  `json:"timestamp"` // server unix millis
	Duplicate bool   `json:"duplicate"` // true if this resolved an existing send
}

// NewMessageBody is a message delivered to a device via fanout / history.
type NewMessageBody struct {
	MessageID  string      `json:"message_id"`
	ChatID     string      `json:"chat_id"`
	SenderID   string      `json:"sender_id"`
	ChatSeq    uint64      `json:"chat_seq"`
	Text       string      `json:"text,omitempty"`
	MediaRef   string      `json:"media_ref,omitempty"`
	ReplyTo    string      `json:"reply_to,omitempty"`
	Attachment *Attachment `json:"attachment,omitempty"`
	// Forward carries provenance when this message was forwarded.
	Forward *ForwardOrigin `json:"forward,omitempty"`
	// ExpiresAt is the self-destruct deadline in unix millis (0 = never).
	ExpiresAt int64 `json:"expires_at,omitempty"`
	// ThreadRoot is the thread this message belongs to (empty = top level);
	// ReplyCount is the tally when this message IS a thread root.
	ThreadRoot string `json:"thread_root,omitempty"`
	ReplyCount int32  `json:"reply_count,omitempty"`
	Edited     bool   `json:"edited,omitempty"`
	Deleted    bool   `json:"deleted,omitempty"`
	Timestamp  int64  `json:"timestamp"`
}

// ReadBody marks a chat read up to (and including) UpToMessageID.
type ReadBody struct {
	ChatID        string `json:"chat_id"`
	UpToMessageID string `json:"up_to_message_id"`
	UpToChatSeq   uint64 `json:"up_to_chat_seq"`
}

// ReadUpdateBody notifies that a member read up to a position.
type ReadUpdateBody struct {
	ChatID      string `json:"chat_id"`
	UserID      string `json:"user_id"`
	UpToChatSeq uint64 `json:"up_to_chat_seq"`
}

// ReactBody toggles an emoji reaction on a message (C→S). Sending the emoji the
// user already has removes it; a different emoji replaces it.
type ReactBody struct {
	ChatID    string `json:"chat_id"`
	MessageID string `json:"message_id"`
	Emoji     string `json:"emoji"`
}

// ReactUpdateBody announces a reaction change to a chat's members (S→C). Added
// is false when the reaction was removed (toggled off). Counts is the full tally
// per emoji after the change, so a client can render without re-fetching.
type ReactUpdateBody struct {
	ChatID    string         `json:"chat_id"`
	MessageID string         `json:"message_id"`
	UserID    string         `json:"user_id"`
	Emoji     string         `json:"emoji"`
	Added     bool           `json:"added"`
	Counts    map[string]int `json:"counts,omitempty"`
}

// TypingBody signals typing state in a chat.
type TypingBody struct {
	ChatID string `json:"chat_id"`
	UserID string `json:"user_id,omitempty"` // filled by server on outbound
	Active bool   `json:"active"`
}

// PresenceBody carries online / last-seen state for a user.
type PresenceBody struct {
	UserID     string `json:"user_id"`
	Online     bool   `json:"online"`
	LastSeenMs int64  `json:"last_seen_ms,omitempty"`
}

// EditBody edits a message's text.
type EditBody struct {
	ChatID    string `json:"chat_id"`
	MessageID string `json:"message_id"`
	Text      string `json:"text"`
}

// DeleteBody deletes a message (tombstone; server keeps an audit copy).
type DeleteBody struct {
	ChatID    string `json:"chat_id"`
	MessageID string `json:"message_id"`
	ForAll    bool   `json:"for_all"`
}

// HistoryBody requests backfill of a chat before a cursor.
type HistoryBody struct {
	ChatID    string `json:"chat_id"`
	BeforeSeq uint64 `json:"before_seq"` // 0 = latest
	Limit     int    `json:"limit"`
}

// HistoryOKBody terminates a history stream with the next cursor.
type HistoryOKBody struct {
	ChatID     string `json:"chat_id"`
	NextBefore uint64 `json:"next_before"`
	Done       bool   `json:"done"`
}

// ResumeBody requests resumption of a dropped session.
type ResumeBody struct {
	ResumeToken string `json:"resume_token"`
	LastAckSeq  uint64 `json:"last_ack_seq"` // last server Seq the client stored
}

// ResumeOKBody confirms resume; replay of unacked frames follows.
type ResumeOKBody struct {
	SessionID string `json:"session_id"`
	FromSeq   uint64 `json:"from_seq"`
}

// ErrorBody is the generic error payload.
type ErrorBody struct {
	Code         ErrorCode `json:"code"`
	Message      string    `json:"message"`
	RetryAfterMs int       `json:"retry_after_ms,omitempty"`
}

// --- Media bodies ---

// MediaInitBody begins an upload.
type MediaInitBody struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
}

// MediaTicketBody returns where to upload and the reference to attach to a message.
type MediaTicketBody struct {
	MediaRef  string `json:"media_ref"`
	UploadURL string `json:"upload_url"`
	ExpiresAt int64  `json:"expires_at"`
}

// MediaFetchBody requests a download URL for a media reference.
type MediaFetchBody struct {
	MediaRef string `json:"media_ref"`
}

// MediaURLBody is a signed, expiring download URL.
type MediaURLBody struct {
	MediaRef    string `json:"media_ref"`
	DownloadURL string `json:"download_url"`
	ExpiresAt   int64  `json:"expires_at"`
}

// --- Search bodies ---

// SearchBody is a full-text query.
type SearchBody struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// SearchHit is one result row.
type SearchHit struct {
	MessageID string `json:"message_id"`
	ChatID    string `json:"chat_id"`
	SenderID  string `json:"sender_id"`
	Seq       uint64 `json:"seq"`
	Text      string `json:"text"`
}

// SearchResultsBody is the ranked, permission-filtered result set.
type SearchResultsBody struct {
	Query string      `json:"query"`
	Hits  []SearchHit `json:"hits"`
}

// --- Secret-chat (E2E) bodies. The server treats Ciphertext/keys as opaque. ---

// KeyPublishBody uploads a device's long-term identity keys and a batch of
// one-time prekeys for X3DH. Keys are base64 raw-url encoded; IdentityKey and
// prekeys are X25519 public keys, SigningKey is an Ed25519 public key, and
// SignedPreKeySig is the Ed25519 signature over SignedPreKey.
type KeyPublishBody struct {
	IdentityKey     string   `json:"identity_key"`
	SigningKey      string   `json:"signing_key"`
	SignedPreKey    string   `json:"signed_prekey"`
	SignedPreKeySig string   `json:"signed_prekey_sig"`
	PreKeys         []string `json:"prekeys"`
}

// KeyFetchBody requests a peer device's prekey bundle to start a session.
type KeyFetchBody struct {
	UserID   string `json:"user_id"`
	DeviceID string `json:"device_id"`
}

// KeyBundleBody is one consumable X3DH prekey bundle for a peer device. The
// initiator verifies SignedPreKeySig against SigningKey before use.
type KeyBundleBody struct {
	UserID          string `json:"user_id"`
	DeviceID        string `json:"device_id"`
	IdentityKey     string `json:"identity_key"`
	SigningKey      string `json:"signing_key"`
	SignedPreKey    string `json:"signed_prekey"`
	SignedPreKeySig string `json:"signed_prekey_sig"`
	OneTimePreKey   string `json:"one_time_prekey,omitempty"`
}

// KeyBundlesBody carries every device's prekey bundle for a user. A sender uses
// it to establish a secret-chat session with each of the peer's devices AND each
// of its own other devices — enabling multi-device sync of secret chats.
type KeyBundlesBody struct {
	UserID  string          `json:"user_id"`
	Bundles []KeyBundleBody `json:"bundles"`
}

// ChatExportBody requests a full dump of a chat (owner/admin only).
type ChatExportBody struct {
	ChatID string `json:"chat_id"`
}

// ChatMemberInfo is one member row in a chat export.
type ChatMemberInfo struct {
	UserID   string `json:"user_id"`
	Role     string `json:"role"`
	JoinedAt int64  `json:"joined_at"`
}

// ChatExportResultBody is the owner/admin dump of a cloud chat (metadata +
// members + messages). Secret chats export metadata only (server has no plaintext).
type ChatExportResultBody struct {
	ChatID   string           `json:"chat_id"`
	Type     string           `json:"type"`
	Title    string           `json:"title"`
	OwnerID  string           `json:"owner_id"`
	Members  []ChatMemberInfo `json:"members"`
	Messages []NewMessageBody `json:"messages"`
	Done     bool             `json:"done"` // true on the final page of a streamed export
}

// SecretMsgBody carries an opaque E2E ciphertext between two devices. The server
// routes by recipient and stores nothing readable — it cannot decrypt this.
type SecretMsgBody struct {
	ToUserID     string `json:"to_user_id"`
	ToDeviceID   string `json:"to_device_id"`
	FromUserID   string `json:"from_user_id,omitempty"`   // filled by server on relay
	FromDeviceID string `json:"from_device_id,omitempty"` // filled by server on relay
	// RatchetHeader + Ciphertext are the Double-Ratchet wire bytes (base64).
	RatchetHeader string `json:"ratchet_header"`
	Ciphertext    string `json:"ciphertext"`
}

// BodyCodec encodes/decodes envelope bodies. It is a seam: the MVP uses JSON for
// readability and zero codegen, but a production build can swap in protobuf via
// SetBodyCodec without touching the framing, envelope, or any call site — bodies
// are the ONLY thing that changes. This is why the "JSON vs protobuf" decision is
// isolated to one interface.
type BodyCodec interface {
	Marshal(v any) ([]byte, error)
	Unmarshal(b []byte, v any) error
}

type jsonCodec struct{}

// ThreadBody requests the replies under a thread root (oldest first).
type ThreadBody struct {
	ChatID   string `json:"chat_id"`
	RootID   string `json:"root_id"`
	AfterSeq uint64 `json:"after_seq,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// ThreadOKBody terminates a streamed thread page.
type ThreadOKBody struct {
	ChatID    string `json:"chat_id"`
	RootID    string `json:"root_id"`
	NextAfter uint64 `json:"next_after,omitempty"`
	Done      bool   `json:"done"`
}

// CallInviteBody starts (or joins) a call in a chat.
type CallInviteBody struct {
	ChatID string `json:"chat_id"`
	Kind   string `json:"kind"` // audio | video
}

// CallActionBody accepts / declines / hangs up a call by id.
type CallActionBody struct {
	CallID string `json:"call_id"`
}

// CallParticipant is one member's status inside a call room.
type CallParticipant struct {
	UserID   string `json:"user_id"`
	DeviceID string `json:"device_id,omitempty"`
	State    string `json:"state"` // invited | joined | left | declined
}

// CallStateBody is the room's lifecycle + roster, pushed to every participant
// whenever it changes (someone rang, joined, declined, or the call ended).
type CallStateBody struct {
	CallID       string            `json:"call_id"`
	ChatID       string            `json:"chat_id"`
	InitiatorID  string            `json:"initiator_id"`
	Kind         string            `json:"kind"`
	State        string            `json:"state"` // ringing | active | ended
	Participants []CallParticipant `json:"participants,omitempty"`
}

// CallSignalBody relays ONE WebRTC signaling payload (an SDP offer/answer or an
// ICE candidate) between two participants' devices. Payload is OPAQUE to the
// server: it is never parsed, never stored, and only forwarded to the addressed
// device of a verified call participant. Media itself never touches the server.
type CallSignalBody struct {
	CallID       string `json:"call_id"`
	ToUserID     string `json:"to_user_id"`
	ToDeviceID   string `json:"to_device_id,omitempty"`
	FromUserID   string `json:"from_user_id,omitempty"`   // stamped by the server
	FromDeviceID string `json:"from_device_id,omitempty"` // stamped by the server
	SignalType   string `json:"signal_type"`              // offer | answer | candidate
	Payload      string `json:"payload"`                  // opaque SDP / ICE blob
}

// PollCreateBody posts a poll into a chat. The question is also written as a
// normal message, so the poll appears in history like any other content.
type PollCreateBody struct {
	ChatID      string   `json:"chat_id"`
	Question    string   `json:"question"`
	Options     []string `json:"options"`
	MultiChoice bool     `json:"multi_choice,omitempty"`
	Anonymous   bool     `json:"anonymous,omitempty"`
}

// PollVoteBody casts (or toggles) a vote.
type PollVoteBody struct {
	PollID string `json:"poll_id"`
	Option int32  `json:"option"`
}

// PollCloseBody stops a poll (creator only).
type PollCloseBody struct {
	PollID string `json:"poll_id"`
}

// PollOption is one choice with its current vote count.
type PollOption struct {
	Index int32  `json:"index"`
	Text  string `json:"text"`
	Votes int32  `json:"votes"`
}

// PollStateBody is a poll plus its live tally. MyVotes is filled only on a
// direct reply to the voter — the broadcast to other members omits it, so one
// member's choices are never revealed to another.
type PollStateBody struct {
	PollID      string       `json:"poll_id"`
	ChatID      string       `json:"chat_id"`
	MessageID   string       `json:"message_id"`
	Question    string       `json:"question"`
	Options     []PollOption `json:"options"`
	TotalVotes  int32        `json:"total_votes"`
	MultiChoice bool         `json:"multi_choice,omitempty"`
	Anonymous   bool         `json:"anonymous,omitempty"`
	Closed      bool         `json:"closed,omitempty"`
	MyVotes     []int32      `json:"my_votes,omitempty"`
}

// ContactAddBody adds or renames a contact. Target is a user id or "@username".
type ContactAddBody struct {
	Target string `json:"target"`
	Name   string `json:"name,omitempty"`
}

// ContactRemoveBody removes a contact.
type ContactRemoveBody struct {
	Target string `json:"target"`
}

// ContactSyncBody requests everything changed after Since (0 = full sync).
type ContactSyncBody struct {
	Since int64 `json:"since,omitempty"`
}

// Contact is one address-book entry.
type Contact struct {
	UserID    string `json:"user_id"`
	Name      string `json:"name,omitempty"`
	Blocked   bool   `json:"blocked,omitempty"`
	UpdatedAt int64  `json:"updated_at"`
}

// ContactListBody is a sync page plus the cursor to resume from.
type ContactListBody struct {
	Contacts []Contact `json:"contacts,omitempty"`
	Cursor   int64     `json:"cursor"`
}

// BlockBody blocks or unblocks a user (works on strangers, not just contacts).
type BlockBody struct {
	Target  string `json:"target"`
	Blocked bool   `json:"blocked"`
}

// ForwardBody copies a message from one chat into another.
type ForwardBody struct {
	FromChatID string `json:"from_chat_id"`
	MessageID  string `json:"message_id"`
	ToChatID   string `json:"to_chat_id"`
	DedupKey   string `json:"dedup_key"`
}

// ForwardOrigin travels with a forwarded message so clients can render
// "forwarded from …". It is a snapshot, not a live reference.
type ForwardOrigin struct {
	ChatID    string `json:"chat_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	SenderID  string `json:"sender_id,omitempty"`
}

// ScheduleBody defers a send to SendAt (unix millis).
type ScheduleBody struct {
	ChatID     string      `json:"chat_id"`
	Text       string      `json:"text,omitempty"`
	MediaRef   string      `json:"media_ref,omitempty"`
	Attachment *Attachment `json:"attachment,omitempty"`
	ReplyTo    string      `json:"reply_to,omitempty"`
	TTLSeconds int32       `json:"ttl_seconds,omitempty"`
	SendAt     int64       `json:"send_at"`
}

// ScheduleListBody asks for a user's pending sends in a chat.
type ScheduleListBody struct {
	ChatID string `json:"chat_id"`
}

// ScheduleCancelBody cancels one pending send.
type ScheduleCancelBody struct {
	ID string `json:"id"`
}

// ScheduledItem is one pending send.
type ScheduledItem struct {
	ID     string `json:"id"`
	ChatID string `json:"chat_id"`
	Text   string `json:"text,omitempty"`
	SendAt int64  `json:"send_at"`
}

// ScheduledBody returns pending sends (or the one just created).
type ScheduledBody struct {
	Items []ScheduledItem `json:"items,omitempty"`
}

// Pin is one pinned message in a chat.
type Pin struct {
	MessageID string `json:"message_id"`
	PinnedBy  string `json:"pinned_by"`
	PinnedAt  int64  `json:"pinned_at"`
}

// PinBody pins or unpins a message.
type PinBody struct {
	ChatID    string `json:"chat_id"`
	MessageID string `json:"message_id"`
}

// PinnedBody is a chat's full pin set (S→C), sent whenever it changes.
type PinnedBody struct {
	ChatID string `json:"chat_id"`
	Pins   []Pin  `json:"pins,omitempty"`
}

// DraftBody saves what the user is composing. Empty text clears the draft.
type DraftBody struct {
	ChatID  string `json:"chat_id"`
	Text    string `json:"text,omitempty"`
	ReplyTo string `json:"reply_to,omitempty"`
}

// DraftSyncBody requests drafts changed after Since (0 = all).
type DraftSyncBody struct {
	Since int64 `json:"since,omitempty"`
}

// DraftItem is one synced draft.
type DraftItem struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text,omitempty"`
	ReplyTo   string `json:"reply_to,omitempty"`
	UpdatedAt int64  `json:"updated_at"`
}

// DraftsBody is a draft-sync page plus the cursor to resume from.
type DraftsBody struct {
	Drafts []DraftItem `json:"drafts,omitempty"`
	Cursor int64       `json:"cursor"`
}

// SetUsernameBody claims (or clears with "") a chat's public handle.
type SetUsernameBody struct {
	ChatID   string `json:"chat_id"`
	Username string `json:"username"`
}

// InviteCreateBody mints a link. Zero values mean unlimited.
type InviteCreateBody struct {
	ChatID    string `json:"chat_id"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	MaxUses   int32  `json:"max_uses,omitempty"`
}

// InviteRevokeBody kills a link.
type InviteRevokeBody struct {
	ChatID string `json:"chat_id"`
	Code   string `json:"code"`
}

// InviteListBody lists a chat's live links.
type InviteListBody struct {
	ChatID string `json:"chat_id"`
}

// JoinBody joins by invite code, or by "@handle" for a public chat.
type JoinBody struct {
	Code   string `json:"code,omitempty"`
	Handle string `json:"handle,omitempty"`
}

// SetRoleBody promotes or demotes a member.
type SetRoleBody struct {
	ChatID string `json:"chat_id"`
	UserID string `json:"user_id"`
	Role   string `json:"role"` // member | admin | owner
}

// InviteLink is one live link.
type InviteLink struct {
	Code      string `json:"code"`
	ChatID    string `json:"chat_id"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	MaxUses   int32  `json:"max_uses,omitempty"`
	Uses      int32  `json:"uses"`
}

// PushTokenBody registers this device's push token (empty clears it — a user
// turning notifications off should stop them at the source, not at the device).
type PushTokenBody struct {
	Token string `json:"token"`
}

// ChatCreateBody creates a group or channel. Members are user ids or
// "@username" — the server resolves them, so a client never has to look a
// stranger up before it can invite them.
type ChatCreateBody struct {
	Type    string   `json:"type"` // group | channel
	Title   string   `json:"title"`
	Members []string `json:"members,omitempty"`
}

// ChatInfoBody describes a chat (currently the reply to a create).
type ChatInfoBody struct {
	ChatID  string `json:"chat_id"`
	Type    string `json:"type"`
	Title   string `json:"title"`
	OwnerID string `json:"owner_id"`
}

// ChatListBody asks for the chats the caller belongs to. It pages by keyset
// over the chat id — After is the last id of the previous page ("" starts from
// the beginning) — because a client with more chats than fit in one frame has
// to be able to resume, and the id is the only cursor that stays valid while
// the list is being read.
type ChatListBody struct {
	After string `json:"after,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// ChatSummary is one row of the chat list: enough to render an entry without a
// follow-up round trip per chat.
type ChatSummary struct {
	ChatID   string `json:"chat_id"`
	Type     string `json:"type"`
	Title    string `json:"title,omitempty"`
	OwnerID  string `json:"owner_id,omitempty"`
	Username string `json:"username,omitempty"` // public handle, if any
	// LastSeq is the chat's newest position, so a client knows what to backfill.
	LastSeq uint64 `json:"last_seq"`
	MyRole  string `json:"my_role,omitempty"`
	// PeerID is filled for DIRECT chats only: the other participant. A 1:1 chat
	// has no title, so without this the entry has nothing to be named after.
	PeerID string `json:"peer_id,omitempty"`
}

// ChatsBody is one page of the caller's chat list plus the cursor to resume from.
type ChatsBody struct {
	Chats     []ChatSummary `json:"chats,omitempty"`
	NextAfter string        `json:"next_after,omitempty"`
	Done      bool          `json:"done"`
}

// ProfileGetBody reads a user's public profile. Target is a user id or
// "@username", which also makes this the handle→user lookup.
type ProfileGetBody struct {
	Target string `json:"target"`
}

// ProfileSetBody updates the CALLER's own profile. Empty fields mean "leave as
// is" (proto3 cannot distinguish absent from empty), so removing an avatar is
// an explicit flag rather than an empty string.
type ProfileSetBody struct {
	DisplayName string `json:"display_name,omitempty"`
	AvatarRef   string `json:"avatar_ref,omitempty"`
	ClearAvatar bool   `json:"clear_avatar,omitempty"`
}

// ProfileBody is a user's public profile. It carries nothing private: the same
// body goes to the owner and to a stranger who resolved their handle.
type ProfileBody struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name,omitempty"`
	AvatarRef   string `json:"avatar_ref,omitempty"`
}

// FanoutShardBody is one chunk of a hot chat's recipients plus the message to
// deliver to them. It never reaches a client — it is an internal bus payload —
// but it goes through the same codec as everything else that crosses the bus,
// because the alternative was the heaviest payload in the system travelling as
// JSON while every lighter one was protobuf.
type FanoutShardBody struct {
	Body    NewMessageBody `json:"body"`
	Members []string       `json:"members,omitempty"`
}

// InvitesBody returns links, or the chat joined.
type InvitesBody struct {
	Links      []InviteLink `json:"links,omitempty"`
	JoinedChat string       `json:"joined_chat,omitempty"`
}
