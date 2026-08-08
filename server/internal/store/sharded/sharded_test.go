package sharded

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store"
)

// fakeShard is a minimal store.MessageStore that records which chats it received
// and assigns a per-chat sequence — enough to test routing/co-location without a
// real backend.
type fakeShard struct {
	mu   sync.Mutex
	seq  map[string]uint64           // chatID → last seq
	msgs map[string][]*model.Message // chatID → messages
}

func newFakeShard() *fakeShard {
	return &fakeShard{seq: map[string]uint64{}, msgs: map[string][]*model.Message{}}
}

func (f *fakeShard) InsertMessage(_ context.Context, m *model.Message, _ string, _ store.MakeOutbox) (*model.Message, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq[m.ChatID]++
	cp := *m
	cp.Seq = f.seq[m.ChatID]
	f.msgs[m.ChatID] = append(f.msgs[m.ChatID], &cp)
	return &cp, false, nil
}
func (f *fakeShard) GetMessage(_ context.Context, chatID, id string) (*model.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.msgs[chatID] {
		if m.ID == id {
			return m, nil
		}
	}
	return nil, store.ErrNotFound
}
func (f *fakeShard) EditMessage(_ context.Context, _, _, _ string, _ int64, _ store.MakeOutbox) (*model.Message, error) {
	return nil, nil
}
func (f *fakeShard) DeleteMessage(_ context.Context, _, _ string, _ int64, _ store.MakeOutbox) (*model.Message, error) {
	return nil, nil
}
func (f *fakeShard) History(_ context.Context, chatID string, _ uint64, _ int) ([]*model.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.msgs[chatID], nil
}
func (f *fakeShard) chatCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.msgs)
}

// _ ensures fakeShard satisfies the interface.
var _ store.MessageStore = (*fakeShard)(nil)

func TestShardingCoLocatesAndPreservesOrder(t *testing.T) {
	const nShards, nChats, perChat = 4, 200, 5
	shards := make([]*fakeShard, nShards)
	backends := make([]store.MessageStore, nShards)
	for i := range shards {
		shards[i] = newFakeShard()
		backends[i] = shards[i]
	}
	s := New(backends...)
	ctx := context.Background()

	for c := 0; c < nChats; c++ {
		chatID := fmt.Sprintf("chat-%d", c)
		for k := 0; k < perChat; k++ {
			m := &model.Message{ID: fmt.Sprintf("%s-m%d", chatID, k), ChatID: chatID, SenderID: "u1"}
			got, _, err := s.InsertMessage(ctx, m, "", nil)
			if err != nil {
				t.Fatalf("insert: %v", err)
			}
			// Co-located seq is gap-free and monotonic within the chat.
			if got.Seq != uint64(k+1) {
				t.Fatalf("chat %s msg %d: got seq %d want %d", chatID, k, got.Seq, k+1)
			}
		}
	}

	// Co-location: every chat's messages live on exactly ONE shard.
	seen := 0
	for c := 0; c < nChats; c++ {
		chatID := fmt.Sprintf("chat-%d", c)
		hits := 0
		for _, sh := range shards {
			sh.mu.Lock()
			if len(sh.msgs[chatID]) > 0 {
				hits++
			}
			sh.mu.Unlock()
		}
		if hits != 1 {
			t.Fatalf("chat %s spread across %d shards (want exactly 1)", chatID, hits)
		}
		seen++
	}
	if seen != nChats {
		t.Fatalf("only %d/%d chats accounted for", seen, nChats)
	}

	// Distribution: chats are spread across more than one shard (not degenerate).
	used := 0
	for _, sh := range shards {
		if sh.chatCount() > 0 {
			used++
		}
	}
	if used < 2 {
		t.Fatalf("chats landed on only %d shard(s); hashing is not distributing", used)
	}
	t.Logf("%d chats distributed across %d/%d shards", nChats, used, nShards)
}

func TestShardRoutingIsStable(t *testing.T) {
	// The same chat id must always map to the same shard (reads reach the writer).
	s := New(newFakeShard(), newFakeShard(), newFakeShard())
	for _, id := range []string{"chat-1", "chat-42", "abc", "9999"} {
		first := s.ShardIndex(id)
		for i := 0; i < 100; i++ {
			if s.ShardIndex(id) != first {
				t.Fatalf("ShardIndex(%q) not stable", id)
			}
		}
	}
}

func TestSingleShardDegeneratesToPassthrough(t *testing.T) {
	f := newFakeShard()
	s := New(f)
	ctx := context.Background()
	if _, _, err := s.InsertMessage(ctx, &model.Message{ID: "x", ChatID: "c"}, "", nil); err != nil {
		t.Fatal(err)
	}
	if f.chatCount() != 1 {
		t.Fatal("single-shard store should pass through to the one backend")
	}
}

// expiringShard is a shard that also implements the optional capabilities, so a
// test can tell whether the sharded decorator forwards them.
type expiringShard struct {
	store.MessageStore
	expired  []*model.Message
	refs     map[string]bool
	expCalls int
}

func (e *expiringShard) ExpireMessages(context.Context, int64, int) ([]*model.Message, error) {
	e.expCalls++
	return e.expired, nil
}

func (e *expiringShard) MediaRefExists(_ context.Context, ref string) (bool, error) {
	return e.refs[ref], nil
}

// TestShardedForwardsOptionalCapabilities pins the failure mode that makes
// capability interfaces dangerous: a decorator implementing only the base does
// not lose a method, it makes a FEATURE disappear — silently, on whichever
// deployment happens to use the decorator. Sharding is switched on by an
// environment variable, so "does self-destruct work?" would otherwise have
// depended on how many DSNs an operator listed.
func TestShardedForwardsOptionalCapabilities(t *testing.T) {
	ctx := context.Background()
	a := &expiringShard{
		expired: []*model.Message{{ID: "1", ChatID: "c1"}},
		refs:    map[string]bool{"kept": true},
	}
	b := &expiringShard{
		expired: []*model.Message{{ID: "2", ChatID: "c2"}},
		refs:    map[string]bool{},
	}
	s := New(a, b)

	// The reaper must visit EVERY shard: a deadline belongs to a message, and due
	// messages are spread over all of them.
	got, err := s.ExpireMessages(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || a.expCalls != 1 || b.expCalls != 1 {
		t.Fatalf("reaper reached %d messages across %d/%d shards", len(got), a.expCalls, b.expCalls)
	}

	// A blob referenced from ANY shard is still referenced — a forward can land on
	// a different shard than the original.
	used, err := s.MediaRefExists(ctx, "kept")
	if err != nil || !used {
		t.Fatalf("a referenced blob was reported unreachable: used=%v err=%v", used, err)
	}
	used, err = s.MediaRefExists(ctx, "orphan")
	if err != nil || used {
		t.Fatalf("an unreferenced blob was reported as kept: used=%v err=%v", used, err)
	}
}
