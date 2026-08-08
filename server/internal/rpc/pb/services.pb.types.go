package rpcpb

import protoimpl "google.golang.org/protobuf/runtime/protoimpl"

type Op int32

type User struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Id          string `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	Username    string `protobuf:"bytes,2,opt,name=username,proto3" json:"username,omitempty"`
	DisplayName string `protobuf:"bytes,3,opt,name=display_name,json=displayName,proto3" json:"display_name,omitempty"`
	CreatedAt   int64  `protobuf:"varint,4,opt,name=created_at,json=createdAt,proto3" json:"created_at,omitempty"`
	AvatarRef   string `protobuf:"bytes,5,opt,name=avatar_ref,json=avatarRef,proto3" json:"avatar_ref,omitempty"`
}

type Session struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Id          string `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	UserId      string `protobuf:"bytes,2,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	DeviceId    string `protobuf:"bytes,3,opt,name=device_id,json=deviceId,proto3" json:"device_id,omitempty"`
	Token       string `protobuf:"bytes,4,opt,name=token,proto3" json:"token,omitempty"`
	ResumeToken string `protobuf:"bytes,5,opt,name=resume_token,json=resumeToken,proto3" json:"resume_token,omitempty"`
	CreatedAt   int64  `protobuf:"varint,6,opt,name=created_at,json=createdAt,proto3" json:"created_at,omitempty"`
	ExpiresAt   int64  `protobuf:"varint,7,opt,name=expires_at,json=expiresAt,proto3" json:"expires_at,omitempty"`
	RevokedAt   int64  `protobuf:"varint,8,opt,name=revoked_at,json=revokedAt,proto3" json:"revoked_at,omitempty"`
}

type Chat struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Id        string `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	Type      string `protobuf:"bytes,2,opt,name=type,proto3" json:"type,omitempty"`
	Title     string `protobuf:"bytes,3,opt,name=title,proto3" json:"title,omitempty"`
	OwnerId   string `protobuf:"bytes,4,opt,name=owner_id,json=ownerId,proto3" json:"owner_id,omitempty"`
	CreatedAt int64  `protobuf:"varint,5,opt,name=created_at,json=createdAt,proto3" json:"created_at,omitempty"`
	LastSeq   uint64 `protobuf:"varint,6,opt,name=last_seq,json=lastSeq,proto3" json:"last_seq,omitempty"`
	Username  string `protobuf:"bytes,7,opt,name=username,proto3" json:"username,omitempty"`
}

type ChatMember struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId   string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	UserId   string `protobuf:"bytes,2,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	Role     string `protobuf:"bytes,3,opt,name=role,proto3" json:"role,omitempty"`
	JoinedAt int64  `protobuf:"varint,4,opt,name=joined_at,json=joinedAt,proto3" json:"joined_at,omitempty"`
	Muted    bool   `protobuf:"varint,5,opt,name=muted,proto3" json:"muted,omitempty"`
}

// Attachment and ForwardOrigin mirror their body.proto twins field for field.
// They are redeclared rather than imported because the two files are deliberately
// separate proto packages (see the note at the top): the shapes are the same
// domain types crossing a different boundary — service-to-service, not
// client-to-server.
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

type ForwardOrigin struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId    string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	MessageId string `protobuf:"bytes,2,opt,name=message_id,json=messageId,proto3" json:"message_id,omitempty"`
	SenderId  string `protobuf:"bytes,3,opt,name=sender_id,json=senderId,proto3" json:"sender_id,omitempty"`
}

// Message must carry EVERY field a client can observe: the gateway renders its
// wire body straight from this, so a field missing here is a field no client on
// the split deployment can ever see — even though the monolith shows it.
type Message struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Id         string         `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	ChatId     string         `protobuf:"bytes,2,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	SenderId   string         `protobuf:"bytes,3,opt,name=sender_id,json=senderId,proto3" json:"sender_id,omitempty"`
	Seq        uint64         `protobuf:"varint,4,opt,name=seq,proto3" json:"seq,omitempty"`
	Text       string         `protobuf:"bytes,5,opt,name=text,proto3" json:"text,omitempty"`
	MediaRef   string         `protobuf:"bytes,6,opt,name=media_ref,json=mediaRef,proto3" json:"media_ref,omitempty"`
	ReplyTo    string         `protobuf:"bytes,7,opt,name=reply_to,json=replyTo,proto3" json:"reply_to,omitempty"`
	Edited     bool           `protobuf:"varint,8,opt,name=edited,proto3" json:"edited,omitempty"`
	Deleted    bool           `protobuf:"varint,9,opt,name=deleted,proto3" json:"deleted,omitempty"`
	CreatedAt  int64          `protobuf:"varint,10,opt,name=created_at,json=createdAt,proto3" json:"created_at,omitempty"`
	EditedAt   int64          `protobuf:"varint,11,opt,name=edited_at,json=editedAt,proto3" json:"edited_at,omitempty"`
	Attachment *Attachment    `protobuf:"bytes,12,opt,name=attachment,proto3" json:"attachment,omitempty"`
	ThreadRoot string         `protobuf:"bytes,13,opt,name=thread_root,json=threadRoot,proto3" json:"thread_root,omitempty"`
	ReplyCount int32          `protobuf:"varint,14,opt,name=reply_count,json=replyCount,proto3" json:"reply_count,omitempty"`
	Forward    *ForwardOrigin `protobuf:"bytes,15,opt,name=forward,proto3" json:"forward,omitempty"`
	ExpiresAt  int64          `protobuf:"varint,16,opt,name=expires_at,json=expiresAt,proto3" json:"expires_at,omitempty"`
}

type RegisterRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Username    string `protobuf:"bytes,1,opt,name=username,proto3" json:"username,omitempty"`
	Password    string `protobuf:"bytes,2,opt,name=password,proto3" json:"password,omitempty"`
	DisplayName string `protobuf:"bytes,3,opt,name=display_name,json=displayName,proto3" json:"display_name,omitempty"`
	DeviceId    string `protobuf:"bytes,4,opt,name=device_id,json=deviceId,proto3" json:"device_id,omitempty"`
	Platform    string `protobuf:"bytes,5,opt,name=platform,proto3" json:"platform,omitempty"`
}

type LoginRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Username string `protobuf:"bytes,1,opt,name=username,proto3" json:"username,omitempty"`
	Password string `protobuf:"bytes,2,opt,name=password,proto3" json:"password,omitempty"`
	DeviceId string `protobuf:"bytes,3,opt,name=device_id,json=deviceId,proto3" json:"device_id,omitempty"`
	Platform string `protobuf:"bytes,4,opt,name=platform,proto3" json:"platform,omitempty"`
}

// SessionUser is the (session, user) pair returned by register/login.
type SessionUser struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Session *Session `protobuf:"bytes,1,opt,name=session,proto3" json:"session,omitempty"`
	User    *User    `protobuf:"bytes,2,opt,name=user,proto3" json:"user,omitempty"`
}

type TokenRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Token string `protobuf:"bytes,1,opt,name=token,proto3" json:"token,omitempty"`
}

type ResumeRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ResumeToken string `protobuf:"bytes,1,opt,name=resume_token,json=resumeToken,proto3" json:"resume_token,omitempty"`
}

// Identity is the resolved (session, user) behind a token/resume.
type Identity struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Session *Session `protobuf:"bytes,1,opt,name=session,proto3" json:"session,omitempty"`
	User    *User    `protobuf:"bytes,2,opt,name=user,proto3" json:"user,omitempty"`
}

type DirectRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	UserA string `protobuf:"bytes,1,opt,name=user_a,json=userA,proto3" json:"user_a,omitempty"`
	UserB string `protobuf:"bytes,2,opt,name=user_b,json=userB,proto3" json:"user_b,omitempty"`
}

type ChatIDRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
}

type CreateGroupRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	OwnerId   string   `protobuf:"bytes,1,opt,name=owner_id,json=ownerId,proto3" json:"owner_id,omitempty"`
	Title     string   `protobuf:"bytes,2,opt,name=title,proto3" json:"title,omitempty"`
	Type      string   `protobuf:"bytes,3,opt,name=type,proto3" json:"type,omitempty"`
	MemberIds []string `protobuf:"bytes,4,rep,name=member_ids,json=memberIds,proto3" json:"member_ids,omitempty"`
}

type MembersReply struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Members []*ChatMember `protobuf:"bytes,1,rep,name=members,proto3" json:"members,omitempty"`
}

type MemberIDsReply struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	UserIds []string `protobuf:"bytes,1,rep,name=user_ids,json=userIds,proto3" json:"user_ids,omitempty"`
}

// MemberPageRequest walks membership by keyset: ids after `after_user_id`,
// ordered, at most `limit`. Fanout needs this to stream a million-member channel
// instead of materializing it.
type MemberPageRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId      string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	AfterUserId string `protobuf:"bytes,2,opt,name=after_user_id,json=afterUserId,proto3" json:"after_user_id,omitempty"`
	Limit       int32  `protobuf:"varint,3,opt,name=limit,proto3" json:"limit,omitempty"`
}

// ChatUserRequest authorizes a user against a chat (CanPost / IsMember).
type ChatUserRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	UserId string `protobuf:"bytes,2,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
}

type BoolReply struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Ok bool `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
}

// UserChatsRequest pages a user's chat list by keyset over the chat id. The
// whole summary is built inside the chat service: assembling it at the gateway
// would cost one Get (plus one Members for every direct chat) per row, turning
// one screen of chats into a burst of round trips.
type UserChatsRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	UserId string `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	After  string `protobuf:"bytes,2,opt,name=after,proto3" json:"after,omitempty"`
	Limit  int32  `protobuf:"varint,3,opt,name=limit,proto3" json:"limit,omitempty"`
}

type ChatSummary struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Chat   *Chat  `protobuf:"bytes,1,opt,name=chat,proto3" json:"chat,omitempty"`
	MyRole string `protobuf:"bytes,2,opt,name=my_role,json=myRole,proto3" json:"my_role,omitempty"`
	PeerId string `protobuf:"bytes,3,opt,name=peer_id,json=peerId,proto3" json:"peer_id,omitempty"`
}

// ChatSummariesReply is one page of summaries. The cursor is not repeated here:
// it is the id of the last row, so the gateway derives it from the page itself
// and the monolith and the split deployment cannot disagree about it.
type ChatSummariesReply struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Chats []*ChatSummary `protobuf:"bytes,1,rep,name=chats,proto3" json:"chats,omitempty"`
}

type SubmitRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Op         Op          `protobuf:"varint,1,opt,name=op,proto3,enum=synapse.rpc.v1.Op" json:"op,omitempty"`
	ActorId    string      `protobuf:"bytes,2,opt,name=actor_id,json=actorId,proto3" json:"actor_id,omitempty"`
	ChatId     string      `protobuf:"bytes,3,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	MessageId  string      `protobuf:"bytes,4,opt,name=message_id,json=messageId,proto3" json:"message_id,omitempty"`
	DedupKey   string      `protobuf:"bytes,5,opt,name=dedup_key,json=dedupKey,proto3" json:"dedup_key,omitempty"`
	Text       string      `protobuf:"bytes,6,opt,name=text,proto3" json:"text,omitempty"`
	MediaRef   string      `protobuf:"bytes,7,opt,name=media_ref,json=mediaRef,proto3" json:"media_ref,omitempty"`
	ReplyTo    string      `protobuf:"bytes,8,opt,name=reply_to,json=replyTo,proto3" json:"reply_to,omitempty"`
	Attachment *Attachment `protobuf:"bytes,9,opt,name=attachment,proto3" json:"attachment,omitempty"`
	// ttl_seconds self-destructs the created message that many seconds after it
	// lands (0 = never). The deadline is computed at the write path, not here, so
	// both deployments date a message from the same clock.
	TtlSeconds int32 `protobuf:"varint,10,opt,name=ttl_seconds,json=ttlSeconds,proto3" json:"ttl_seconds,omitempty"`
}

type SubmitReply struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Message   *Message `protobuf:"bytes,1,opt,name=message,proto3" json:"message,omitempty"`
	Duplicate bool     `protobuf:"varint,2,opt,name=duplicate,proto3" json:"duplicate,omitempty"`
}

type HistoryRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	UserId    string `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	ChatId    string `protobuf:"bytes,2,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	BeforeSeq uint64 `protobuf:"varint,3,opt,name=before_seq,json=beforeSeq,proto3" json:"before_seq,omitempty"`
	Limit     int32  `protobuf:"varint,4,opt,name=limit,proto3" json:"limit,omitempty"`
}

type HistoryReply struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Messages []*Message `protobuf:"bytes,1,rep,name=messages,proto3" json:"messages,omitempty"`
}

type ThreadRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	UserId   string `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	ChatId   string `protobuf:"bytes,2,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	RootId   string `protobuf:"bytes,3,opt,name=root_id,json=rootId,proto3" json:"root_id,omitempty"`
	AfterSeq uint64 `protobuf:"varint,4,opt,name=after_seq,json=afterSeq,proto3" json:"after_seq,omitempty"`
	Limit    int32  `protobuf:"varint,5,opt,name=limit,proto3" json:"limit,omitempty"`
}

type ForwardRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	UserId    string `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	SrcChatId string `protobuf:"bytes,2,opt,name=src_chat_id,json=srcChatId,proto3" json:"src_chat_id,omitempty"`
	SrcMsgId  string `protobuf:"bytes,3,opt,name=src_msg_id,json=srcMsgId,proto3" json:"src_msg_id,omitempty"`
	DstChatId string `protobuf:"bytes,4,opt,name=dst_chat_id,json=dstChatId,proto3" json:"dst_chat_id,omitempty"`
	DedupKey  string `protobuf:"bytes,5,opt,name=dedup_key,json=dedupKey,proto3" json:"dedup_key,omitempty"`
}

type MarkReadRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	UserId  string `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	ChatId  string `protobuf:"bytes,2,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	UpToSeq uint64 `protobuf:"varint,3,opt,name=up_to_seq,json=upToSeq,proto3" json:"up_to_seq,omitempty"`
}

type Empty struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

type UserRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	UserId string `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
}

type TypingRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ChatId string `protobuf:"bytes,1,opt,name=chat_id,json=chatId,proto3" json:"chat_id,omitempty"`
	UserId string `protobuf:"bytes,2,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	Active bool   `protobuf:"varint,3,opt,name=active,proto3" json:"active,omitempty"`
}

type PublishRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	UserId          string   `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	DeviceId        string   `protobuf:"bytes,2,opt,name=device_id,json=deviceId,proto3" json:"device_id,omitempty"`
	IdentityKey     string   `protobuf:"bytes,3,opt,name=identity_key,json=identityKey,proto3" json:"identity_key,omitempty"`
	SigningKey      string   `protobuf:"bytes,4,opt,name=signing_key,json=signingKey,proto3" json:"signing_key,omitempty"`
	SignedPrekey    string   `protobuf:"bytes,5,opt,name=signed_prekey,json=signedPrekey,proto3" json:"signed_prekey,omitempty"`
	SignedPrekeySig string   `protobuf:"bytes,6,opt,name=signed_prekey_sig,json=signedPrekeySig,proto3" json:"signed_prekey_sig,omitempty"`
	Prekeys         []string `protobuf:"bytes,7,rep,name=prekeys,proto3" json:"prekeys,omitempty"`
}

type FetchRequest struct {
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

type FetchReply struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Bundle *KeyBundle `protobuf:"bytes,1,opt,name=bundle,proto3" json:"bundle,omitempty"`
	Found  bool       `protobuf:"varint,2,opt,name=found,proto3" json:"found,omitempty"`
}

type FetchAllReply struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Bundles []*KeyBundle `protobuf:"bytes,1,rep,name=bundles,proto3" json:"bundles,omitempty"`
}
