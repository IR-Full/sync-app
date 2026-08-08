package fanout

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// captureBus records published events (and lets us inspect shard jobs).
type captureBus struct {
	mu     sync.Mutex
	events []eventbus.Event
}

func (b *captureBus) Publish(_ context.Context, e eventbus.Event) error {
	b.mu.Lock()
	b.events = append(b.events, e)
	b.mu.Unlock()
	return nil
}
func (b *captureBus) Subscribe(string, string, eventbus.Handler) error { return nil }
func (b *captureBus) Close() error                                     { return nil }
func (b *captureBus) bySubject(subj string) []eventbus.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []eventbus.Event
	for _, e := range b.events {
		if e.Subject == subj {
			out = append(out, e)
		}
	}
	return out
}

// countRouter counts NodesFor calls (one per member delivered) and reports every
// user offline, so we measure the fanout reach without real connections.
type countRouter struct{ calls atomic.Int64 }

func (r *countRouter) NodesFor(context.Context, string) ([]string, error) {
	r.calls.Add(1)
	return nil, nil
}
func (r *countRouter) Bind(context.Context, string, string, string) error   { return nil }
func (r *countRouter) Unbind(context.Context, string, string, string) error { return nil }
func (r *countRouter) Refresh(context.Context, string, string) error        { return nil }

type staticChats struct {
	ids   []string
	chats []model.ChatSummary
}

func (c staticChats) MemberIDs(context.Context, string) ([]string, error) { return c.ids, nil }

// UserChats answers the presence audience lookup. Empty unless a test sets it:
// the message-fanout tests must not acquire an accidental presence audience.
func (c staticChats) UserChats(_ context.Context, _ string, after string, limit int) ([]model.ChatSummary, error) {
	out := make([]model.ChatSummary, 0, limit)
	for _, s := range c.chats {
		if s.Chat == nil || s.Chat.ID <= after {
			continue
		}
		out = append(out, s)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// MemberIDsPage serves the ids in sorted order after a cursor, like a real store
// keyset walk — the fanout coordinator depends on that ordering to page.
func (c staticChats) MemberIDsPage(_ context.Context, _ string, after string, limit int) ([]string, error) {
	sorted := append([]string(nil), c.ids...)
	sort.Strings(sorted)
	out := make([]string, 0, limit)
	for _, id := range sorted {
		if id <= after {
			continue
		}
		out = append(out, id)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// makeMembers builds n ids that sort in a stable, non-ambiguous order (fixed
// width), so the keyset walk above behaves like the numeric one in Postgres.
func makeMembers(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("u%06d", i)
	}
	return ids
}

func TestFanoutShardsHotChat(t *testing.T) {
	const n = 2300 // > threshold → sharded; expect ceil(2300/500)=5 jobs
	bus := &captureBus{}
	rtr := &countRouter{}
	s := New(bus, staticChats{ids: makeMembers(n)}, rtr, slog.Default())

	body := wire.NewMessageBody{ChatID: "hot", MessageID: "m1", SenderID: "u0", Text: "hi"}
	evt := eventbus.Event{Subject: eventbus.SubjMessageCreated, Data: wire.Marshal(body)}
	if err := s.onMessage(context.Background(), evt); err != nil {
		t.Fatal(err)
	}

	// onMessage must NOT deliver inline for a hot chat — it publishes shard jobs.
	if got := rtr.calls.Load(); got != 0 {
		t.Fatalf("hot chat delivered inline (%d) instead of sharding", got)
	}
	jobs := bus.bySubject(subjFanoutShard)
	wantJobs := (n + fanoutShardSize - 1) / fanoutShardSize
	if len(jobs) != wantJobs {
		t.Fatalf("got %d shard jobs, want %d", len(jobs), wantJobs)
	}

	// Every member is covered exactly once across the chunks.
	seen := map[string]bool{}
	for _, j := range jobs {
		var sj wire.FanoutShardBody
		if err := wire.Unmarshal(j.Data, &sj); err != nil {
			t.Fatal(err)
		}
		if len(sj.Members) > fanoutShardSize {
			t.Fatalf("chunk too big: %d", len(sj.Members))
		}
		for _, m := range sj.Members {
			if seen[m] {
				t.Fatalf("member %s in more than one chunk", m)
			}
			seen[m] = true
		}
	}
	if len(seen) != n {
		t.Fatalf("chunks cover %d members, want %d", len(seen), n)
	}

	// Processing the chunks (as competing workers would) delivers every member.
	for _, j := range jobs {
		if err := s.onShard(context.Background(), j); err != nil {
			t.Fatal(err)
		}
	}
	if got := rtr.calls.Load(); got != int64(n) {
		t.Fatalf("delivered %d members across shards, want %d", got, n)
	}
}

func TestFanoutSmallChatInline(t *testing.T) {
	bus := &captureBus{}
	rtr := &countRouter{}
	s := New(bus, staticChats{ids: makeMembers(10)}, rtr, slog.Default())
	body := wire.NewMessageBody{ChatID: "small", MessageID: "m1", SenderID: "u0"}
	if err := s.onMessage(context.Background(), eventbus.Event{Data: wire.Marshal(body)}); err != nil {
		t.Fatal(err)
	}
	if len(bus.bySubject(subjFanoutShard)) != 0 {
		t.Fatal("small chat should not publish shard jobs")
	}
	if got := rtr.calls.Load(); got != 10 {
		t.Fatalf("small chat delivered %d inline, want 10", got)
	}
}

// TestMemberCacheIsBounded pins the two ways the cache is kept from growing
// forever: expired entries are collected, and the ceiling holds even when
// nothing has expired. Without both, a worker keeps one member list per chat it
// has ever delivered to — the leak is proportional to the traffic it served.
func TestMemberCacheIsBounded(t *testing.T) {
	s := New(&captureBus{}, staticChats{ids: makeMembers(2)}, &countRouter{},
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	s.cache["expired"] = memberEntry{ids: []string{"u1"}, expires: time.Now().Add(-time.Minute)}
	s.cache["live"] = memberEntry{ids: []string{"u2"}, expires: time.Now().Add(time.Minute)}
	s.lastSweep = time.Now().Add(-2 * memberSweepEvery)

	s.mu.Lock()
	s.sweepLocked()
	s.mu.Unlock()

	if _, ok := s.cache["expired"]; ok {
		t.Fatal("expired entry survived the sweep")
	}
	if _, ok := s.cache["live"]; !ok {
		t.Fatal("live entry evicted by the sweep")
	}

	// Nothing below has expired, so only the ceiling can bound this.
	for i := 0; i < memberCacheMax+100; i++ {
		s.cache[fmt.Sprintf("chat%d", i)] = memberEntry{expires: time.Now().Add(time.Minute)}
	}
	s.mu.Lock()
	s.sweepLocked()
	s.mu.Unlock()
	if len(s.cache) >= memberCacheMax {
		t.Fatalf("cache stayed above its ceiling: %d entries", len(s.cache))
	}
}

// TestHotChatIsNeverMaterialized pins the property that makes a million-member
// channel affordable: the coordinator holds one PAGE at a time, never the whole
// membership. A chat store that refuses to hand out more than a page proves it —
// the old implementation asked for every id up front and would fail here.
func TestHotChatIsNeverMaterialized(t *testing.T) {
	const n = 3000
	chats := &pagingOnlyChats{ids: makeMembers(n)}
	bus := &captureBus{}
	s := New(bus, chats, &countRouter{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	body := wire.NewMessageBody{ChatID: "hot", MessageID: "m1", SenderID: "u000000", Text: "hi"}
	if err := s.onMessage(context.Background(), eventbus.Event{
		Subject: eventbus.SubjMessageCreated, Data: wire.Marshal(body),
	}); err != nil {
		t.Fatal(err)
	}

	if chats.fullCalls > 0 {
		t.Fatalf("coordinator loaded the whole membership %d time(s)", chats.fullCalls)
	}
	if chats.maxPage > fanoutShardThreshold+1 {
		t.Fatalf("largest page requested was %d — bigger than the sharding decision needs", chats.maxPage)
	}
	seen := map[string]bool{}
	for _, j := range bus.bySubject(subjFanoutShard) {
		var sj wire.FanoutShardBody
		if err := wire.Unmarshal(j.Data, &sj); err != nil {
			t.Fatal(err)
		}
		if sj.Body.MessageID != body.MessageID {
			t.Fatalf("shard job lost the message: %+v", sj.Body)
		}
		for _, m := range sj.Members {
			seen[m] = true
		}
	}
	if len(seen) != n {
		t.Fatalf("shard jobs cover %d of %d members", len(seen), n)
	}
}

// pagingOnlyChats refuses the "give me everyone" call outright.
type pagingOnlyChats struct {
	ids       []string
	fullCalls int
	maxPage   int
	pageCalls int
}

func (c *pagingOnlyChats) MemberIDs(context.Context, string) ([]string, error) {
	c.fullCalls++
	return nil, fmt.Errorf("membership must be paged, not materialized")
}

func (c *pagingOnlyChats) UserChats(context.Context, string, string, int) ([]model.ChatSummary, error) {
	return nil, nil
}

func (c *pagingOnlyChats) MemberIDsPage(_ context.Context, _ string, after string, limit int) ([]string, error) {
	c.pageCalls++
	if limit > c.maxPage {
		c.maxPage = limit
	}
	sorted := append([]string(nil), c.ids...)
	sort.Strings(sorted)
	out := make([]string, 0, limit)
	for _, id := range sorted {
		if id <= after {
			continue
		}
		out = append(out, id)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// TestNormalChatCostsOneLookupPerTTL guards the reason the member cache exists.
// Deciding whether a chat is hot by reading a page on every message would put
// back, per message and for every ordinary chat in the system, exactly the
// lookup this cache removes — to answer a question only huge chats ever answer
// differently.
func TestNormalChatCostsOneLookupPerTTL(t *testing.T) {
	chats := &pagingOnlyChats{ids: makeMembers(3)}
	s := New(&captureBus{}, chats, &countRouter{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	for i := 0; i < 10; i++ {
		body := wire.NewMessageBody{ChatID: "small", MessageID: fmt.Sprintf("m%d", i), SenderID: "u000000"}
		if err := s.onMessage(context.Background(), eventbus.Event{
			Subject: eventbus.SubjMessageCreated, Data: wire.Marshal(body),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if chats.pageCalls != 1 {
		t.Fatalf("membership was read %d times for 10 messages; the cache is bypassed", chats.pageCalls)
	}

	// Secondary events (read receipts, typing, reactions) share the same cache.
	if err := s.onRead(context.Background(), eventbus.Event{
		Subject: eventbus.SubjMessageRead,
		Data:    wire.Marshal(wire.ReadUpdateBody{ChatID: "small", UserID: "u000001", UpToChatSeq: 3}),
	}); err != nil {
		t.Fatal(err)
	}
	if chats.pageCalls != 1 {
		t.Fatalf("a read receipt re-read membership (%d lookups total)", chats.pageCalls)
	}
}

// TestHotChatSecondaryEventsStream pins that the non-message paths reach the same
// audience without materializing it: a pin in a huge channel must still arrive.
func TestHotChatSecondaryEventsStream(t *testing.T) {
	const n = 2500
	chats := &pagingOnlyChats{ids: makeMembers(n)}
	rtr := &countRouter{}
	s := New(&captureBus{}, chats, rtr, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := s.onPinned(context.Background(), eventbus.Event{
		Subject: eventbus.SubjPinned,
		Data:    wire.Marshal(wire.PinnedBody{ChatID: "hot", Pins: []wire.Pin{{MessageID: "m1", PinnedBy: "u000000"}}}),
	}); err != nil {
		t.Fatal(err)
	}
	if chats.fullCalls > 0 {
		t.Fatalf("a secondary event materialized the membership %d time(s)", chats.fullCalls)
	}
	if got := rtr.calls.Load(); got != int64(n) {
		t.Fatalf("pin reached %d of %d members", got, n)
	}
}

// TestPresenceReachesDirectPeersOnly pins down the audience rule.
//
// Presence flips on every connect and disconnect, so "everyone who shares any
// chat" would turn one flaky mobile link into a membership-sized multiplication of
// frames across every group the user belongs to. The peer of a direct chat is
// where "last seen" is actually shown, and there is exactly one of them.
func TestPresenceReachesDirectPeersOnly(t *testing.T) {
	rtr := &recordRouter{}
	chats := staticChats{chats: []model.ChatSummary{
		{Chat: &model.Chat{ID: "1", Type: model.ChatDirect}, PeerID: "peer-a"},
		{Chat: &model.Chat{ID: "2", Type: model.ChatGroup}, PeerID: ""},
		{Chat: &model.Chat{ID: "3", Type: model.ChatChannel}, PeerID: ""},
		{Chat: &model.Chat{ID: "4", Type: model.ChatDirect}, PeerID: "peer-b"},
		// A duplicate row must not double the fanout.
		{Chat: &model.Chat{ID: "5", Type: model.ChatDirect}, PeerID: "peer-a"},
		// Nor may a self-referencing row make a user watch themselves.
		{Chat: &model.Chat{ID: "6", Type: model.ChatDirect}, PeerID: "u1"},
	}}
	s := New(&captureBus{}, chats, rtr, slog.New(slog.NewTextHandler(io.Discard, nil)))

	body := wire.PresenceBody{UserID: "u1", Online: true, LastSeenMs: 1700000000000}
	if err := s.onPresence(context.Background(), eventbus.Event{
		Subject: eventbus.SubjPresence, Key: "u1", Data: wire.Marshal(body),
	}); err != nil {
		t.Fatal(err)
	}

	got := rtr.seen()
	want := map[string]bool{"peer-a": true, "peer-b": true}
	if len(got) != len(want) {
		t.Fatalf("presence delivered to %v, want exactly %v", got, want)
	}
	for _, uid := range got {
		if !want[uid] {
			t.Fatalf("presence delivered to %q, which shares no direct chat with the user", uid)
		}
	}
}

// TestPresenceSurvivesAnAudienceLookupFailure keeps a Redis blip from turning into
// an endless redelivery loop: presence is ephemeral and the next transition (or the
// TTL behind it) supersedes a lost one, so the event must be acknowledged.
func TestPresenceSurvivesAnAudienceLookupFailure(t *testing.T) {
	s := New(&captureBus{}, failingChats{}, &countRouter{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	err := s.onPresence(context.Background(), eventbus.Event{
		Data: wire.Marshal(wire.PresenceBody{UserID: "u1", Online: false}),
	})
	if err != nil {
		t.Fatalf("onPresence returned %v — the bus would redeliver this forever", err)
	}
}

// recordRouter remembers who was routed to, not just how many times.
type recordRouter struct {
	mu    sync.Mutex
	users []string
}

func (r *recordRouter) NodesFor(_ context.Context, userID string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.users = append(r.users, userID)
	return nil, nil
}
func (r *recordRouter) Bind(context.Context, string, string, string) error   { return nil }
func (r *recordRouter) Unbind(context.Context, string, string, string) error { return nil }
func (r *recordRouter) Refresh(context.Context, string, string) error        { return nil }

func (r *recordRouter) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.users...)
}

// failingChats fails the audience lookup the way a registry outage would.
type failingChats struct{}

func (failingChats) MemberIDs(context.Context, string) ([]string, error) {
	return nil, fmt.Errorf("membership unavailable")
}

func (failingChats) MemberIDsPage(context.Context, string, string, int) ([]string, error) {
	return nil, fmt.Errorf("membership unavailable")
}

func (failingChats) UserChats(context.Context, string, string, int) ([]model.ChatSummary, error) {
	return nil, fmt.Errorf("chat list unavailable")
}
