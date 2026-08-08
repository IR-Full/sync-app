package sharded_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/internal/store/postgres"
	"github.com/synapse-chat/synapse/internal/store/sharded"
	"github.com/synapse-chat/synapse/pkg/id"
)

// The unit tests prove the ROUTING with fake shards. What they cannot prove is
// that a real backend behaves correctly when it is one of several: that each
// shard allocates its own gap-free per-chat sequence, that each stages its own
// outbox, and that the capabilities forwarded across shards (the self-destruct
// reaper, the media reference check) actually reach data living in a shard the
// caller never named.
//
// That last part is the reason this file exists. A capability implemented by
// asking "the" shard is indistinguishable from a correct one until a message
// lands somewhere else — and which shard a chat lands on is a hash, so a bug
// here would surface as messages that never expire for SOME chats.
//
// The DSNs must name DATABASES OF THEIR OWN — including versus
// SYNAPSE_TEST_PG_DSN. The outbox is a global table that both this package and
// the postgres package drain, so pointing two suites at one database makes them
// delete each other's staged events and fail in whichever order they happened to
// interleave. That is a property of shared mutable state, not a bug in either
// test, and the only fix is not to share.
//
// Runs only when SYNAPSE_TEST_SHARD_DSNS is set to two or more comma-separated
// DSNs, e.g.:
//
//	SYNAPSE_TEST_SHARD_DSNS="postgres://…:55432/synapse?sslmode=disable,postgres://…:55433/synapse?sslmode=disable" \
//	  go test ./internal/store/sharded -run TestSharded

func openShards(t *testing.T) ([]*postgres.Store, []store.MessageStore) {
	t.Helper()
	dsns := os.Getenv("SYNAPSE_TEST_SHARD_DSNS")
	if dsns == "" {
		t.Skip("set SYNAPSE_TEST_SHARD_DSNS (2+ comma-separated DSNs) to run the sharded integration test")
	}
	parts := strings.Split(dsns, ",")
	if len(parts) < 2 {
		t.Skip("sharding is only meaningful with 2+ shards; set SYNAPSE_TEST_SHARD_DSNS to several DSNs")
	}
	ctx := context.Background()
	var stores []*postgres.Store
	var shards []store.MessageStore
	for i, dsn := range parts {
		ps, err := postgres.Connect(ctx, strings.TrimSpace(dsn))
		if err != nil {
			t.Fatalf("connect shard %d: %v", i, err)
		}
		// Every shard carries the FULL schema: a shard is a complete message store,
		// not a partial one, which is what lets a chat live entirely inside it.
		if err := ps.Migrate(ctx); err != nil {
			t.Fatalf("migrate shard %d: %v", i, err)
		}
		t.Cleanup(ps.Close)
		stores = append(stores, ps)
		shards = append(shards, ps)
	}
	return stores, shards
}

// seedChat creates the user/chat/membership a message needs, on every shard.
// Chat metadata lives in the primary store in production; here each shard gets
// its own copy so a message can be written to whichever one the hash picks.
func seedChat(t *testing.T, stores []*postgres.Store, ids *id.Generator, userID, chatID string, now int64) {
	t.Helper()
	ctx := context.Background()
	for _, ps := range stores {
		if err := ps.CreateUser(ctx, &model.User{
			ID: userID, Username: "sh_" + userID, PasswordHash: "x", CreatedAt: now,
		}); err != nil && !isConflict(err) {
			t.Fatalf("create user: %v", err)
		}
		if err := ps.CreateChat(ctx, &model.Chat{
			ID: chatID, Type: model.ChatGroup, OwnerID: userID, CreatedAt: now,
		}); err != nil {
			t.Fatalf("create chat: %v", err)
		}
		if err := ps.AddMember(ctx, &model.ChatMember{
			ChatID: chatID, UserID: userID, Role: model.RoleOwner, JoinedAt: now,
		}); err != nil {
			t.Fatalf("add member: %v", err)
		}
	}
}

func isConflict(err error) bool { return err != nil && strings.Contains(err.Error(), "conflict") }

// TestShardedRealPostgresCoLocatesAndSpreads pins the two properties sharding
// exists for, against real backends: a chat's messages (and its sequence) stay
// on ONE shard, and different chats actually land on different ones — a router
// that sent everything to shard 0 would pass every co-location assertion while
// delivering none of the benefit.
func TestShardedRealPostgresCoLocatesAndSpreads(t *testing.T) {
	stores, shards := openShards(t)
	s := sharded.New(shards...)
	ids, _ := id.NewGenerator(21)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	user := ids.NextString()
	// Enough chats that the hash is very unlikely to put them all in one shard.
	chats := make([]string, 12)
	for i := range chats {
		chats[i] = ids.NextString()
		seedChat(t, stores, ids, user, chats[i], now)
	}

	const perChat = 3
	used := map[int]bool{}
	for _, chatID := range chats {
		used[s.ShardIndex(chatID)] = true
		for i := 0; i < perChat; i++ {
			m := &model.Message{
				ID: ids.NextString(), ChatID: chatID, SenderID: user,
				Text: "hello", CreatedAt: time.Now().UnixMilli(),
			}
			stored, dup, err := s.InsertMessage(ctx, m, "", nil)
			if err != nil {
				t.Fatalf("insert into chat %s: %v", chatID, err)
			}
			if dup {
				t.Fatal("unexpected duplicate")
			}
			// The sequence is allocated by the shard that owns the chat, so it must
			// still be gap-free and start at 1 — the guarantee sharding must not cost.
			if stored.Seq != uint64(i+1) {
				t.Fatalf("chat %s got seq %d for message %d", chatID, stored.Seq, i+1)
			}
		}
	}
	if len(used) < 2 {
		t.Fatalf("every chat hashed to %d shard(s); the test proves nothing about spreading", len(used))
	}

	// Co-location: each chat's messages are readable through the sharded store,
	// and present in EXACTLY ONE backing shard.
	for _, chatID := range chats {
		msgs, err := s.History(ctx, chatID, 0, 10)
		if err != nil {
			t.Fatalf("history for %s: %v", chatID, err)
		}
		if len(msgs) != perChat {
			t.Fatalf("chat %s: %d messages through the sharded store, want %d", chatID, len(msgs), perChat)
		}
		holders := 0
		for _, ps := range stores {
			direct, err := ps.History(ctx, chatID, 0, 10)
			if err != nil {
				t.Fatalf("direct history: %v", err)
			}
			if len(direct) > 0 {
				holders++
				if len(direct) != perChat {
					t.Fatalf("chat %s is split: one shard holds %d of %d", chatID, len(direct), perChat)
				}
			}
		}
		if holders != 1 {
			t.Fatalf("chat %s lives on %d shards; co-location is broken", chatID, holders)
		}
	}
}

// TestShardedRealPostgresForwardsCapabilities is the one the unit tests cannot
// write: data placed by the HASH into a shard nobody named must still be reached
// by the reaper and by the media reference check.
func TestShardedRealPostgresForwardsCapabilities(t *testing.T) {
	stores, shards := openShards(t)
	s := sharded.New(shards...)
	ids, _ := id.NewGenerator(22)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	user := ids.NextString()

	// Find one chat per shard, so the assertions below cover every backend rather
	// than whichever one the first id happened to hash to.
	perShard := make([]string, len(shards))
	for filled := 0; filled < len(shards); {
		chatID := ids.NextString()
		idx := s.ShardIndex(chatID)
		if perShard[idx] != "" {
			continue
		}
		perShard[idx] = chatID
		seedChat(t, stores, ids, user, chatID, now)
		filled++
	}

	// A self-destructing message in EVERY shard, already past its deadline.
	past := time.Now().Add(-time.Minute).UnixMilli()
	mine := map[string]int{} // message id → shard it should be reaped from
	for _, chatID := range perShard {
		m := &model.Message{
			ID: ids.NextString(), ChatID: chatID, SenderID: user, Text: "burn",
			MediaRef: "blob-" + chatID, CreatedAt: past, ExpiresAt: past,
		}
		if _, _, err := s.InsertMessage(ctx, m, "", nil); err != nil {
			t.Fatalf("insert expiring message: %v", err)
		}
		mine[m.ID] = s.ShardIndex(chatID)
	}

	// The media check must find a reference wherever it lives: the ref below is in
	// the LAST shard, and the caller never says which shard to ask.
	last := perShard[len(perShard)-1]
	used, err := s.MediaRefExists(ctx, "blob-"+last)
	if err != nil {
		t.Fatalf("media ref check: %v", err)
	}
	if !used {
		t.Fatal("a blob referenced from another shard was reported unreachable — the sweep would delete it")
	}
	if used, err := s.MediaRefExists(ctx, "blob-nobody-has-this"); err != nil || used {
		t.Fatalf("an unreferenced blob was reported as kept: used=%v err=%v", used, err)
	}

	// The reaper must visit every shard: a deadline belongs to a message, and the
	// hash decides where those messages are.
	//
	// The assertion is about MY messages, not about how many rows the databases
	// happen to hold. These shards are shared with every other test (and with
	// whatever a previous run left behind), so counting globally would make this
	// test report the state of the environment instead of the behaviour of the
	// code — and it would do so intermittently, which is worse than not testing.
	found := map[string]bool{}
	for pass := 0; pass < 5 && len(found) < len(mine); pass++ {
		expired, err := s.ExpireMessages(ctx, time.Now().UnixMilli(), 100)
		if err != nil {
			t.Fatalf("expire: %v", err)
		}
		if len(expired) == 0 {
			break
		}
		for _, m := range expired {
			if _, ok := mine[m.ID]; ok {
				found[m.ID] = true
			}
		}
	}
	if len(found) != len(mine) {
		t.Fatalf("reaper tombstoned %d of the %d messages it was given, one per shard", len(found), len(mine))
	}
	reached := map[int]bool{}
	for id := range found {
		reached[mine[id]] = true
	}
	if len(reached) != len(shards) {
		t.Fatalf("reaper only reached %d of %d shards", len(reached), len(shards))
	}

	// Tombstoned means unreachable: the blob is now collectable too, which is the
	// property that makes self-destruct true of the bytes and not only the text.
	if used, err := s.MediaRefExists(ctx, "blob-"+last); err != nil || used {
		t.Fatalf("an expired message still holds its blob: used=%v err=%v", used, err)
	}
}

// TestShardedRealPostgresStagesOutboxPerShard pins the part a relay depends on:
// the event for a message is staged in the SAME shard the message went to, so a
// relay draining every shard sees each event exactly once — and a relay that
// drained only the primary would silently lose the rest.
func TestShardedRealPostgresStagesOutboxPerShard(t *testing.T) {
	stores, shards := openShards(t)
	s := sharded.New(shards...)
	ids, _ := id.NewGenerator(23)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	user := ids.NextString()

	// Drain anything left by earlier tests so the counts below mean what they say.
	for _, ps := range stores {
		for {
			recs, err := ps.Poll(ctx, 500)
			if err != nil {
				t.Fatalf("drain: %v", err)
			}
			if len(recs) == 0 {
				break
			}
			ids := make([]string, len(recs))
			for i, r := range recs {
				ids[i] = r.ID
			}
			if err := ps.MarkSent(ctx, ids); err != nil {
				t.Fatalf("drain mark: %v", err)
			}
		}
	}

	perShard := make([]string, len(shards))
	for filled := 0; filled < len(shards); {
		chatID := ids.NextString()
		idx := s.ShardIndex(chatID)
		if perShard[idx] != "" {
			continue
		}
		perShard[idx] = chatID
		seedChat(t, stores, ids, user, chatID, now)
		filled++
	}

	mkOb := func(m *model.Message) *store.OutboxRecord {
		return &store.OutboxRecord{
			ID: ids.NextString(), Subject: "message.created", Key: m.ChatID, Data: []byte(`{}`),
		}
	}
	for _, chatID := range perShard {
		m := &model.Message{ID: ids.NextString(), ChatID: chatID, SenderID: user, Text: "e", CreatedAt: now}
		if _, _, err := s.InsertMessage(ctx, m, "", mkOb); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	for i, ps := range stores {
		recs, err := ps.Poll(ctx, 10)
		if err != nil {
			t.Fatalf("poll shard %d: %v", i, err)
		}
		if len(recs) != 1 {
			t.Fatalf("shard %d staged %d events, want exactly its own 1", i, len(recs))
		}
		if recs[0].Key != perShard[i] {
			t.Fatalf("shard %d staged the event for chat %s, want %s", i, recs[0].Key, perShard[i])
		}
	}
}
