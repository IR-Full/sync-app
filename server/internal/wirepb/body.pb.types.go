package wirepb

import protoimpl "google.golang.org/protobuf/runtime/protoimpl"

type Hello struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ClientVersion string `protobuf:"bytes,1,opt,name=client_version,json=clientVersion,proto3" json:"client_version,omitempty"`
	DeviceId      string `protobuf:"bytes,2,opt,name=device_id,json=deviceId,proto3" json:"device_id,omitempty"`
	Platform      string `protobuf:"bytes,3,opt,name=platform,proto3" json:"platform,omitempty"`
	Caps          uint32 `protobuf:"varint,4,opt,name=caps,proto3" json:"caps,omitempty"`
	ResumeToken   string `protobuf:"bytes,5,opt,name=resume_token,json=resumeToken,proto3" json:"resume_token,omitempty"`
}

type Welcome struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ServerVersion   string `protobuf:"bytes,1,opt,name=server_version,json=serverVersion,proto3" json:"server_version,omitempty"`
	SessionId       string `protobuf:"bytes,2,opt,name=session_id,json=sessionId,proto3" json:"session_id,omitempty"`
	Caps            uint32 `protobuf:"varint,3,opt,name=caps,proto3" json:"caps,omitempty"`
	HeartbeatMs     int32  `protobuf:"varint,4,opt,name=heartbeat_ms,json=heartbeatMs,proto3" json:"heartbeat_ms,omitempty"`
	MaxInflight     int32  `protobuf:"varint,5,opt,name=max_inflight,json=maxInflight,proto3" json:"max_inflight,omitempty"`
	ResumeSupported bool   `protobuf:"varint,6,opt,name=resume_supported,json=resumeSupported,proto3" json:"resume_supported,omitempty"`
}

type Auth struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Token    string `protobuf:"bytes,1,opt,name=token,proto3" json:"token,omitempty"`
	Username string `protobuf:"bytes,2,opt,name=username,proto3" json:"username,omitempty"`
	Password string `protobuf:"bytes,3,opt,name=password,proto3" json:"password,omitempty"`
	Register bool   `protobuf:"varint,4,opt,name=register,proto3" json:"register,omitempty"`
	// display_name is honoured on registration only; afterwards it is changed
	// through ProfileSet, which is the only writer of the field.
	DisplayName string `protobuf:"bytes,5,opt,name=display_name,json=displayName,proto3" json:"display_name,omitempty"`
	// The second factor, sent on the RETRY after the server answered
	// ErrTwoFactorRequired. A recovery code is accepted here too: the user who
	// lost their phone is looking at the same prompt.
	TotpCode string `protobuf:"bytes,6,opt,name=totp_code,json=totpCode,proto3" json:"totp_code,omitempty"`
}

type AuthOK struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	UserId      string `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	DeviceId    string `protobuf:"bytes,2,opt,name=device_id,json=deviceId,proto3" json:"device_id,omitempty"`
	SessionId   string `protobuf:"bytes,3,opt,name=session_id,json=sessionId,proto3" json:"session_id,omitempty"`
	Token       string `protobuf:"bytes,4,opt,name=token,proto3" json:"token,omitempty"`
	ResumeToken string `protobuf:"bytes,5,opt,name=resume_token,json=resumeToken,proto3" json:"resume_token,omitempty"`
	// The identity behind the session. Without these a client that authenticated
	// by TOKEN knows its user id and nothing else about itself — there was no
	// other message that would tell it.
	Username    string `protobuf:"bytes,6,opt,name=username,proto3" json:"username,omitempty"`
	DisplayName string `protobuf:"bytes,7,opt,name=display_name,json=displayName,proto3" json:"display_name,omitempty"`
	AvatarRef   string `protobuf:"bytes,8,opt,name=avatar_ref,json=avatarRef,proto3" json:"avatar_ref,omitempty"`
}

type Send struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId     string      `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	DedupKey   string      `protobuf:"bytes,2,opt,name=dedup_key,json=dedupKey,proto3" json:"dedup_key,omitempty"`
	Text       string      `protobuf:"bytes,3,opt,name=text,proto3" json:"text,omitempty"`
	MediaRef   string      `protobuf:"bytes,4,opt,name=media_ref,json=mediaRef,proto3" json:"media_ref,omitempty"`
	ReplyTo    string      `protobuf:"bytes,5,opt,name=reply_to,json=replyTo,proto3" json:"reply_to,omitempty"`
	Attachment *Attachment `protobuf:"bytes,6,opt,name=attachment,proto3" json:"attachment,omitempty"`
	TtlSeconds int32       `protobuf:"varint,7,opt,name=ttl_seconds,json=ttlSeconds,proto3" json:"ttl_seconds,omitempty"`
}

// Attachment describes media attached to a message (voice, video note, file).
type Attachment struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Kind       string  `protobuf:"bytes,1,opt,name=kind,proto3" json:"kind,omitempty"`
	MediaRef   string  `protobuf:"bytes,2,opt,name=media_ref,json=mediaRef,proto3" json:"media_ref,omitempty"`
	Filename   string  `protobuf:"bytes,3,opt,name=filename,proto3" json:"filename,omitempty"`
	Mime       string  `protobuf:"bytes,4,opt,name=mime,proto3" json:"mime,omitempty"`
	Size       int64   `protobuf:"varint,5,opt,name=size,proto3" json:"size,omitempty"`
	DurationMs int64   `protobuf:"varint,6,opt,name=duration_ms,json=durationMs,proto3" json:"duration_ms,omitempty"`
	Waveform   []int32 `protobuf:"varint,7,rep,packed,name=waveform,proto3" json:"waveform,omitempty"`
	Width      int32   `protobuf:"varint,8,opt,name=width,proto3" json:"width,omitempty"`
	Height     int32   `protobuf:"varint,9,opt,name=height,proto3" json:"height,omitempty"`
	ThumbRef   string  `protobuf:"bytes,10,opt,name=thumb_ref,json=thumbRef,proto3" json:"thumb_ref,omitempty"`
}

type SendAck struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	DedupKey  string `protobuf:"bytes,1,opt,name=dedup_key,json=dedupKey,proto3" json:"dedup_key,omitempty"`
	MessageId string `protobuf:"bytes,2,opt,name=message_id,json=messageId,proto3" json:"message_id,omitempty"`
	ChatId    string `protobuf:"bytes,3,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	ChatSeq   uint64 `protobuf:"varint,4,opt,name=chat_seq,json=chatSeq,proto3" json:"chat_seq,omitempty"`
	Timestamp int64  `protobuf:"varint,5,opt,name=timestamp,proto3" json:"timestamp,omitempty"`
	Duplicate bool   `protobuf:"varint,6,opt,name=duplicate,proto3" json:"duplicate,omitempty"`
}

type NewMessage struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	MessageId  string         `protobuf:"bytes,1,opt,name=message_id,json=messageId,proto3" json:"message_id,omitempty"`
	ChatId     string         `protobuf:"bytes,2,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	SenderId   string         `protobuf:"bytes,3,opt,name=sender_id,json=senderId,proto3" json:"sender_id,omitempty"`
	ChatSeq    uint64         `protobuf:"varint,4,opt,name=chat_seq,json=chatSeq,proto3" json:"chat_seq,omitempty"`
	Text       string         `protobuf:"bytes,5,opt,name=text,proto3" json:"text,omitempty"`
	MediaRef   string         `protobuf:"bytes,6,opt,name=media_ref,json=mediaRef,proto3" json:"media_ref,omitempty"`
	ReplyTo    string         `protobuf:"bytes,7,opt,name=reply_to,json=replyTo,proto3" json:"reply_to,omitempty"`
	Edited     bool           `protobuf:"varint,8,opt,name=edited,proto3" json:"edited,omitempty"`
	Deleted    bool           `protobuf:"varint,9,opt,name=deleted,proto3" json:"deleted,omitempty"`
	Timestamp  int64          `protobuf:"varint,10,opt,name=timestamp,proto3" json:"timestamp,omitempty"`
	Attachment *Attachment    `protobuf:"bytes,11,opt,name=attachment,proto3" json:"attachment,omitempty"`
	ThreadRoot string         `protobuf:"bytes,12,opt,name=thread_root,json=threadRoot,proto3" json:"thread_root,omitempty"`
	ReplyCount int32          `protobuf:"varint,13,opt,name=reply_count,json=replyCount,proto3" json:"reply_count,omitempty"`
	Forward    *ForwardOrigin `protobuf:"bytes,14,opt,name=forward,proto3" json:"forward,omitempty"`
	ExpiresAt  int64          `protobuf:"varint,15,opt,name=expires_at,json=expiresAt,proto3" json:"expires_at,omitempty"`
}

// Thread requests the replies under a thread root (oldest first, forward paging).
type Thread struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId   string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	RootId   string `protobuf:"bytes,2,opt,name=root_id,json=rootId,proto3" json:"root_id,omitempty"`
	AfterSeq uint64 `protobuf:"varint,3,opt,name=after_seq,json=afterSeq,proto3" json:"after_seq,omitempty"`
	Limit    int32  `protobuf:"varint,4,opt,name=limit,proto3" json:"limit,omitempty"`
}

// ThreadOK terminates a streamed thread page.
type ThreadOK struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId    string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	RootId    string `protobuf:"bytes,2,opt,name=root_id,json=rootId,proto3" json:"root_id,omitempty"`
	NextAfter uint64 `protobuf:"varint,3,opt,name=next_after,json=nextAfter,proto3" json:"next_after,omitempty"`
	Done      bool   `protobuf:"varint,4,opt,name=done,proto3" json:"done,omitempty"`
}

type Read struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId        string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	UpToMessageId string `protobuf:"bytes,2,opt,name=up_to_message_id,json=upToMessageId,proto3" json:"up_to_message_id,omitempty"`
	UpToChatSeq   uint64 `protobuf:"varint,3,opt,name=up_to_chat_seq,json=upToChatSeq,proto3" json:"up_to_chat_seq,omitempty"`
}

type ReadUpdate struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId      string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	UserId      string `protobuf:"bytes,2,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	UpToChatSeq uint64 `protobuf:"varint,3,opt,name=up_to_chat_seq,json=upToChatSeq,proto3" json:"up_to_chat_seq,omitempty"`
}

type Typing struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	UserId string `protobuf:"bytes,2,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	Active bool   `protobuf:"varint,3,opt,name=active,proto3" json:"active,omitempty"`
}

// React toggles an emoji reaction on a message (C→S).
type React struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId    string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	MessageId string `protobuf:"bytes,2,opt,name=message_id,json=messageId,proto3" json:"message_id,omitempty"`
	Emoji     string `protobuf:"bytes,3,opt,name=emoji,proto3" json:"emoji,omitempty"`
}

// ReactUpdate announces a reaction change to a chat's members (S→C).
type ReactUpdate struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId    string           `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	MessageId string           `protobuf:"bytes,2,opt,name=message_id,json=messageId,proto3" json:"message_id,omitempty"`
	UserId    string           `protobuf:"bytes,3,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	Emoji     string           `protobuf:"bytes,4,opt,name=emoji,proto3" json:"emoji,omitempty"`
	Added     bool             `protobuf:"varint,5,opt,name=added,proto3" json:"added,omitempty"`
	Counts    map[string]int32 `protobuf:"bytes,6,rep,name=counts,proto3" json:"counts,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"varint,2,opt,name=value,proto3"`
}

type Presence struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	UserId     string `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	Online     bool   `protobuf:"varint,2,opt,name=online,proto3" json:"online,omitempty"`
	LastSeenMs int64  `protobuf:"varint,3,opt,name=last_seen_ms,json=lastSeenMs,proto3" json:"last_seen_ms,omitempty"`
}

type Edit struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId    string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	MessageId string `protobuf:"bytes,2,opt,name=message_id,json=messageId,proto3" json:"message_id,omitempty"`
	Text      string `protobuf:"bytes,3,opt,name=text,proto3" json:"text,omitempty"`
}

type Delete struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId    string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	MessageId string `protobuf:"bytes,2,opt,name=message_id,json=messageId,proto3" json:"message_id,omitempty"`
	ForAll    bool   `protobuf:"varint,3,opt,name=for_all,json=forAll,proto3" json:"for_all,omitempty"`
}

type History struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId    string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	BeforeSeq uint64 `protobuf:"varint,2,opt,name=before_seq,json=beforeSeq,proto3" json:"before_seq,omitempty"`
	Limit     int32  `protobuf:"varint,3,opt,name=limit,proto3" json:"limit,omitempty"`
}

type HistoryOK struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId     string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	NextBefore uint64 `protobuf:"varint,2,opt,name=next_before,json=nextBefore,proto3" json:"next_before,omitempty"`
	Done       bool   `protobuf:"varint,3,opt,name=done,proto3" json:"done,omitempty"`
}

type Resume struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ResumeToken string `protobuf:"bytes,1,opt,name=resume_token,json=resumeToken,proto3" json:"resume_token,omitempty"`
	LastAckSeq  uint64 `protobuf:"varint,2,opt,name=last_ack_seq,json=lastAckSeq,proto3" json:"last_ack_seq,omitempty"`
}

type ResumeOK struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	SessionId string `protobuf:"bytes,1,opt,name=session_id,json=sessionId,proto3" json:"session_id,omitempty"`
	FromSeq   uint64 `protobuf:"varint,2,opt,name=from_seq,json=fromSeq,proto3" json:"from_seq,omitempty"`
	// The NEXT resume token: resuming consumes the one that was used. A client
	// that keeps the old one looks like a thief replaying a consumed token.
	ResumeToken string `protobuf:"bytes,3,opt,name=resume_token,json=resumeToken,proto3" json:"resume_token,omitempty"`
}

type Error struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Code         uint32 `protobuf:"varint,1,opt,name=code,proto3" json:"code,omitempty"`
	Message      string `protobuf:"bytes,2,opt,name=message,proto3" json:"message,omitempty"`
	RetryAfterMs int32  `protobuf:"varint,3,opt,name=retry_after_ms,json=retryAfterMs,proto3" json:"retry_after_ms,omitempty"`
}

type MediaInit struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Filename    string `protobuf:"bytes,1,opt,name=filename,proto3" json:"filename,omitempty"`
	ContentType string `protobuf:"bytes,2,opt,name=content_type,json=contentType,proto3" json:"content_type,omitempty"`
	Size        int64  `protobuf:"varint,3,opt,name=size,proto3" json:"size,omitempty"`
}

type MediaTicket struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	MediaRef  string `protobuf:"bytes,1,opt,name=media_ref,json=mediaRef,proto3" json:"media_ref,omitempty"`
	UploadUrl string `protobuf:"bytes,2,opt,name=upload_url,json=uploadUrl,proto3" json:"upload_url,omitempty"`
	ExpiresAt int64  `protobuf:"varint,3,opt,name=expires_at,json=expiresAt,proto3" json:"expires_at,omitempty"`
}

type MediaFetch struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	MediaRef string `protobuf:"bytes,1,opt,name=media_ref,json=mediaRef,proto3" json:"media_ref,omitempty"`
}

type MediaURL struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	MediaRef    string `protobuf:"bytes,1,opt,name=media_ref,json=mediaRef,proto3" json:"media_ref,omitempty"`
	DownloadUrl string `protobuf:"bytes,2,opt,name=download_url,json=downloadUrl,proto3" json:"download_url,omitempty"`
	ExpiresAt   int64  `protobuf:"varint,3,opt,name=expires_at,json=expiresAt,proto3" json:"expires_at,omitempty"`
}

type Search struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Query string `protobuf:"bytes,1,opt,name=query,proto3" json:"query,omitempty"`
	Limit int32  `protobuf:"varint,2,opt,name=limit,proto3" json:"limit,omitempty"`
	// Optional narrowing. Membership still bounds the result — chat_id is checked
	// against the caller's membership, not trusted as a filter.
	ChatId   string `protobuf:"bytes,3,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	SenderId string `protobuf:"bytes,4,opt,name=sender_id,json=senderId,proto3" json:"sender_id,omitempty"`
}

type SearchHit struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	MessageId string `protobuf:"bytes,1,opt,name=message_id,json=messageId,proto3" json:"message_id,omitempty"`
	ChatId    string `protobuf:"bytes,2,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	SenderId  string `protobuf:"bytes,3,opt,name=sender_id,json=senderId,proto3" json:"sender_id,omitempty"`
	Seq       uint64 `protobuf:"varint,4,opt,name=seq,proto3" json:"seq,omitempty"`
	Text      string `protobuf:"bytes,5,opt,name=text,proto3" json:"text,omitempty"`
	CreatedAt int64  `protobuf:"varint,6,opt,name=created_at,json=createdAt,proto3" json:"created_at,omitempty"`
}

type SearchResults struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Query string       `protobuf:"bytes,1,opt,name=query,proto3" json:"query,omitempty"`
	Hits  []*SearchHit `protobuf:"bytes,2,rep,name=hits,proto3" json:"hits,omitempty"`
}

type KeyPublish struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	IdentityKey     string   `protobuf:"bytes,1,opt,name=identity_key,json=identityKey,proto3" json:"identity_key,omitempty"`
	SigningKey      string   `protobuf:"bytes,2,opt,name=signing_key,json=signingKey,proto3" json:"signing_key,omitempty"`
	SignedPrekey    string   `protobuf:"bytes,3,opt,name=signed_prekey,json=signedPrekey,proto3" json:"signed_prekey,omitempty"`
	SignedPrekeySig string   `protobuf:"bytes,4,opt,name=signed_prekey_sig,json=signedPrekeySig,proto3" json:"signed_prekey_sig,omitempty"`
	Prekeys         []string `protobuf:"bytes,5,rep,name=prekeys,proto3" json:"prekeys,omitempty"`
}

type KeyFetch struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	UserId   string `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	DeviceId string `protobuf:"bytes,2,opt,name=device_id,json=deviceId,proto3" json:"device_id,omitempty"`
}

type KeyBundle struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	UserId          string `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	DeviceId        string `protobuf:"bytes,2,opt,name=device_id,json=deviceId,proto3" json:"device_id,omitempty"`
	IdentityKey     string `protobuf:"bytes,3,opt,name=identity_key,json=identityKey,proto3" json:"identity_key,omitempty"`
	SigningKey      string `protobuf:"bytes,4,opt,name=signing_key,json=signingKey,proto3" json:"signing_key,omitempty"`
	SignedPrekey    string `protobuf:"bytes,5,opt,name=signed_prekey,json=signedPrekey,proto3" json:"signed_prekey,omitempty"`
	SignedPrekeySig string `protobuf:"bytes,6,opt,name=signed_prekey_sig,json=signedPrekeySig,proto3" json:"signed_prekey_sig,omitempty"`
	OneTimePrekey   string `protobuf:"bytes,7,opt,name=one_time_prekey,json=oneTimePrekey,proto3" json:"one_time_prekey,omitempty"`
}
type KeyState struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// What the directory holds AFTER this publish, capped at the per-device ceiling. A
	// client tops up below its own threshold rather than at zero: zero has already cost
	// somebody the stronger handshake.
	OneTimePrekeysLeft int32 `protobuf:"varint,1,opt,name=one_time_prekeys_left,json=oneTimePrekeysLeft,proto3" json:"one_time_prekeys_left,omitempty"`
	// How long ago the stored signed prekey first appeared; 0 when this publish
	// introduced it. A signed prekey published once and never rotated is a single key
	// protecting every future session start, and the client cannot otherwise tell
	// whether its own rotation ever landed.
	SignedPrekeyAgeMs int64 `protobuf:"varint,2,opt,name=signed_prekey_age_ms,json=signedPrekeyAgeMs,proto3" json:"signed_prekey_age_ms,omitempty"`
	// How many prekeys from THIS frame were kept. Lower than what was sent when the
	// per-publish cap or the per-device ceiling trimmed it — which a client holding the
	// private halves needs to know, or it keeps private keys for public ones nobody will
	// ever fetch.
	Accepted int32 `protobuf:"varint,3,opt,name=accepted,proto3" json:"accepted,omitempty"`
}

// KeyBundles carries every device's bundle for a user (multi-device secret sync).
type KeyBundles struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	UserId  string       `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	Bundles []*KeyBundle `protobuf:"bytes,2,rep,name=bundles,proto3" json:"bundles,omitempty"`
}

type SecretMsg struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ToUserId     string `protobuf:"bytes,1,opt,name=to_user_id,json=toUserId,proto3" json:"to_user_id,omitempty"`
	ToDeviceId   string `protobuf:"bytes,2,opt,name=to_device_id,json=toDeviceId,proto3" json:"to_device_id,omitempty"`
	FromUserId   string `protobuf:"bytes,3,opt,name=from_user_id,json=fromUserId,proto3" json:"from_user_id,omitempty"`
	FromDeviceId string `protobuf:"bytes,4,opt,name=from_device_id,json=fromDeviceId,proto3" json:"from_device_id,omitempty"`
	// The legacy TEXT form, kept for peers that do not negotiate CapSecretQueue.
	//
	// The two halves are encoded differently: ratchet_header is text (clients put a
	// JSON object there and JSON-parse it back), while ciphertext is base64.
	//
	// It was the ONLY form, and that cost 33% on every secret message plus an
	// encode and a decode at each end — in a project that wrote a binary protocol
	// for the client link specifically to avoid exactly this. It cannot simply be
	// replaced, because a client written against these fields reads nothing from the
	// bytes ones; so the choice is made per connection, by the handler that knows
	// what the peer negotiated.
	RatchetHeader string `protobuf:"bytes,5,opt,name=ratchet_header,json=ratchetHeader,proto3" json:"ratchet_header,omitempty"`
	Ciphertext    string `protobuf:"bytes,6,opt,name=ciphertext,proto3" json:"ciphertext,omitempty"`
	// Set only on a replay out of the offline queue; echoed back in SecretAcked.
	QueueId string `protobuf:"bytes,7,opt,name=queue_id,json=queueId,proto3" json:"queue_id,omitempty"`
	// The binary form. Sent to peers that negotiated CapSecretQueue, which is the
	// bit that means "this client is new enough to speak the durable secret-chat
	// protocol" — and a client that new also reads these.
	RatchetHeaderBin []byte `protobuf:"bytes,8,opt,name=ratchet_header_bin,json=ratchetHeaderBin,proto3" json:"ratchet_header_bin,omitempty"`
	CiphertextBin    []byte `protobuf:"bytes,9,opt,name=ciphertext_bin,json=ciphertextBin,proto3" json:"ciphertext_bin,omitempty"`
}
type BillingPlans struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Country string `protobuf:"bytes,1,opt,name=country,proto3" json:"country,omitempty"`
}

type BillingOffers struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Offers []*PlanOffer `protobuf:"bytes,1,rep,name=offers,proto3" json:"offers,omitempty"`
}
type PlanOffer struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Plan        string   `protobuf:"bytes,1,opt,name=plan,proto3" json:"plan,omitempty"`
	AmountMinor int64    `protobuf:"varint,2,opt,name=amount_minor,json=amountMinor,proto3" json:"amount_minor,omitempty"`
	Currency    string   `protobuf:"bytes,3,opt,name=currency,proto3" json:"currency,omitempty"`
	PeriodDays  int32    `protobuf:"varint,4,opt,name=period_days,json=periodDays,proto3" json:"period_days,omitempty"`
	Methods     []string `protobuf:"bytes,5,rep,name=methods,proto3" json:"methods,omitempty"`
}

type BillingCheckout struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Plan   string `protobuf:"bytes,1,opt,name=plan,proto3" json:"plan,omitempty"`
	Method string `protobuf:"bytes,2,opt,name=method,proto3" json:"method,omitempty"`
	// Required, and the client. A retried checkout must reach the same payment; a key
	// the server invents differs on every retry, which is the same as having none.
	IdempotencyKey string `protobuf:"bytes,3,opt,name=idempotency_key,json=idempotencyKey,proto3" json:"idempotency_key,omitempty"`
	Country        string `protobuf:"bytes,4,opt,name=country,proto3" json:"country,omitempty"`
	ReturnUrl      string `protobuf:"bytes,5,opt,name=return_url,json=returnUrl,proto3" json:"return_url,omitempty"`
}

type BillingPayment struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	PaymentId   string `protobuf:"bytes,1,opt,name=payment_id,json=paymentId,proto3" json:"payment_id,omitempty"`
	Status      string `protobuf:"bytes,2,opt,name=status,proto3" json:"status,omitempty"`
	AmountMinor int64  `protobuf:"varint,3,opt,name=amount_minor,json=amountMinor,proto3" json:"amount_minor,omitempty"`
	Currency    string `protobuf:"bytes,4,opt,name=currency,proto3" json:"currency,omitempty"`
	// pay_url is followed; qr_payload is DISPLAYED. An SBP QR payload is not a URL.
	PayUrl       string `protobuf:"bytes,5,opt,name=pay_url,json=payUrl,proto3" json:"pay_url,omitempty"`
	QrPayload    string `protobuf:"bytes,6,opt,name=qr_payload,json=qrPayload,proto3" json:"qr_payload,omitempty"`
	Deduplicated bool   `protobuf:"varint,7,opt,name=deduplicated,proto3" json:"deduplicated,omitempty"`
}

type BillingStatus struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

type BillingCancel struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}
type Subscription struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Plan               string `protobuf:"bytes,1,opt,name=plan,proto3" json:"plan,omitempty"`
	Status             string `protobuf:"bytes,2,opt,name=status,proto3" json:"status,omitempty"`
	PeriodEnd          int64  `protobuf:"varint,3,opt,name=period_end,json=periodEnd,proto3" json:"period_end,omitempty"`
	CancelAtPeriodEnd  bool   `protobuf:"varint,4,opt,name=cancel_at_period_end,json=cancelAtPeriodEnd,proto3" json:"cancel_at_period_end,omitempty"`
	SecretChats        bool   `protobuf:"varint,5,opt,name=secret_chats,json=secretChats,proto3" json:"secret_chats,omitempty"`
	MaxUploadBytes     int64  `protobuf:"varint,6,opt,name=max_upload_bytes,json=maxUploadBytes,proto3" json:"max_upload_bytes,omitempty"`
	MaxPinnedChats     int32  `protobuf:"varint,7,opt,name=max_pinned_chats,json=maxPinnedChats,proto3" json:"max_pinned_chats,omitempty"`
	Folders            bool   `protobuf:"varint,8,opt,name=folders,proto3" json:"folders,omitempty"`
	AdvancedSearch     bool   `protobuf:"varint,9,opt,name=advanced_search,json=advancedSearch,proto3" json:"advanced_search,omitempty"`
	PriorityDelivery   bool   `protobuf:"varint,10,opt,name=priority_delivery,json=priorityDelivery,proto3" json:"priority_delivery,omitempty"`
	VoiceTranscription bool   `protobuf:"varint,11,opt,name=voice_transcription,json=voiceTranscription,proto3" json:"voice_transcription,omitempty"`
	Badge              bool   `protobuf:"varint,12,opt,name=badge,proto3" json:"badge,omitempty"`
}

type PasswordChange struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	OldPassword string `protobuf:"bytes,1,opt,name=old_password,json=oldPassword,proto3" json:"old_password,omitempty"`
	NewPassword string `protobuf:"bytes,2,opt,name=new_password,json=newPassword,proto3" json:"new_password,omitempty"`
}
type PasswordChanged struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	SessionsRevoked int32 `protobuf:"varint,1,opt,name=sessions_revoked,json=sessionsRevoked,proto3" json:"sessions_revoked,omitempty"`
}
type TOTPSetup struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}
type TOTPSetupInfo struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Secret string `protobuf:"bytes,1,opt,name=secret,proto3" json:"secret,omitempty"`
	Uri    string `protobuf:"bytes,2,opt,name=uri,proto3" json:"uri,omitempty"`
}

type TOTPConfirm struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Code string `protobuf:"bytes,1,opt,name=code,proto3" json:"code,omitempty"`
}
type TOTPDisable struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Password string `protobuf:"bytes,1,opt,name=password,proto3" json:"password,omitempty"`
	Code     string `protobuf:"bytes,2,opt,name=code,proto3" json:"code,omitempty"`
}
type TOTPState struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Enabled       bool     `protobuf:"varint,1,opt,name=enabled,proto3" json:"enabled,omitempty"`
	RecoveryLeft  int32    `protobuf:"varint,2,opt,name=recovery_left,json=recoveryLeft,proto3" json:"recovery_left,omitempty"`
	RecoveryCodes []string `protobuf:"bytes,3,rep,name=recovery_codes,json=recoveryCodes,proto3" json:"recovery_codes,omitempty"`
	ConfirmedAtMs int64    `protobuf:"varint,4,opt,name=confirmed_at_ms,json=confirmedAtMs,proto3" json:"confirmed_at_ms,omitempty"`
}

type SecretAck struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ToUserId   string `protobuf:"bytes,1,opt,name=to_user_id,json=toUserId,proto3" json:"to_user_id,omitempty"`
	ToDeviceId string `protobuf:"bytes,2,opt,name=to_device_id,json=toDeviceId,proto3" json:"to_device_id,omitempty"`
	Devices    int32  `protobuf:"varint,3,opt,name=devices,proto3" json:"devices,omitempty"`
	Queued     bool   `protobuf:"varint,4,opt,name=queued,proto3" json:"queued,omitempty"`
}

// Ask for ciphertext this device missed while offline.
type SecretSync struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	After string `protobuf:"bytes,1,opt,name=after,proto3" json:"after,omitempty"`
	Limit int32  `protobuf:"varint,2,opt,name=limit,proto3" json:"limit,omitempty"`
}

// End of one sync page.
type SecretSynced struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Count     int32  `protobuf:"varint,1,opt,name=count,proto3" json:"count,omitempty"`
	NextAfter string `protobuf:"bytes,2,opt,name=next_after,json=nextAfter,proto3" json:"next_after,omitempty"`
	Done      bool   `protobuf:"varint,3,opt,name=done,proto3" json:"done,omitempty"`
}

// Confirm envelopes are stored on the device and may be dropped.
type SecretAcked struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Ids []string `protobuf:"bytes,1,rep,name=ids,proto3" json:"ids,omitempty"`
}

// ChatExport is an owner/admin dump of a cloud chat.
type ChatExport struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
}

type ChatMember struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	UserId   string `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	Role     string `protobuf:"bytes,2,opt,name=role,proto3" json:"role,omitempty"`
	JoinedAt int64  `protobuf:"varint,3,opt,name=joined_at,json=joinedAt,proto3" json:"joined_at,omitempty"`
}

type ChatExportResult struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId   string        `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	Type     string        `protobuf:"bytes,2,opt,name=type,proto3" json:"type,omitempty"`
	Title    string        `protobuf:"bytes,3,opt,name=title,proto3" json:"title,omitempty"`
	OwnerId  string        `protobuf:"bytes,4,opt,name=owner_id,json=ownerId,proto3" json:"owner_id,omitempty"`
	Members  []*ChatMember `protobuf:"bytes,5,rep,name=members,proto3" json:"members,omitempty"`
	Messages []*NewMessage `protobuf:"bytes,6,rep,name=messages,proto3" json:"messages,omitempty"`
	Done     bool          `protobuf:"varint,7,opt,name=done,proto3" json:"done,omitempty"` // true on the final page of a streamed export
}

type CallInvite struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	Kind   string `protobuf:"bytes,2,opt,name=kind,proto3" json:"kind,omitempty"`
}

type CallAction struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	CallId string `protobuf:"bytes,1,opt,name=call_id,json=callId,proto3" json:"call_id,omitempty"`
}

type CallParticipant struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	UserId   string `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	DeviceId string `protobuf:"bytes,2,opt,name=device_id,json=deviceId,proto3" json:"device_id,omitempty"`
	State    string `protobuf:"bytes,3,opt,name=state,proto3" json:"state,omitempty"`
}

type CallState struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	CallId       string             `protobuf:"bytes,1,opt,name=call_id,json=callId,proto3" json:"call_id,omitempty"`
	ChatId       string             `protobuf:"bytes,2,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	InitiatorId  string             `protobuf:"bytes,3,opt,name=initiator_id,json=initiatorId,proto3" json:"initiator_id,omitempty"`
	Kind         string             `protobuf:"bytes,4,opt,name=kind,proto3" json:"kind,omitempty"`
	State        string             `protobuf:"bytes,5,opt,name=state,proto3" json:"state,omitempty"`
	Participants []*CallParticipant `protobuf:"bytes,6,rep,name=participants,proto3" json:"participants,omitempty"`
}

type CallSignal struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	CallId       string `protobuf:"bytes,1,opt,name=call_id,json=callId,proto3" json:"call_id,omitempty"`
	ToUserId     string `protobuf:"bytes,2,opt,name=to_user_id,json=toUserId,proto3" json:"to_user_id,omitempty"`
	ToDeviceId   string `protobuf:"bytes,3,opt,name=to_device_id,json=toDeviceId,proto3" json:"to_device_id,omitempty"`
	FromUserId   string `protobuf:"bytes,4,opt,name=from_user_id,json=fromUserId,proto3" json:"from_user_id,omitempty"`
	FromDeviceId string `protobuf:"bytes,5,opt,name=from_device_id,json=fromDeviceId,proto3" json:"from_device_id,omitempty"`
	SignalType   string `protobuf:"bytes,6,opt,name=signal_type,json=signalType,proto3" json:"signal_type,omitempty"`
	Payload      string `protobuf:"bytes,7,opt,name=payload,proto3" json:"payload,omitempty"`
}

type PollCreate struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId      string   `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	Question    string   `protobuf:"bytes,2,opt,name=question,proto3" json:"question,omitempty"`
	Options     []string `protobuf:"bytes,3,rep,name=options,proto3" json:"options,omitempty"`
	MultiChoice bool     `protobuf:"varint,4,opt,name=multi_choice,json=multiChoice,proto3" json:"multi_choice,omitempty"`
	Anonymous   bool     `protobuf:"varint,5,opt,name=anonymous,proto3" json:"anonymous,omitempty"`
}

type PollVote struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	PollId string `protobuf:"bytes,1,opt,name=poll_id,json=pollId,proto3" json:"poll_id,omitempty"`
	Option int32  `protobuf:"varint,2,opt,name=option,proto3" json:"option,omitempty"`
}

type PollClose struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	PollId string `protobuf:"bytes,1,opt,name=poll_id,json=pollId,proto3" json:"poll_id,omitempty"`
}

type PollOption struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Index int32  `protobuf:"varint,1,opt,name=index,proto3" json:"index,omitempty"`
	Text  string `protobuf:"bytes,2,opt,name=text,proto3" json:"text,omitempty"`
	Votes int32  `protobuf:"varint,3,opt,name=votes,proto3" json:"votes,omitempty"`
}

type PollState struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	PollId      string        `protobuf:"bytes,1,opt,name=poll_id,json=pollId,proto3" json:"poll_id,omitempty"`
	ChatId      string        `protobuf:"bytes,2,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	MessageId   string        `protobuf:"bytes,3,opt,name=message_id,json=messageId,proto3" json:"message_id,omitempty"`
	Question    string        `protobuf:"bytes,4,opt,name=question,proto3" json:"question,omitempty"`
	Options     []*PollOption `protobuf:"bytes,5,rep,name=options,proto3" json:"options,omitempty"`
	TotalVotes  int32         `protobuf:"varint,6,opt,name=total_votes,json=totalVotes,proto3" json:"total_votes,omitempty"`
	MultiChoice bool          `protobuf:"varint,7,opt,name=multi_choice,json=multiChoice,proto3" json:"multi_choice,omitempty"`
	Anonymous   bool          `protobuf:"varint,8,opt,name=anonymous,proto3" json:"anonymous,omitempty"`
	Closed      bool          `protobuf:"varint,9,opt,name=closed,proto3" json:"closed,omitempty"`
	MyVotes     []int32       `protobuf:"varint,10,rep,packed,name=my_votes,json=myVotes,proto3" json:"my_votes,omitempty"`
}

type ContactAdd struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Target string `protobuf:"bytes,1,opt,name=target,proto3" json:"target,omitempty"`
	Name   string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
}

type ContactRemove struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Target string `protobuf:"bytes,1,opt,name=target,proto3" json:"target,omitempty"`
}

type ContactSync struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Since int64 `protobuf:"varint,1,opt,name=since,proto3" json:"since,omitempty"`
}

type Contact struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	UserId    string `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	Name      string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	Blocked   bool   `protobuf:"varint,3,opt,name=blocked,proto3" json:"blocked,omitempty"`
	UpdatedAt int64  `protobuf:"varint,4,opt,name=updated_at,json=updatedAt,proto3" json:"updated_at,omitempty"`
}

type ContactList struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Contacts []*Contact `protobuf:"bytes,1,rep,name=contacts,proto3" json:"contacts,omitempty"`
	Cursor   int64      `protobuf:"varint,2,opt,name=cursor,proto3" json:"cursor,omitempty"`
}

type Block struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Target  string `protobuf:"bytes,1,opt,name=target,proto3" json:"target,omitempty"`
	Blocked bool   `protobuf:"varint,2,opt,name=blocked,proto3" json:"blocked,omitempty"`
}

type ForwardOrigin struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId    string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	MessageId string `protobuf:"bytes,2,opt,name=message_id,json=messageId,proto3" json:"message_id,omitempty"`
	SenderId  string `protobuf:"bytes,3,opt,name=sender_id,json=senderId,proto3" json:"sender_id,omitempty"`
}

type Forward struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	FromChatId string `protobuf:"bytes,1,opt,name=from_chat_id,json=fromChatId,proto3" json:"from_chat_id,omitempty"`
	MessageId  string `protobuf:"bytes,2,opt,name=message_id,json=messageId,proto3" json:"message_id,omitempty"`
	ToChatId   string `protobuf:"bytes,3,opt,name=to_chat_id,json=toChatId,proto3" json:"to_chat_id,omitempty"`
	DedupKey   string `protobuf:"bytes,4,opt,name=dedup_key,json=dedupKey,proto3" json:"dedup_key,omitempty"`
}

type Schedule struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId     string      `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	Text       string      `protobuf:"bytes,2,opt,name=text,proto3" json:"text,omitempty"`
	MediaRef   string      `protobuf:"bytes,3,opt,name=media_ref,json=mediaRef,proto3" json:"media_ref,omitempty"`
	Attachment *Attachment `protobuf:"bytes,4,opt,name=attachment,proto3" json:"attachment,omitempty"`
	ReplyTo    string      `protobuf:"bytes,5,opt,name=reply_to,json=replyTo,proto3" json:"reply_to,omitempty"`
	TtlSeconds int32       `protobuf:"varint,6,opt,name=ttl_seconds,json=ttlSeconds,proto3" json:"ttl_seconds,omitempty"`
	SendAt     int64       `protobuf:"varint,7,opt,name=send_at,json=sendAt,proto3" json:"send_at,omitempty"`
}

type ScheduleList struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
}

type ScheduleCancel struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Id string `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
}

type ScheduledItem struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Id     string `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	ChatId string `protobuf:"bytes,2,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	Text   string `protobuf:"bytes,3,opt,name=text,proto3" json:"text,omitempty"`
	SendAt int64  `protobuf:"varint,4,opt,name=send_at,json=sendAt,proto3" json:"send_at,omitempty"`
}

type Scheduled struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Items []*ScheduledItem `protobuf:"bytes,1,rep,name=items,proto3" json:"items,omitempty"`
}

type Pin struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	MessageId string `protobuf:"bytes,1,opt,name=message_id,json=messageId,proto3" json:"message_id,omitempty"`
	PinnedBy  string `protobuf:"bytes,2,opt,name=pinned_by,json=pinnedBy,proto3" json:"pinned_by,omitempty"`
	PinnedAt  int64  `protobuf:"varint,3,opt,name=pinned_at,json=pinnedAt,proto3" json:"pinned_at,omitempty"`
}

type PinAction struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId    string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	MessageId string `protobuf:"bytes,2,opt,name=message_id,json=messageId,proto3" json:"message_id,omitempty"`
}

type Pinned struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	Pins   []*Pin `protobuf:"bytes,2,rep,name=pins,proto3" json:"pins,omitempty"`
}

type Draft struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId  string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	Text    string `protobuf:"bytes,2,opt,name=text,proto3" json:"text,omitempty"`
	ReplyTo string `protobuf:"bytes,3,opt,name=reply_to,json=replyTo,proto3" json:"reply_to,omitempty"`
}

type DraftSync struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Since int64 `protobuf:"varint,1,opt,name=since,proto3" json:"since,omitempty"`
}

type DraftItem struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId    string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	Text      string `protobuf:"bytes,2,opt,name=text,proto3" json:"text,omitempty"`
	ReplyTo   string `protobuf:"bytes,3,opt,name=reply_to,json=replyTo,proto3" json:"reply_to,omitempty"`
	UpdatedAt int64  `protobuf:"varint,4,opt,name=updated_at,json=updatedAt,proto3" json:"updated_at,omitempty"`
}

type Drafts struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Drafts []*DraftItem `protobuf:"bytes,1,rep,name=drafts,proto3" json:"drafts,omitempty"`
	Cursor int64        `protobuf:"varint,2,opt,name=cursor,proto3" json:"cursor,omitempty"`
}

type SetUsername struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId   string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	Username string `protobuf:"bytes,2,opt,name=username,proto3" json:"username,omitempty"`
}

type InviteCreate struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId    string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	ExpiresAt int64  `protobuf:"varint,2,opt,name=expires_at,json=expiresAt,proto3" json:"expires_at,omitempty"`
	MaxUses   int32  `protobuf:"varint,3,opt,name=max_uses,json=maxUses,proto3" json:"max_uses,omitempty"`
}

type InviteRevoke struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	Code   string `protobuf:"bytes,2,opt,name=code,proto3" json:"code,omitempty"`
}

type InviteList struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
}

type Join struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Code   string `protobuf:"bytes,1,opt,name=code,proto3" json:"code,omitempty"`
	Handle string `protobuf:"bytes,2,opt,name=handle,proto3" json:"handle,omitempty"`
}

type SetRole struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	UserId string `protobuf:"bytes,2,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	Role   string `protobuf:"bytes,3,opt,name=role,proto3" json:"role,omitempty"`
}

type InviteLink struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Code      string `protobuf:"bytes,1,opt,name=code,proto3" json:"code,omitempty"`
	ChatId    string `protobuf:"bytes,2,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	ExpiresAt int64  `protobuf:"varint,3,opt,name=expires_at,json=expiresAt,proto3" json:"expires_at,omitempty"`
	MaxUses   int32  `protobuf:"varint,4,opt,name=max_uses,json=maxUses,proto3" json:"max_uses,omitempty"`
	Uses      int32  `protobuf:"varint,5,opt,name=uses,proto3" json:"uses,omitempty"`
}

type Invites struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Links      []*InviteLink `protobuf:"bytes,1,rep,name=links,proto3" json:"links,omitempty"`
	JoinedChat string        `protobuf:"bytes,2,opt,name=joined_chat,json=joinedChat,proto3" json:"joined_chat,omitempty"`
}
type FanoutShard struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Body    *NewMessage `protobuf:"bytes,1,opt,name=body,proto3" json:"body,omitempty"`
	Members []string    `protobuf:"bytes,2,rep,name=members,proto3" json:"members,omitempty"`
}

type ChatCreate struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Type    string   `protobuf:"bytes,1,opt,name=type,proto3" json:"type,omitempty"`
	Title   string   `protobuf:"bytes,2,opt,name=title,proto3" json:"title,omitempty"`
	Members []string `protobuf:"bytes,3,rep,name=members,proto3" json:"members,omitempty"`
}

type ChatInfo struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId  string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	Type    string `protobuf:"bytes,2,opt,name=type,proto3" json:"type,omitempty"`
	Title   string `protobuf:"bytes,3,opt,name=title,proto3" json:"title,omitempty"`
	OwnerId string `protobuf:"bytes,4,opt,name=owner_id,json=ownerId,proto3" json:"owner_id,omitempty"`
}

type PushToken struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Token string `protobuf:"bytes,1,opt,name=token,proto3" json:"token,omitempty"`
}
type ChatList struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	After string `protobuf:"bytes,1,opt,name=after,proto3" json:"after,omitempty"`
	Limit int32  `protobuf:"varint,2,opt,name=limit,proto3" json:"limit,omitempty"`
	// The other half of the cursor: the previous page's last last_activity_at. The
	// list is ordered by activity, so a cursor naming only a chat id would skip and
	// repeat rows exactly when the chat is busy.
	AfterActivity   int64 `protobuf:"varint,3,opt,name=after_activity,json=afterActivity,proto3" json:"after_activity,omitempty"`
	IncludeArchived bool  `protobuf:"varint,4,opt,name=include_archived,json=includeArchived,proto3" json:"include_archived,omitempty"`
}
type ChatSummary struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId   string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	Type     string `protobuf:"bytes,2,opt,name=type,proto3" json:"type,omitempty"`
	Title    string `protobuf:"bytes,3,opt,name=title,proto3" json:"title,omitempty"`
	OwnerId  string `protobuf:"bytes,4,opt,name=owner_id,json=ownerId,proto3" json:"owner_id,omitempty"`
	Username string `protobuf:"bytes,5,opt,name=username,proto3" json:"username,omitempty"`
	// last_seq is the chat's newest position, so a client knows what to backfill.
	LastSeq uint64 `protobuf:"varint,6,opt,name=last_seq,json=lastSeq,proto3" json:"last_seq,omitempty"`
	// my_role is the caller's role in this chat (member | admin | owner).
	MyRole string `protobuf:"bytes,7,opt,name=my_role,json=myRole,proto3" json:"my_role,omitempty"`
	// peer_id is filled for DIRECT chats only: the other participant. A 1:1 chat
	// has no title, so without this the entry has nothing to be named after.
	PeerId string `protobuf:"bytes,8,opt,name=peer_id,json=peerId,proto3" json:"peer_id,omitempty"`
	// What a chat list actually draws, and none of it used to be sent: a client
	// wanting a preview or a badge had to call HISTORY per chat.
	LastMessage    *NewMessage `protobuf:"bytes,9,opt,name=last_message,json=lastMessage,proto3" json:"last_message,omitempty"`
	UnreadCount    int64       `protobuf:"varint,10,opt,name=unread_count,json=unreadCount,proto3" json:"unread_count,omitempty"`
	LastActivityAt int64       `protobuf:"varint,11,opt,name=last_activity_at,json=lastActivityAt,proto3" json:"last_activity_at,omitempty"`
	MutedUntil     int64       `protobuf:"varint,12,opt,name=muted_until,json=mutedUntil,proto3" json:"muted_until,omitempty"`
	Pinned         bool        `protobuf:"varint,13,opt,name=pinned,proto3" json:"pinned,omitempty"`
	Archived       bool        `protobuf:"varint,14,opt,name=archived,proto3" json:"archived,omitempty"`
}

type Chats struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Chats []*ChatSummary `protobuf:"bytes,1,rep,name=chats,proto3" json:"chats,omitempty"`
	// next_after is the cursor for the following page; done marks the last one.
	NextAfter         string `protobuf:"bytes,2,opt,name=next_after,json=nextAfter,proto3" json:"next_after,omitempty"`
	Done              bool   `protobuf:"varint,3,opt,name=done,proto3" json:"done,omitempty"`
	NextAfterActivity int64  `protobuf:"varint,4,opt,name=next_after_activity,json=nextAfterActivity,proto3" json:"next_after_activity,omitempty"`
}
type ChatFlags struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId     string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	MutedUntil int64  `protobuf:"varint,2,opt,name=muted_until,json=mutedUntil,proto3" json:"muted_until,omitempty"`
	Pinned     bool   `protobuf:"varint,3,opt,name=pinned,proto3" json:"pinned,omitempty"`
	Archived   bool   `protobuf:"varint,4,opt,name=archived,proto3" json:"archived,omitempty"`
}
type ChatFlagsSet struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId     string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	MutedUntil int64  `protobuf:"varint,2,opt,name=muted_until,json=mutedUntil,proto3" json:"muted_until,omitempty"`
	Pinned     bool   `protobuf:"varint,3,opt,name=pinned,proto3" json:"pinned,omitempty"`
	Archived   bool   `protobuf:"varint,4,opt,name=archived,proto3" json:"archived,omitempty"`
}
type ProfileGet struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Target string `protobuf:"bytes,1,opt,name=target,proto3" json:"target,omitempty"`
}
type ProfileSet struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	DisplayName string `protobuf:"bytes,1,opt,name=display_name,json=displayName,proto3" json:"display_name,omitempty"`
	AvatarRef   string `protobuf:"bytes,2,opt,name=avatar_ref,json=avatarRef,proto3" json:"avatar_ref,omitempty"`
	ClearAvatar bool   `protobuf:"varint,3,opt,name=clear_avatar,json=clearAvatar,proto3" json:"clear_avatar,omitempty"`
}

type Profile struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	UserId      string `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	Username    string `protobuf:"bytes,2,opt,name=username,proto3" json:"username,omitempty"`
	DisplayName string `protobuf:"bytes,3,opt,name=display_name,json=displayName,proto3" json:"display_name,omitempty"`
	AvatarRef   string `protobuf:"bytes,4,opt,name=avatar_ref,json=avatarRef,proto3" json:"avatar_ref,omitempty"`
}
type AccountDelete struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Password string `protobuf:"bytes,1,opt,name=password,proto3" json:"password,omitempty"`
	// Optional free text. It is written to the audit log and nowhere else — in
	// particular it never lands on a row that survives the deletion.
	Reason string `protobuf:"bytes,2,opt,name=reason,proto3" json:"reason,omitempty"`
}
type AccountDeleted struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	UserId    string `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	DeletedAt int64  `protobuf:"varint,2,opt,name=deleted_at,json=deletedAt,proto3" json:"deleted_at,omitempty"`
}

// SessionList asks for every live session of the CALLER's account.
type SessionList struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}
type SessionInfo struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	SessionId string `protobuf:"bytes,1,opt,name=session_id,json=sessionId,proto3" json:"session_id,omitempty"`
	DeviceId  string `protobuf:"bytes,2,opt,name=device_id,json=deviceId,proto3" json:"device_id,omitempty"`
	Platform  string `protobuf:"bytes,3,opt,name=platform,proto3" json:"platform,omitempty"`
	CreatedAt int64  `protobuf:"varint,4,opt,name=created_at,json=createdAt,proto3" json:"created_at,omitempty"`
	ExpiresAt int64  `protobuf:"varint,5,opt,name=expires_at,json=expiresAt,proto3" json:"expires_at,omitempty"`
	// current marks the session this connection is authenticated with, so a client
	// can label it and think twice before revoking it.
	Current bool `protobuf:"varint,6,opt,name=current,proto3" json:"current,omitempty"`
}

type Sessions struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Sessions []*SessionInfo `protobuf:"bytes,1,rep,name=sessions,proto3" json:"sessions,omitempty"`
}
type SessionRevoke struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	SessionId string `protobuf:"bytes,1,opt,name=session_id,json=sessionId,proto3" json:"session_id,omitempty"`
	// all_including_current extends the sweep to this connection too. Separate
	// from an empty session_id because "log out everywhere, including here" and
	// "log out everywhere but here" are different intentions and a client should
	// not express the difference by omitting a field.
	AllIncludingCurrent bool `protobuf:"varint,2,opt,name=all_including_current,json=allIncludingCurrent,proto3" json:"all_including_current,omitempty"`
}
type SessionRevoked struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Revoked int32 `protobuf:"varint,1,opt,name=revoked,proto3" json:"revoked,omitempty"`
	// self is true when the caller's own session was among them, so the client
	// knows to drop its token rather than wait for the connection to fail.
	Self bool `protobuf:"varint,2,opt,name=self,proto3" json:"self,omitempty"`
}
type HistoryPage struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Messages []*NewMessage `protobuf:"bytes,1,rep,name=messages,proto3" json:"messages,omitempty"`
	// chat_id is the RESOLVED id, even when the request addressed "@handle": the
	// page is the one frame that names which chat these messages came from.
	ChatId string `protobuf:"bytes,2,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	// next_before is the cursor for the following page; done marks the last one.
	NextBefore uint64 `protobuf:"varint,3,opt,name=next_before,json=nextBefore,proto3" json:"next_before,omitempty"`
	Done       bool   `protobuf:"varint,4,opt,name=done,proto3" json:"done,omitempty"`
}
type PrivacyGet struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}
type PrivacySet struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	LastSeen string `protobuf:"bytes,1,opt,name=last_seen,json=lastSeen,proto3" json:"last_seen,omitempty"`
	Avatar   string `protobuf:"bytes,2,opt,name=avatar,proto3" json:"avatar,omitempty"`
	Groups   string `protobuf:"bytes,3,opt,name=groups,proto3" json:"groups,omitempty"`
	// Whether message text may be sent to the push provider. Defaults false: the
	// previous behaviour put a preview of every message into the FCM/APNs payload,
	// which is a leak rather than a preference nobody chose.
	PushPreview bool `protobuf:"varint,4,opt,name=push_preview,json=pushPreview,proto3" json:"push_preview,omitempty"`
}

// Privacy is the current settings, echoed after a get or a set.
type Privacy struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	LastSeen    string `protobuf:"bytes,1,opt,name=last_seen,json=lastSeen,proto3" json:"last_seen,omitempty"`
	Avatar      string `protobuf:"bytes,2,opt,name=avatar,proto3" json:"avatar,omitempty"`
	Groups      string `protobuf:"bytes,3,opt,name=groups,proto3" json:"groups,omitempty"`
	PushPreview bool   `protobuf:"varint,4,opt,name=push_preview,json=pushPreview,proto3" json:"push_preview,omitempty"`
}
