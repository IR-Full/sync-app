package rpc

import (
	"log/slog"

	pb "github.com/synapse-chat/synapse/internal/rpc/pb"
)

// AuthClient satisfies gateway.AuthService against a remote auth service.
type AuthClient struct{ c pb.AuthServiceClient }

// ChatClient satisfies gateway.ChatService and the message/search chat
// dependency (CanPost/IsMember) against a remote chat service.
type ChatClient struct{ c pb.ChatServiceClient }

// MessageClient satisfies gateway.MessageBroker and gateway.MessageReader
// against a remote message service.
type MessageClient struct{ c pb.MessageServiceClient }

// PresenceClient satisfies gateway.PresenceService against a remote service.
type PresenceClient struct{ c pb.PresenceServiceClient }

// KeyDirClient satisfies keydir.Directory against a remote key directory. The
// interface carries a context but no error, so a failure is logged and reported
// as "no bundle" — which is what the E2E path already has to handle.
type KeyDirClient struct {
	c   pb.KeyDirServiceClient
	log *slog.Logger
}
