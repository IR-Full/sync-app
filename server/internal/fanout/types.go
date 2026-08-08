package fanout

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/synapse-chat/synapse/internal/router"
	"github.com/synapse-chat/synapse/pkg/eventbus"
)

// Chats is the membership lookup fanout needs. An interface (not *chat.Service)
// so fanoutd can run against a gRPC chat client.
type Chats interface {
	MemberIDs(ctx context.Context, chatID string) ([]string, error)
	// MemberIDsPage walks membership by keyset so a hot chat can be streamed
	// rather than materialized.
	MemberIDsPage(ctx context.Context, chatID, afterUserID string, limit int) ([]string, error)
}

// Service consumes domain events and routes deliveries to the owning nodes.
type Service struct {
	bus    eventbus.Bus
	chats  Chats
	router router.Router
	log    *slog.Logger

	mu        sync.RWMutex
	cache     map[string]memberEntry
	lastSweep time.Time
}

// memberEntry is a chat's cached delivery shape. ids is nil when the chat is
// HOT: the whole point is not to hold a huge channel's membership, so the cache
// records the verdict instead — which is the part worth remembering.
type memberEntry struct {
	ids     []string
	hot     bool
	expires time.Time
}
