package rpcpb

import (
	context "context"

	grpc "google.golang.org/grpc"
)

// AuthServiceClient is the client API for AuthService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type AuthServiceClient interface {
	Register(ctx context.Context, in *RegisterRequest, opts ...grpc.CallOption) (*SessionUser, error)
	Login(ctx context.Context, in *LoginRequest, opts ...grpc.CallOption) (*SessionUser, error)
	Authenticate(ctx context.Context, in *TokenRequest, opts ...grpc.CallOption) (*Identity, error)
	Resume(ctx context.Context, in *ResumeRequest, opts ...grpc.CallOption) (*Identity, error)
}

type authServiceClient struct {
	cc grpc.ClientConnInterface
}

// AuthServiceServer is the server API for AuthService service.
// All implementations must embed UnimplementedAuthServiceServer
// for forward compatibility.
type AuthServiceServer interface {
	Register(context.Context, *RegisterRequest) (*SessionUser, error)
	Login(context.Context, *LoginRequest) (*SessionUser, error)
	Authenticate(context.Context, *TokenRequest) (*Identity, error)
	Resume(context.Context, *ResumeRequest) (*Identity, error)
	mustEmbedUnimplementedAuthServiceServer()
}

// UnimplementedAuthServiceServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedAuthServiceServer struct{}

// UnsafeAuthServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to AuthServiceServer will
// result in compilation errors.
type UnsafeAuthServiceServer interface {
	mustEmbedUnimplementedAuthServiceServer()
}

// ChatServiceClient is the client API for ChatService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type ChatServiceClient interface {
	EnsureDirect(ctx context.Context, in *DirectRequest, opts ...grpc.CallOption) (*Chat, error)
	FindDirect(ctx context.Context, in *DirectRequest, opts ...grpc.CallOption) (*Chat, error)
	Get(ctx context.Context, in *ChatIDRequest, opts ...grpc.CallOption) (*Chat, error)
	CreateGroup(ctx context.Context, in *CreateGroupRequest, opts ...grpc.CallOption) (*Chat, error)
	Members(ctx context.Context, in *ChatIDRequest, opts ...grpc.CallOption) (*MembersReply, error)
	UserChats(ctx context.Context, in *UserChatsRequest, opts ...grpc.CallOption) (*ChatSummariesReply, error)
	MemberIDs(ctx context.Context, in *ChatIDRequest, opts ...grpc.CallOption) (*MemberIDsReply, error)
	MemberIDsPage(ctx context.Context, in *MemberPageRequest, opts ...grpc.CallOption) (*MemberIDsReply, error)
	// CanPost/IsMember are used by the message service (messaged is a client of
	// chatd) to authorize writes and reads.
	CanPost(ctx context.Context, in *ChatUserRequest, opts ...grpc.CallOption) (*BoolReply, error)
	IsMember(ctx context.Context, in *ChatUserRequest, opts ...grpc.CallOption) (*BoolReply, error)
}

type chatServiceClient struct {
	cc grpc.ClientConnInterface
}

// ChatServiceServer is the server API for ChatService service.
// All implementations must embed UnimplementedChatServiceServer
// for forward compatibility.
type ChatServiceServer interface {
	EnsureDirect(context.Context, *DirectRequest) (*Chat, error)
	FindDirect(context.Context, *DirectRequest) (*Chat, error)
	Get(context.Context, *ChatIDRequest) (*Chat, error)
	CreateGroup(context.Context, *CreateGroupRequest) (*Chat, error)
	Members(context.Context, *ChatIDRequest) (*MembersReply, error)
	UserChats(context.Context, *UserChatsRequest) (*ChatSummariesReply, error)
	MemberIDs(context.Context, *ChatIDRequest) (*MemberIDsReply, error)
	MemberIDsPage(context.Context, *MemberPageRequest) (*MemberIDsReply, error)
	// CanPost/IsMember are used by the message service (messaged is a client of
	// chatd) to authorize writes and reads.
	CanPost(context.Context, *ChatUserRequest) (*BoolReply, error)
	IsMember(context.Context, *ChatUserRequest) (*BoolReply, error)
	mustEmbedUnimplementedChatServiceServer()
}

// UnimplementedChatServiceServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedChatServiceServer struct{}

// UnsafeChatServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to ChatServiceServer will
// result in compilation errors.
type UnsafeChatServiceServer interface {
	mustEmbedUnimplementedChatServiceServer()
}

// MessageServiceClient is the client API for MessageService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
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

// MessageServiceServer is the server API for MessageService service.
// All implementations must embed UnimplementedMessageServiceServer
// for forward compatibility.
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

// UnsafeMessageServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to MessageServiceServer will
// result in compilation errors.
type UnsafeMessageServiceServer interface {
	mustEmbedUnimplementedMessageServiceServer()
}

// PresenceServiceClient is the client API for PresenceService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type PresenceServiceClient interface {
	Online(ctx context.Context, in *UserRequest, opts ...grpc.CallOption) (*Empty, error)
	Heartbeat(ctx context.Context, in *UserRequest, opts ...grpc.CallOption) (*Empty, error)
	Offline(ctx context.Context, in *UserRequest, opts ...grpc.CallOption) (*Empty, error)
	Typing(ctx context.Context, in *TypingRequest, opts ...grpc.CallOption) (*Empty, error)
}

type presenceServiceClient struct {
	cc grpc.ClientConnInterface
}

// PresenceServiceServer is the server API for PresenceService service.
// All implementations must embed UnimplementedPresenceServiceServer
// for forward compatibility.
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

// UnsafePresenceServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to PresenceServiceServer will
// result in compilation errors.
type UnsafePresenceServiceServer interface {
	mustEmbedUnimplementedPresenceServiceServer()
}

// KeyDirServiceClient is the client API for KeyDirService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type KeyDirServiceClient interface {
	Publish(ctx context.Context, in *PublishRequest, opts ...grpc.CallOption) (*Empty, error)
	Fetch(ctx context.Context, in *FetchRequest, opts ...grpc.CallOption) (*FetchReply, error)
	FetchAll(ctx context.Context, in *UserRequest, opts ...grpc.CallOption) (*FetchAllReply, error)
}

type keyDirServiceClient struct {
	cc grpc.ClientConnInterface
}

// KeyDirServiceServer is the server API for KeyDirService service.
// All implementations must embed UnimplementedKeyDirServiceServer
// for forward compatibility.
type KeyDirServiceServer interface {
	Publish(context.Context, *PublishRequest) (*Empty, error)
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

// UnsafeKeyDirServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to KeyDirServiceServer will
// result in compilation errors.
type UnsafeKeyDirServiceServer interface {
	mustEmbedUnimplementedKeyDirServiceServer()
}
