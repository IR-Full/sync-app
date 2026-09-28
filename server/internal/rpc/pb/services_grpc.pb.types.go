package rpcpb

import (
	context "context"
	grpc "google.golang.org/grpc"
)

type AuthServiceClient interface {
	Register(ctx context.Context, in *RegisterRequest, opts ...grpc.CallOption) (*SessionUser, error)
	Login(ctx context.Context, in *LoginRequest, opts ...grpc.CallOption) (*SessionUser, error)
	Authenticate(ctx context.Context, in *TokenRequest, opts ...grpc.CallOption) (*Identity, error)
	Resume(ctx context.Context, in *ResumeRequest, opts ...grpc.CallOption) (*Identity, error)
	ListSessions(ctx context.Context, in *UserIDRequest, opts ...grpc.CallOption) (*SessionsReply, error)
	RevokeOwned(ctx context.Context, in *RevokeOwnedRequest, opts ...grpc.CallOption) (*Empty, error)
	RevokeAll(ctx context.Context, in *RevokeAllRequest, opts ...grpc.CallOption) (*RevokeAllReply, error)
	DeleteAccount(ctx context.Context, in *DeleteAccountRequest, opts ...grpc.CallOption) (*Empty, error)
	// Account security. None of this existed: the password could not be changed by
	// any path, and there was no second factor at all.
	LoginWithCode(ctx context.Context, in *LoginWithCodeRequest, opts ...grpc.CallOption) (*SessionUser, error)
	ChangePassword(ctx context.Context, in *ChangePasswordRequest, opts ...grpc.CallOption) (*ChangePasswordReply, error)
	BeginTOTP(ctx context.Context, in *BeginTOTPRequest, opts ...grpc.CallOption) (*BeginTOTPReply, error)
	ConfirmTOTP(ctx context.Context, in *ConfirmTOTPRequest, opts ...grpc.CallOption) (*ConfirmTOTPReply, error)
	DisableTOTP(ctx context.Context, in *DisableTOTPRequest, opts ...grpc.CallOption) (*Empty, error)
	TwoFactorState(ctx context.Context, in *UserIDRequest, opts ...grpc.CallOption) (*TwoFactorStateReply, error)
}

type authServiceClient struct {
	cc grpc.ClientConnInterface
}
type AuthServiceServer interface {
	Register(context.Context, *RegisterRequest) (*SessionUser, error)
	Login(context.Context, *LoginRequest) (*SessionUser, error)
	Authenticate(context.Context, *TokenRequest) (*Identity, error)
	Resume(context.Context, *ResumeRequest) (*Identity, error)
	ListSessions(context.Context, *UserIDRequest) (*SessionsReply, error)
	RevokeOwned(context.Context, *RevokeOwnedRequest) (*Empty, error)
	RevokeAll(context.Context, *RevokeAllRequest) (*RevokeAllReply, error)
	DeleteAccount(context.Context, *DeleteAccountRequest) (*Empty, error)
	// Account security. None of this existed: the password could not be changed by
	// any path, and there was no second factor at all.
	LoginWithCode(context.Context, *LoginWithCodeRequest) (*SessionUser, error)
	ChangePassword(context.Context, *ChangePasswordRequest) (*ChangePasswordReply, error)
	BeginTOTP(context.Context, *BeginTOTPRequest) (*BeginTOTPReply, error)
	ConfirmTOTP(context.Context, *ConfirmTOTPRequest) (*ConfirmTOTPReply, error)
	DisableTOTP(context.Context, *DisableTOTPRequest) (*Empty, error)
	TwoFactorState(context.Context, *UserIDRequest) (*TwoFactorStateReply, error)
	mustEmbedUnimplementedAuthServiceServer()
}

// UnimplementedAuthServiceServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedAuthServiceServer struct{}
type UnsafeAuthServiceServer interface {
	mustEmbedUnimplementedAuthServiceServer()
}
type ChatServiceClient interface {
	EnsureDirect(ctx context.Context, in *DirectRequest, opts ...grpc.CallOption) (*Chat, error)
	// EnsureSecret is EnsureDirect for the SECRET type. A separate RPC rather than a
	// type field on DirectRequest, so the existing call keeps its exact meaning.
	EnsureSecret(ctx context.Context, in *DirectRequest, opts ...grpc.CallOption) (*Chat, error)
	FindDirect(ctx context.Context, in *DirectRequest, opts ...grpc.CallOption) (*Chat, error)
	Get(ctx context.Context, in *ChatIDRequest, opts ...grpc.CallOption) (*Chat, error)
	CreateGroup(ctx context.Context, in *CreateGroupRequest, opts ...grpc.CallOption) (*Chat, error)
	Members(ctx context.Context, in *ChatIDRequest, opts ...grpc.CallOption) (*MembersReply, error)
	UserChats(ctx context.Context, in *UserChatsRequest, opts ...grpc.CallOption) (*ChatSummariesReply, error)
	// UserChatPage is UserChats with the composite cursor and the archived filter.
	// Separate rather than extra fields on UserChatsRequest so the old call keeps
	// its exact meaning for any caller that has not moved.
	UserChatPage(ctx context.Context, in *UserChatPageRequest, opts ...grpc.CallOption) (*ChatSummariesReply, error)
	// SetChatFlags writes the CALLER's own mute/pin/archive for a chat.
	SetChatFlags(ctx context.Context, in *SetChatFlagsRequest, opts ...grpc.CallOption) (*ChatFlagsReply, error)
	// UserChatIDs lists the chats a user belongs to, ids only — what search needs
	// to bound a query. UserChats cannot serve it: it pages, and it builds full
	// summaries that the caller throws away.
	UserChatIDs(ctx context.Context, in *UserIDRequest, opts ...grpc.CallOption) (*MemberIDsReply, error)
	MemberIDs(ctx context.Context, in *ChatIDRequest, opts ...grpc.CallOption) (*MemberIDsReply, error)
	MemberIDsPage(ctx context.Context, in *MemberPageRequest, opts ...grpc.CallOption) (*MemberIDsReply, error)
	// CanPost/IsMember are used by the message service (messaged is a client of
	// chatd) to authorize writes and reads.
	CanPost(ctx context.Context, in *ChatUserRequest, opts ...grpc.CallOption) (*BoolReply, error)
	IsMember(ctx context.Context, in *ChatUserRequest, opts ...grpc.CallOption) (*BoolReply, error)
	// CanModerate authorizes acting on ANOTHER member's message (deletion). It is
	// its own call rather than a flag on CanPost because the two answers differ in
	// exactly the case that matters: in a group every member may post and only
	// admins may moderate.
	CanModerate(ctx context.Context, in *ChatUserRequest, opts ...grpc.CallOption) (*BoolReply, error)
	// CountPinnedChatsExcept backs the MaxPinnedChats entitlement, which the
	// gateway enforces because only the gateway knows the caller's tier. The
	// exclusion lets it answer "is there room for THIS chat" without tripping over
	// a client re-sending flags for one that is already pinned.
	CountPinnedChatsExcept(ctx context.Context, in *PinnedCountRequest, opts ...grpc.CallOption) (*CountReply, error)
}

type chatServiceClient struct {
	cc grpc.ClientConnInterface
}
type ChatServiceServer interface {
	EnsureDirect(context.Context, *DirectRequest) (*Chat, error)
	// EnsureSecret is EnsureDirect for the SECRET type. A separate RPC rather than a
	// type field on DirectRequest, so the existing call keeps its exact meaning.
	EnsureSecret(context.Context, *DirectRequest) (*Chat, error)
	FindDirect(context.Context, *DirectRequest) (*Chat, error)
	Get(context.Context, *ChatIDRequest) (*Chat, error)
	CreateGroup(context.Context, *CreateGroupRequest) (*Chat, error)
	Members(context.Context, *ChatIDRequest) (*MembersReply, error)
	UserChats(context.Context, *UserChatsRequest) (*ChatSummariesReply, error)
	// UserChatPage is UserChats with the composite cursor and the archived filter.
	// Separate rather than extra fields on UserChatsRequest so the old call keeps
	// its exact meaning for any caller that has not moved.
	UserChatPage(context.Context, *UserChatPageRequest) (*ChatSummariesReply, error)
	// SetChatFlags writes the CALLER's own mute/pin/archive for a chat.
	SetChatFlags(context.Context, *SetChatFlagsRequest) (*ChatFlagsReply, error)
	// UserChatIDs lists the chats a user belongs to, ids only — what search needs
	// to bound a query. UserChats cannot serve it: it pages, and it builds full
	// summaries that the caller throws away.
	UserChatIDs(context.Context, *UserIDRequest) (*MemberIDsReply, error)
	MemberIDs(context.Context, *ChatIDRequest) (*MemberIDsReply, error)
	MemberIDsPage(context.Context, *MemberPageRequest) (*MemberIDsReply, error)
	// CanPost/IsMember are used by the message service (messaged is a client of
	// chatd) to authorize writes and reads.
	CanPost(context.Context, *ChatUserRequest) (*BoolReply, error)
	IsMember(context.Context, *ChatUserRequest) (*BoolReply, error)
	// CanModerate authorizes acting on ANOTHER member's message (deletion). It is
	// its own call rather than a flag on CanPost because the two answers differ in
	// exactly the case that matters: in a group every member may post and only
	// admins may moderate.
	CanModerate(context.Context, *ChatUserRequest) (*BoolReply, error)
	// CountPinnedChatsExcept backs the MaxPinnedChats entitlement, which the
	// gateway enforces because only the gateway knows the caller's tier. The
	// exclusion lets it answer "is there room for THIS chat" without tripping over
	// a client re-sending flags for one that is already pinned.
	CountPinnedChatsExcept(context.Context, *PinnedCountRequest) (*CountReply, error)
	mustEmbedUnimplementedChatServiceServer()
}

// UnimplementedChatServiceServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedChatServiceServer struct{}
type UnsafeChatServiceServer interface {
	mustEmbedUnimplementedChatServiceServer()
}
type MessageServiceClient interface {
	Submit(ctx context.Context, in *SubmitRequest, opts ...grpc.CallOption) (*SubmitReply, error)
	History(ctx context.Context, in *HistoryRequest, opts ...grpc.CallOption) (*HistoryReply, error)
	Thread(ctx context.Context, in *ThreadRequest, opts ...grpc.CallOption) (*HistoryReply, error)
	Forward(ctx context.Context, in *ForwardRequest, opts ...grpc.CallOption) (*SubmitReply, error)
	MarkRead(ctx context.Context, in *MarkReadRequest, opts ...grpc.CallOption) (*Empty, error)
}

type messageServiceClient struct {
	cc grpc.ClientConnInterface
}
type MessageServiceServer interface {
	Submit(context.Context, *SubmitRequest) (*SubmitReply, error)
	History(context.Context, *HistoryRequest) (*HistoryReply, error)
	Thread(context.Context, *ThreadRequest) (*HistoryReply, error)
	Forward(context.Context, *ForwardRequest) (*SubmitReply, error)
	MarkRead(context.Context, *MarkReadRequest) (*Empty, error)
	mustEmbedUnimplementedMessageServiceServer()
}

// UnimplementedMessageServiceServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedMessageServiceServer struct{}
type UnsafeMessageServiceServer interface {
	mustEmbedUnimplementedMessageServiceServer()
}
type PresenceServiceClient interface {
	Online(ctx context.Context, in *UserRequest, opts ...grpc.CallOption) (*Empty, error)
	Heartbeat(ctx context.Context, in *UserRequest, opts ...grpc.CallOption) (*Empty, error)
	Offline(ctx context.Context, in *UserRequest, opts ...grpc.CallOption) (*Empty, error)
	Typing(ctx context.Context, in *TypingRequest, opts ...grpc.CallOption) (*Empty, error)
}

type presenceServiceClient struct {
	cc grpc.ClientConnInterface
}
type PresenceServiceServer interface {
	Online(context.Context, *UserRequest) (*Empty, error)
	Heartbeat(context.Context, *UserRequest) (*Empty, error)
	Offline(context.Context, *UserRequest) (*Empty, error)
	Typing(context.Context, *TypingRequest) (*Empty, error)
	mustEmbedUnimplementedPresenceServiceServer()
}

// UnimplementedPresenceServiceServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedPresenceServiceServer struct{}
type UnsafePresenceServiceServer interface {
	mustEmbedUnimplementedPresenceServiceServer()
}
type KeyDirServiceClient interface {
	Publish(ctx context.Context, in *PublishRequest, opts ...grpc.CallOption) (*PublishReply, error)
	Fetch(ctx context.Context, in *FetchRequest, opts ...grpc.CallOption) (*FetchReply, error)
	FetchAll(ctx context.Context, in *UserRequest, opts ...grpc.CallOption) (*FetchAllReply, error)
}

type keyDirServiceClient struct {
	cc grpc.ClientConnInterface
}
type KeyDirServiceServer interface {
	Publish(context.Context, *PublishRequest) (*PublishReply, error)
	Fetch(context.Context, *FetchRequest) (*FetchReply, error)
	FetchAll(context.Context, *UserRequest) (*FetchAllReply, error)
	mustEmbedUnimplementedKeyDirServiceServer()
}

// UnimplementedKeyDirServiceServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedKeyDirServiceServer struct{}
type UnsafeKeyDirServiceServer interface {
	mustEmbedUnimplementedKeyDirServiceServer()
}
