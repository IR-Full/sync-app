package platform

import (
	"log/slog"

	"github.com/redis/go-redis/v9"
	"github.com/synapse-chat/synapse/internal/presence"
	"github.com/synapse-chat/synapse/internal/replay"
	"github.com/synapse-chat/synapse/internal/router"
	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/id"
)

// Backends holds the shared infrastructure handles.
type Backends struct {
	Log      *slog.Logger
	Region   string
	NodeID   int64
	IDs      *id.Generator
	Stores   store.Stores
	Bus      eventbus.Bus
	Presence presence.Backend
	Router   router.Router
	Replay   replay.Buffer
	Redis    *redis.Client // nil unless SYNAPSE_REDIS_ADDR is set

	// MessageStore is the write path for messages: the primary store by default,
	// or a chat_id-sharded store across SYNAPSE_MESSAGE_SHARD_DSNS. MsgOutbox is
	// the set of outbox stores a relay must drain (one per shard, or the primary).
	MessageStore store.MessageStore
	MsgOutbox    []store.OutboxStore

	closers []func()
}
