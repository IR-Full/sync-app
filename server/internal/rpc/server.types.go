package rpc

import (
	"github.com/synapse-chat/synapse/internal/auth"
	"github.com/synapse-chat/synapse/internal/chat"
	"github.com/synapse-chat/synapse/internal/keydir"
	"github.com/synapse-chat/synapse/internal/message"
	"github.com/synapse-chat/synapse/internal/presence"
	pb "github.com/synapse-chat/synapse/internal/rpc/pb"
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
