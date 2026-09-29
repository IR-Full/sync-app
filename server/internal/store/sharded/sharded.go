// Package sharded scales the message write path horizontally by partitioning it
// across N backend shards, keyed by chat_id. Every message operation for a given
// chat is routed to the same shard (FNV-1a hash of the chat id), so a chat's
// messages, its per-chat sequence counter, and its outbox stay CO-LOCATED on one
// shard. That co-location is what preserves the atomic, gap-free per-chat seq
// guarantee while spreading write/fsync load across independent primaries —
// lifting the single-Postgres write ceiling.
//
// Backends satisfy store.MessageStore, so a shard can be a Postgres primary (with
// the chat's seq co-sharded), a wide-column store (Scylla/Cassandra partitions by
// chat_id natively — the intended production target), or the in-memory store.
// The MessageStore contract is unchanged, so message.Service is oblivious to
// whether it writes to one store or a sharded fleet.
package sharded

import (
	"context"
	"errors"
	"hash/fnv"

	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/store"
)

// --- Optional capabilities ---
//
// The message store is not one interface but a base plus optional capabilities,
// discovered by type assertion (threads, the self-destruct reaper, media
// reference checks). A decorator that implements only the base therefore does
// not merely lose a method — it makes the FEATURE disappear, silently, on the
// deployments that use the decorator. Sharding is turned on by an environment
// variable, so "self-destruct works" would have quietly depended on how many
// DSNs an operator listed.
//
// Each capability below is forwarded, and the assertions underneath fail the
// build if a future one is added to store.MessageStore's optional set and not
// forwarded here.
var (
	_ store.MessageStore      = (*MessageStore)(nil)
	_ store.ThreadReader      = (*MessageStore)(nil)
	_ store.Expirer           = (*MessageStore)(nil)
	_ store.MediaReferencer   = (*MessageStore)(nil)
	_ store.MediaChatResolver = (*MessageStore)(nil)
)

// MessageStore routes store.MessageStore operations to one of N shards by chat_id.
type MessageStore struct {
	shards []store.MessageStore
}

// New builds a sharded message store over one or more backends. Ordering of the
// shards is significant (it defines the hash→shard mapping), so keep it stable
// across restarts; changing shard count reshuffles chats and is an offline
// migration, not a hot resize.
func New(shards ...store.MessageStore) *MessageStore {
	if len(shards) == 0 {
		panic("sharded: need at least one shard")
	}
	return &MessageStore{shards: shards}
}

// ShardIndex returns the shard number a chat maps to (exported for the outbox
// relay, which drains every shard, and for tests).
func (s *MessageStore) ShardIndex(chatID string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(chatID))
	return int(h.Sum32() % uint32(len(s.shards)))
}

// Shards returns the backing shards (e.g. so a relay can drain each one).
func (s *MessageStore) Shards() []store.MessageStore { return s.shards }

func (s *MessageStore) shardFor(chatID string) store.MessageStore {
	return s.shards[s.ShardIndex(chatID)]
}

func (s *MessageStore) InsertMessage(ctx context.Context, m *model.Message, dedupKey string, mkOb store.MakeOutbox) (*model.Message, bool, error) {
	return s.shardFor(m.ChatID).InsertMessage(ctx, m, dedupKey, mkOb)
}

func (s *MessageStore) GetMessage(ctx context.Context, chatID, id string) (*model.Message, error) {
	return s.shardFor(chatID).GetMessage(ctx, chatID, id)
}

func (s *MessageStore) EditMessage(ctx context.Context, chatID, id, text string, at int64, mkOb store.MakeOutbox) (*model.Message, error) {
	return s.shardFor(chatID).EditMessage(ctx, chatID, id, text, at, mkOb)
}

func (s *MessageStore) DeleteMessage(ctx context.Context, chatID, id string, at int64, mkOb store.MakeOutbox) (*model.Message, error) {
	return s.shardFor(chatID).DeleteMessage(ctx, chatID, id, at, mkOb)
}

func (s *MessageStore) History(ctx context.Context, chatID string, beforeSeq uint64, limit int) ([]*model.Message, error) {
	return s.shardFor(chatID).History(ctx, chatID, beforeSeq, limit)
}

// Thread routes to the chat's shard: a thread lives entirely inside one chat, so
// it is entirely inside one shard.
func (s *MessageStore) Thread(ctx context.Context, chatID, rootID string, afterSeq uint64, limit int) ([]*model.Message, error) {
	tr, ok := s.shardFor(chatID).(store.ThreadReader)
	if !ok {
		return nil, store.ErrNotFound
	}
	return tr.Thread(ctx, chatID, rootID, afterSeq, limit)
}

// ExpireMessages reaps across EVERY shard: a self-destruct deadline belongs to a
// message, not to a chat, so due messages are spread over all of them. The limit
// is divided so one busy shard cannot consume the whole budget and starve the
// others — a message whose time has passed must not wait on someone else's chat.
func (s *MessageStore) ExpireMessages(ctx context.Context, now int64, limit int) ([]*model.Message, error) {
	per := limit / len(s.shards)
	if per < 1 {
		per = 1
	}
	var out []*model.Message
	var errs []error
	for _, sh := range s.shards {
		ex, ok := sh.(store.Expirer)
		if !ok {
			continue
		}
		got, err := ex.ExpireMessages(ctx, now, per)
		if err != nil {
			// Record and keep going. Returning here would make one unreachable shard
			// stop the reap of every shard ordered after it — on every tick, for as
			// long as it stays down — so messages whose deadline has passed would keep
			// accumulating on shards that are perfectly healthy. Self-destruct that a
			// neighbour's outage can suspend is not self-destruct.
			errs = append(errs, err)
			continue
		}
		out = append(out, got...)
	}
	return out, errors.Join(errs...)
}

// MediaRefChats unions the answer across shards: a forward can land the same
// blob in a chat that hashes to a different shard than the original.
func (s *MessageStore) MediaRefChats(ctx context.Context, ref string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 32
	}
	seen := make(map[string]bool)
	var out []string
	for _, sh := range s.shards {
		mr, ok := sh.(store.MediaChatResolver)
		if !ok {
			continue
		}
		chats, err := mr.MediaRefChats(ctx, ref, limit)
		if err != nil {
			return nil, err
		}
		for _, c := range chats {
			if seen[c] {
				continue
			}
			seen[c] = true
			out = append(out, c)
			if len(out) == limit {
				return out, nil
			}
		}
	}
	return out, nil
}

func (s *MessageStore) MediaRefExists(ctx context.Context, ref string) (bool, error) {
	for _, sh := range s.shards {
		mr, ok := sh.(store.MediaReferencer)
		if !ok {
			// A shard that cannot answer must not be read as "no": deleting a blob on
			// an unanswerable question is unrecoverable, so this fails SAFE.
			return true, nil
		}
		used, err := mr.MediaRefExists(ctx, ref)
		if err != nil {
			return true, err
		}
		if used {
			return true, nil
		}
	}
	return false, nil
}
