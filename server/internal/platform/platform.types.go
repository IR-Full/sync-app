package platform

import (
	"log/slog"

	"github.com/SyncApp-chat/SyncApp/internal/presence"
	"github.com/SyncApp-chat/SyncApp/internal/replay"
	"github.com/SyncApp-chat/SyncApp/internal/router"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
	"github.com/SyncApp-chat/SyncApp/pkg/id"
	"github.com/redis/go-redis/v9"
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
	Redis    *redis.Client // nil unless SYNCAPP_REDIS_ADDR is set

	// MessageStore is the write path for messages: the primary store by default,
	// or a chat_id-sharded store across SYNCAPP_MESSAGE_SHARD_DSNS. MsgOutbox is
	// the set of outbox stores a relay must drain (one per shard, or the primary).
	MessageStore store.MessageStore
	MsgOutbox    []store.OutboxStore

	closers []func()
}
