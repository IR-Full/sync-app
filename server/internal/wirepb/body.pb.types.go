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

	ToUserId      string `protobuf:"bytes,1,opt,name=to_user_id,json=toUserId,proto3" json:"to_user_id,omitempty"`
	ToDeviceId    string `protobuf:"bytes,2,opt,name=to_device_id,json=toDeviceId,proto3" json:"to_device_id,omitempty"`
	FromUserId    string `protobuf:"bytes,3,opt,name=from_user_id,json=fromUserId,proto3" json:"from_user_id,omitempty"`
	FromDeviceId  string `protobuf:"bytes,4,opt,name=from_device_id,json=fromDeviceId,proto3" json:"from_device_id,omitempty"`
	RatchetHeader string `protobuf:"bytes,5,opt,name=ratchet_header,json=ratchetHeader,proto3" json:"ratchet_header,omitempty"`
	Ciphertext    string `protobuf:"bytes,6,opt,name=ciphertext,proto3" json:"ciphertext,omitempty"`
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

// FanoutShard is an INTERNAL bus payload, not an envelope body: one chunk of a
// hot chat's recipients plus the message to deliver to them. It lives here so
// everything crossing the bus is protobuf — the shard job used to be the one
// exception, encoded as JSON, which meant the heaviest payload in the system
// (a full message body per chunk) travelled in the least efficient encoding.
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

// ChatList asks for the chats the caller belongs to. It pages by keyset over the
// chat id (`after` = the last id of the previous page, empty = from the start):
// a client with more chats than fit in one frame must be able to resume where it
// stopped, and a chat id is the only cursor that is stable while the list is
// being read.
type ChatList struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	After string `protobuf:"bytes,1,opt,name=after,proto3" json:"after,omitempty"`
	Limit int32  `protobuf:"varint,2,opt,name=limit,proto3" json:"limit,omitempty"`
}

// ChatSummary is one row of the chat list: enough to render an entry without a
// follow-up round trip per chat.
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
}

type Chats struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Chats []*ChatSummary `protobuf:"bytes,1,rep,name=chats,proto3" json:"chats,omitempty"`
	// next_after is the cursor for the following page; done marks the last one.
	NextAfter string `protobuf:"bytes,2,opt,name=next_after,json=nextAfter,proto3" json:"next_after,omitempty"`
	Done      bool   `protobuf:"varint,3,opt,name=done,proto3" json:"done,omitempty"`
}

// ProfileGet reads a user's public profile. Target is a user id or "@username" —
// which also makes it the handle→user lookup, so resolving a handle no longer
// has to be smuggled through an unrelated read-only message.
type ProfileGet struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Target string `protobuf:"bytes,1,opt,name=target,proto3" json:"target,omitempty"`
}

// ProfileSet updates the CALLER's own profile. Empty fields mean "leave as is"
// (proto3 cannot tell absent from empty), so clearing the avatar is an explicit
// flag rather than an empty string.
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
