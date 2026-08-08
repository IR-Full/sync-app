package presence

import (
	"context"
	"sync"
	"time"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/pkg/eventbus"
)

// Backend stores ephemeral presence. Implementations: memoryBackend, redisBackend.
type Backend interface {
	SetOnline(ctx context.Context, userID string, ttl time.Duration) error
	SetOffline(ctx context.Context, userID string, lastSeenMs int64) error
	Get(ctx context.Context, userID string) (model.Presence, error)
}

// Service tracks presence and relays typing.
type Service struct {
	backend Backend
	bus     eventbus.Bus
	ttl     time.Duration
}

// --- in-memory backend ---

type memoryBackend struct {
	mu   sync.RWMutex
	data map[string]model.Presence
}
