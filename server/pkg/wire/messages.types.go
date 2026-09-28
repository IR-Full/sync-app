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
	// TOTPCode is the second factor, sent on the RETRY after the server answered
	// ErrTwoFactorRequired. A client cannot know in advance whether an account has
	// one — asking would make this an oracle for which accounts are protected —
	// so the flow is: send credentials, get told a code is needed, send both.
	//
	// A recovery code is accepted here too. The user who has lost their phone is
	// looking at the same prompt, and making them find a different screen for it is
	// how a recovery path goes unused.
	TOTPCode string `json:"totp_code,omitempty"`
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
	// ResumeToken is the NEXT token, because resuming consumes the one that was
	// used. A client that keeps its old token after a successful resume will fail
	// the next one — and, worse, will look to the server exactly like a thief
	// replaying a consumed token, which ends the session.
	//
	// Rotation is the point: an unrotated resume token granted access for the
	// session whole 14-day life from a single capture, and its use was invisible
	// because the real client kept working alongside it.
	ResumeToken string `json:"resume_token,omitempty"`
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
	// ChatID narrows to one conversation, SenderID to one author. Both are
	// filters WITHIN what the caller may already see: the server scopes every
	// search by chat membership first, and a named chat is verified against it
	// rather than trusted — a chat id is guessable, so treating it as a filter
	// alone would turn "search in this chat" into a way to read one.
	ChatID   string `json:"chat_id,omitempty"`
	SenderID string `json:"sender_id,omitempty"`
}

// SearchHit is one result row.
type SearchHit struct {
	MessageID string `json:"message_id"`
	ChatID    string `json:"chat_id"`
	SenderID  string `json:"sender_id"`
	Seq       uint64 `json:"seq"`
	Text      string `json:"text"`
	// CreatedAt is the ranking key a client can show and re-sort by. Seq is a
	// PER-CHAT counter, so it cannot order results that span chats — which is the
	// mistake the server's own ranking used to make.
	CreatedAt int64 `json:"created_at,omitempty"`
}

// SearchResultsBody is the ranked, permission-filtered result set.
type SearchResultsBody struct {
	Query string      `json:"query"`
	Hits  []SearchHit `json:"hits"`
}

// --- Secret-chat (E2E) bodies. The server treats Ciphertext/keys as opaque. ---

// KeyPublishBody uploads a device's long-term identity keys and a batch of
// one-time prekeys for X3DH. Keys are **standard base64 with padding**
// (`base64.StdEncoding`) — not raw-url, which an earlier version of this comment
// claimed and no client ever used. IdentityKey and prekeys are 32-byte X25519
// public keys, SigningKey is a 32-byte Ed25519 public key, and SignedPreKeySig is
// the 64-byte Ed25519 signature over SignedPreKey.
//
// The gateway validates all of that (and that the signature verifies) before the
// bundle reaches the directory; see gateway.validateKeyBundle.
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

// KeyStateBody answers KEY_PUBLISH: what the directory now holds for this device.
//
// The reply exists because of a gap that was invisible from both ends. One-time
// prekeys are consumed one per peer that starts a session, so a popular device runs
// its batch down on its own; when the batch is empty X3DH silently falls back to
// three Diffie-Hellmans instead of four, which is weaker and reported to nobody.
// Only the OWNER can refill, and the owner is not the party doing the fetching — so
// the count has to come back here, on the publish, and nowhere else.
//
// SignedPreKeyAgeMs is the second half: a signed prekey published once and never
// rotated is a single key protecting every future session start. The client decides
// when to rotate; the server just reports how old the one it holds is, because the
// client cannot know whether its own publish ever landed.
type KeyStateBody struct {
	// OneTimePreKeysLeft is what the directory holds AFTER this publish was applied,
	// capped at MaxOneTimePreKeys. A client tops up when it drops below its own
	// threshold rather than at zero: reaching zero has already cost somebody the
	// stronger handshake.
	OneTimePreKeysLeft int `json:"one_time_prekeys_left"`
	// SignedPreKeyAgeMs is how long ago the stored signed prekey first appeared. Zero
	// when this publish introduced it.
	SignedPreKeyAgeMs int64 `json:"signed_prekey_age_ms,omitempty"`
	// Accepted is the number of prekeys from THIS frame the directory kept. Lower
	// than what was sent when the per-publish cap or the per-device ceiling trimmed
	// it — which a client that keeps private halves needs to know, or it holds
	// private keys for public ones nobody will ever fetch.
	Accepted int `json:"accepted"`
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
	// The LEGACY text form, and its two halves are encoded DIFFERENTLY — the field
	// comment used to claim both were base64, which is wrong and would reject every
	// real message if anyone believed it:
	//
	//   - RatchetHeader is TEXT. Every client puts a JSON object there (the X3DH
	//     bootstrap on the first message, the ratchet header after), and the
	//     receiving client calls a JSON parse on it.
	//   - Ciphertext IS base64, because it is genuinely opaque bytes.
	//
	// Header + Cipher are the same two values as raw bytes.
	//
	// Both exist because only one can be used per connection, and which one is a
	// property of the PEER rather than of the message. Base64 was the only form,
	// and it cost 33% on the wire plus an encode and a decode at each end — which
	// is precisely what this project wrote a binary protocol to avoid. But a client
	// built against the string fields reads nothing from the binary ones, so the
	// old form cannot simply go.
	//
	// The decision is made by the handler, which knows what the peer negotiated;
	// the codec just maps whichever fields are set. That is why this is not a
	// capability check inside Marshal: the codec is a package-level singleton with
	// no idea who it is encoding for, and threading connection state into it would
	// be a much larger change than carrying two fields.
	//
	// Readers accept EITHER. Use SecretPayload to get the bytes without caring.
	RatchetHeader string `json:"ratchet_header,omitempty"`
	Ciphertext    string `json:"ciphertext,omitempty"`
	Header        []byte `json:"header,omitempty"`
	Cipher        []byte `json:"cipher,omitempty"`
	// QueueID is set ONLY on a replay out of the offline queue, and it is what
	// the receiver echoes in SECRET_ACKED to have the row dropped. Empty means
	// this arrived live and there is nothing to acknowledge — which keeps the
	// common path free of an id that exists solely to be deleted.
	QueueID string `json:"queue_id,omitempty"`
}

// SecretAckBody reports what the relay did with a SECRET_SEND.
//
// Sent because the relay used to answer nothing at all: a client could not tell
// "the peer has it" from "the peer is offline" from "the server dropped it on
// the floor", and it drew the same state for all three. Devices is how many of
// the recipient's live devices the ciphertext reached; Queued says the rest was
// stored for later.
type SecretAckBody struct {
	ToUserID   string `json:"to_user_id"`
	ToDeviceID string `json:"to_device_id,omitempty"`
	Devices    int32  `json:"devices"`
	Queued     bool   `json:"queued,omitempty"`
}

// SecretSyncBody asks for ciphertext this device missed while it was away.
// After is the QueueID of the last envelope the device stored (empty = from the
// start), so an interrupted sync resumes instead of restarting.
type SecretSyncBody struct {
	After string `json:"after,omitempty"`
	Limit int32  `json:"limit,omitempty"`
}

// SecretSyncedBody ends one sync page. Done=false means there is more behind
// NextAfter — a device that has been away a long time pages rather than
// receiving an unbounded burst.
type SecretSyncedBody struct {
	Count     int32  `json:"count"`
	NextAfter string `json:"next_after,omitempty"`
	Done      bool   `json:"done,omitempty"`
}

// SecretAckedBody confirms envelopes are stored on the device and may be
// dropped. The queue is at-least-once on purpose: deleting on send would lose
// the message when the socket dies between the write and the client persisting
// it, which is the same failure the queue exists to fix, moved one step later.
type SecretAckedBody struct {
	IDs []string `json:"ids"`
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
// BillingPlansBody asks what is purchasable.
//
// Country comes from the CLIENT rather than from a GeoIP lookup on the server: the
// user knows which market they are in, and a lookup that guesses wrong offers a
// payment method their bank does not support.
type BillingPlansBody struct {
	Country string `json:"country,omitempty"`
}

// BillingOffersBody is the catalogue for one market.
type BillingOffersBody struct {
	Offers []PlanOfferWire `json:"offers,omitempty"`
}

// PlanOfferWire is one purchasable plan.
//
// AmountMinor is an INTEGER in the currency minor unit — kopeks, cents. Never a
// float and never a formatted string: a price is an exact quantity, binary floating
// point cannot hold 0.01, and a client that renders the price from a float
// eventually shows a number that differs from what it charges.
type PlanOfferWire struct {
	Plan        string   `json:"plan"`
	AmountMinor int64    `json:"amount_minor"`
	Currency    string   `json:"currency"`
	PeriodDays  int32    `json:"period_days"`
	Methods     []string `json:"methods,omitempty"`
}

// BillingCheckoutBody starts a payment.
type BillingCheckoutBody struct {
	Plan   string `json:"plan"`
	Method string `json:"method,omitempty"`
	// IdempotencyKey is REQUIRED and is the client. A retried checkout must reach the
	// same payment rather than starting a second one, and only the client knows two
	// requests are the same request — a key the server invents differs on every
	// retry, which is the same as having none.
	IdempotencyKey string `json:"idempotency_key"`
	Country        string `json:"country,omitempty"`
	ReturnURL      string `json:"return_url,omitempty"`
}

// BillingPaymentBody is where to send the user.
type BillingPaymentBody struct {
	PaymentID   string `json:"payment_id"`
	Status      string `json:"status"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	// PayURL is followed; QRPayload is DISPLAYED. They are not interchangeable — an
	// SBP QR payload is not a URL, and a client that renders it as a link produces a
	// broken one.
	PayURL    string `json:"pay_url,omitempty"`
	QRPayload string `json:"qr_payload,omitempty"`
	// Deduplicated means this was a repeat of an earlier request and no new charge
	// was made. Reported rather than hidden so a client can tell "already paying"
	// from "paying again".
	Deduplicated bool `json:"deduplicated,omitempty"`
}

// BillingStatusBody asks for the caller subscription. No fields: it is always the
// caller own, because a subscription is not something one account may read of
// another.
type BillingStatusBody struct{}

// BillingCancelBody stops renewal. No fields, and no "immediately" option: the
// period is paid for, and a product that takes away what was bought the moment
// someone clicks cancel teaches them not to click it.
type BillingCancelBody struct{}

// SubscriptionBody is the caller tier and what it grants.
//
// Sent as a reply to BILLING_STATUS and PUSHED whenever the subscription changes —
// a payment settling, a cancellation, a period lapsing. The push is what keeps a
// client from offering features the server has started refusing.
//
// Entitlements are sent EXPLICITLY rather than derived from the plan name. The
// client would otherwise have to encode the policy too, and the two copies drift:
// a server that raises the upload ceiling would need every client updated before
// anyone could use it.
type SubscriptionBody struct {
	Plan   string `json:"plan"`
	Status string `json:"status"`
	// PeriodEnd is when access lapses without a renewal (unix millis, 0 = no expiry).
	PeriodEnd         int64 `json:"period_end,omitempty"`
	CancelAtPeriodEnd bool  `json:"cancel_at_period_end,omitempty"`

	SecretChats        bool  `json:"secret_chats,omitempty"`
	MaxUploadBytes     int64 `json:"max_upload_bytes,omitempty"`
	MaxPinnedChats     int32 `json:"max_pinned_chats,omitempty"`
	Folders            bool  `json:"folders,omitempty"`
	AdvancedSearch     bool  `json:"advanced_search,omitempty"`
	PriorityDelivery   bool  `json:"priority_delivery,omitempty"`
	VoiceTranscription bool  `json:"voice_transcription,omitempty"`
	Badge              bool  `json:"badge,omitempty"`
	CustomThemes       bool  `json:"custom_themes,omitempty"`
}

// PasswordChangeBody replaces the caller password.
//
// The old password is required even though the connection is already
// authenticated. A session token is enough to ACT as the account but not enough
// to replace its credential: a stolen token would otherwise become permanent
// ownership, which is exactly what a password change is supposed to take back.
type PasswordChangeBody struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// PasswordChangedBody confirms the change and says how many OTHER sessions were
// signed out. The caller own session survives — signing someone out of the device
// they are using to secure the account is a good way to have them not finish.
type PasswordChangedBody struct {
	SessionsRevoked int32 `json:"sessions_revoked"`
}

// TOTPSetupBody begins enrolment. No fields: the server mints the secret, because
// a client-chosen one is a client-chosen second factor.
type TOTPSetupBody struct{}

// TOTPSetupInfoBody carries the new secret and the otpauth:// URI an
// authenticator app scans.
//
// The factor is NOT yet enforced at this point. Enrolment is two steps on purpose:
// without a confirmation step a mis-scanned QR code locks an account out of
// itself, which is the most common way a 2FA rollout goes wrong.
type TOTPSetupInfoBody struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
}

// TOTPConfirmBody proves the user can produce a code, which is what enrols them.
type TOTPConfirmBody struct {
	Code string `json:"code"`
}

// TOTPDisableBody removes the factor. Password AND a code, because someone holding
// only a stolen session token must not be able to take off the factor that would
// keep them out of the next login.
type TOTPDisableBody struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

// TOTPStateBody is the current second-factor state, plus the recovery codes on the
// one occasion they are shown.
//
// RecoveryCodes is populated ONLY in the reply to TOTP_CONFIRM. The stored form is
// an argon2id hash, so there is nothing to show later — which is the property that
// makes a leak of the table worthless, and the reason the client has to tell the
// user to write them down now.
type TOTPStateBody struct {
	Enabled       bool     `json:"enabled"`
	RecoveryLeft  int32    `json:"recovery_left"`
	RecoveryCodes []string `json:"recovery_codes,omitempty"`
	ConfirmedAtMs int64    `json:"confirmed_at_ms,omitempty"`
}

// ChatFlagsBody sets the caller's own settings for one chat.
//
// MutedUntil is a unix-millis deadline, not a flag: "mute for eight hours" is what
// muting usually means and a boolean cannot express it, while a distant deadline
// expresses "forever". 0 unmutes.
type ChatFlagsBody struct {
	ChatID     string `json:"chat_id"`
	MutedUntil int64  `json:"muted_until,omitempty"`
	Pinned     bool   `json:"pinned,omitempty"`
	Archived   bool   `json:"archived,omitempty"`
}

// ChatFlagsSetBody echoes the flags now stored, so a client that raced two
// changes converges on what the server has rather than on what it last sent.
type ChatFlagsSetBody struct {
	ChatID     string `json:"chat_id"`
	MutedUntil int64  `json:"muted_until,omitempty"`
	Pinned     bool   `json:"pinned,omitempty"`
	Archived   bool   `json:"archived,omitempty"`
}

type ChatListBody struct {
	After string `json:"after,omitempty"`
	Limit int    `json:"limit,omitempty"`
	// AfterActivity is the other half of the cursor: the previous page's last
	// LastActivityAt. The list is ordered by activity, which reorders as messages
	// arrive, so a cursor naming only a chat id would skip and repeat rows exactly
	// when the chat is busy.
	AfterActivity int64 `json:"after_activity,omitempty"`
	// IncludeArchived lists the archived pile instead of hiding it.
	IncludeArchived bool `json:"include_archived,omitempty"`
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

	// The rest is what a chat list actually draws, and none of it used to be sent.
	// A client that wanted a preview, a badge or a sort order had to call HISTORY
	// per chat to work it out — so the server's N+1 moved to the client and became
	// N+1 over the network.
	//
	// LastMessage previews the newest live message (nil for an empty chat).
	LastMessage *NewMessageBody `json:"last_message,omitempty"`
	// UnreadCount is how many messages sit above the caller's read cursor.
	UnreadCount int64 `json:"unread_count,omitempty"`
	// LastActivityAt is the sort key AND the paging cursor. It falls back to the
	// chat's creation time so a new empty chat appears at the top, not the bottom.
	LastActivityAt int64 `json:"last_activity_at,omitempty"`
	// The caller's own settings for this chat.
	MutedUntil int64 `json:"muted_until,omitempty"`
	Pinned     bool  `json:"pinned,omitempty"`
	Archived   bool  `json:"archived,omitempty"`
}

// ChatsBody is one page of the caller's chat list plus the cursor to resume from.
type ChatsBody struct {
	Chats     []ChatSummary `json:"chats,omitempty"`
	NextAfter string        `json:"next_after,omitempty"`
	// NextAfterActivity completes the cursor. Both halves are echoed back in the
	// next CHAT_LIST, so the client never has to reconstruct a sort key it did not
	// choose.
	NextAfterActivity int64 `json:"next_after_activity,omitempty"`
	Done              bool  `json:"done"`
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

// AccountDeleteBody erases the CALLER's account. The password is re-checked
// even though the socket is already authenticated: a session token lives on the
// device, so without it anyone holding an unlocked phone could destroy the
// account behind it.
type AccountDeleteBody struct {
	Password string `json:"password"`
	// Optional; recorded in the audit log and nowhere else — in particular never
	// on a row that outlives the account.
	Reason string `json:"reason,omitempty"`
}

// AccountDeletedBody confirms the erasure. Every session is revoked before it
// is sent, so it is the last frame the connection carries.
type AccountDeletedBody struct {
	UserID    string `json:"user_id"`
	DeletedAt int64  `json:"deleted_at"`
}

// PrivacyGetBody asks for the caller's own visibility settings.
//
// No target field, deliberately: these are not readable for anyone else, and a
// message that could name someone would leak exactly what it exists to protect.
type PrivacyGetBody struct{}

// PrivacySetBody replaces the caller's settings.
//
// Every field is sent every time. A partial update would make "nobody"
// indistinguishable from "not specified", and the field whose whole purpose is
// to withhold something is the worst one to have an ambiguous empty value.
type PrivacySetBody struct {
	LastSeen string `json:"last_seen"`
	Avatar   string `json:"avatar"`
	Groups   string `json:"groups"`
	// PushPreview allows message text in the push payload. See PrivacyBody.
	PushPreview bool `json:"push_preview,omitempty"`
}

// PrivacyBody is the current settings, echoed after a get or a set.
type PrivacyBody struct {
	LastSeen string `json:"last_seen"`
	Avatar   string `json:"avatar"`
	Groups   string `json:"groups"`
	// PushPreview allows message TEXT in the push payload. The audience for this
	// one is not another user but Apple and Google: the notification path used to
	// include a preview of every message unconditionally, so a third party saw the
	// contents of every conversation. Defaults false — the old behaviour was a leak,
	// not a setting anyone had chosen.
	PushPreview bool `json:"push_preview,omitempty"`
}

// HistoryPageBody is one backfill page delivered as a single frame.
//
// The per-message NEW stream it replaces is still used for peers that did not
// negotiate CapBatching, so both shapes carry exactly the same messages — a page
// is a transport optimisation, not a different answer.
type HistoryPageBody struct {
	Messages []NewMessageBody `json:"messages"`
	// ChatID is the RESOLVED id even when the request addressed "@handle": this
	// frame is the only one that says which chat the page came from.
	ChatID     string `json:"chat_id"`
	NextBefore uint64 `json:"next_before"`
	Done       bool   `json:"done"`
}

// SessionListBody asks for every live session of the caller's account. It has no
// fields: the account is the authenticated connection, and letting a client name
// a different one would make this a cross-account enumeration primitive.
type SessionListBody struct{}

// SessionInfo describes one live session.
//
// It deliberately carries NO token. The point of the list is to let a person
// recognise a device well enough to decide whether to kill it; handing every
// device the credentials of every other would turn a read into a lateral-movement
// tool, which is the opposite of what this is for.
type SessionInfo struct {
	SessionID string `json:"session_id"`
	DeviceID  string `json:"device_id"`
	Platform  string `json:"platform"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	// Current marks the session this connection authenticated with, so a client
	// can label it and warn before revoking it.
	Current bool `json:"current"`
}

// SessionsBody is the answer to SessionListBody.
type SessionsBody struct {
	Sessions []SessionInfo `json:"sessions"`
}

// SessionRevokeBody kills sessions.
//
// An empty SessionID means "every session except this one" — the "sign out
// everywhere else" a person reaches for after losing a device. AllIncludingCurrent
// extends the sweep to this connection too; it is a separate field because "log
// out everywhere, including here" and "log out everywhere but here" are different
// intentions, and a client should not have to express the difference by omitting
// something.
type SessionRevokeBody struct {
	SessionID           string `json:"session_id,omitempty"`
	AllIncludingCurrent bool   `json:"all_including_current,omitempty"`
}

// SessionRevokedBody reports what the revoke actually did. The count matters: a
// client that asked to sign out five devices and signed out one should say so
// rather than show a checkmark.
type SessionRevokedBody struct {
	Revoked int `json:"revoked"`
	// Self is true when the caller's own session was among those killed, so the
	// client drops its token instead of waiting for the connection to fail.
	Self bool `json:"self"`
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
