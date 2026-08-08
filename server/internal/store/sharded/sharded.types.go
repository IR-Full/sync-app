package sharded

import "github.com/synapse-chat/synapse/internal/store"

// MessageStore routes store.MessageStore operations to one of N shards by chat_id.
type MessageStore struct {
	shards []store.MessageStore
}
