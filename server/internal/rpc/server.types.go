package rpc

import (
	"github.com/SyncApp-chat/SyncApp/internal/auth"
	"github.com/SyncApp-chat/SyncApp/internal/chat"
	"github.com/SyncApp-chat/SyncApp/internal/keydir"
	"github.com/SyncApp-chat/SyncApp/internal/message"
	"github.com/SyncApp-chat/SyncApp/internal/presence"
	pb "github.com/SyncApp-chat/SyncApp/internal/rpc/pb"
)

// AuthServer exposes an *auth.Service over gRPC.
type AuthServer struct {
	pb.UnimplementedAuthServiceServer
	svc *auth.Service
}

// ChatServer exposes a *chat.Service over gRPC.
type ChatServer struct {
	pb.UnimplementedChatServiceServer
	svc *chat.Service
}

// MessageServer exposes the message broker (writes) and read service over gRPC.
type MessageServer struct {
	pb.UnimplementedMessageServiceServer
	broker *message.Broker
	reader *message.Service
}

// PresenceServer exposes a *presence.Service over gRPC.
type PresenceServer struct {
	pb.UnimplementedPresenceServiceServer
	svc *presence.Service
}

// KeyDirServer exposes a keydir.Directory over gRPC.
type KeyDirServer struct {
	pb.UnimplementedKeyDirServiceServer
	dir keydir.Directory
}
