package outbox

import (
	"context"
	"log/slog"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
)

// Listener is an optional capability: a store that signals when new outbox rows
// are committed (Postgres LISTEN/NOTIFY), so the relay wakes immediately instead
// of tight polling.
type Listener interface {
	Listen(ctx context.Context) (<-chan struct{}, error)
}

// Relay publishes staged outbox events to the bus and collects them afterwards.
type Relay struct {
	store      store.OutboxStore
	bus        eventbus.Bus
	log        *slog.Logger
	interval   time.Duration
	batch      int
	retain     time.Duration
	purgeEvery time.Duration
	purgeBatch int
}
