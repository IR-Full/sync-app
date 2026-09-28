package search

import (
	"context"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
)

// memberChats answers membership from a fixed set of (chat, user) pairs.
type memberChats struct {
	mu      sync.Mutex
	members map[string]bool // "chat|user"
	calls   int             // IsMember probes
	// scopeCalls counts UserChatIDs lookups. The point of the batch scope is that
	// this is ONE per query where IsMember used to be one per candidate document,
	// so the test suite needs to be able to see the difference.
	scopeCalls int
}

func (m *memberChats) IsMember(_ context.Context, chatID, userID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	return m.members[chatID+"|"+userID], nil
}

func (m *memberChats) UserChatIDs(_ context.Context, userID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scopeCalls++
	var out []string
	for pair, ok := range m.members {
		if !ok {
			continue
		}
		if chatID, member, found := strings.Cut(pair, "|"); found && member == userID {
			out = append(out, chatID)
		}
	}
	sort.Strings(out) // deterministic, so a test can assert on order
	return out, nil
}

func newSvc(pairs ...string) (*Service, *memberChats) {
	set := map[string]bool{}
	for _, p := range pairs {
		set[p] = true
	}
	chats := &memberChats{members: set}
	return New(NewMemoryBackend(), chats, slog.New(slog.NewTextHandler(io.Discard, nil))), chats
}

func ctx() context.Context { return context.Background() }

func index(s *Service, id, chatID, senderID string, seq uint64, text string) {
	// CreatedAt tracks seq so ranking (which is by wall clock now, not by the
	// per-chat sequence) is deterministic in tests.
	s.backend.Index(ctx(), Doc{
		MessageID: id, ChatID: chatID, SenderID: senderID,
		Seq: seq, Text: text, CreatedAt: int64(seq),
	})
}

func TestQueryFindsIndexedText(t *testing.T) {
	s, _ := newSvc("c1|alice")
	index(s, "m1", "c1", "bob", 1, "pineapple on pizza")

	hits, err := s.Query(ctx(), "alice", "pineapple", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].MessageID != "m1" {
		t.Fatalf("hits: %+v", hits)
	}
	if hits[0].Text != "pineapple on pizza" || hits[0].ChatID != "c1" {
		t.Fatalf("hit lost fields: %+v", hits[0])
	}
}

// The permission filter is the security property of this service: results are
// filtered by the CALLER's membership, so a hit in a chat they are not in must
// never be returned however well it matches.
func TestQueryFiltersByMembership(t *testing.T) {
	s, _ := newSvc("c1|alice") // alice is in c1 only
	index(s, "m1", "c1", "bob", 1, "secret recipe")
	index(s, "m2", "c2", "bob", 1, "secret recipe")

	hits, err := s.Query(ctx(), "alice", "secret", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want only the one in a chat alice belongs to: %+v", len(hits), hits)
	}
	if hits[0].ChatID != "c1" {
		t.Fatalf("leaked a hit from %s", hits[0].ChatID)
	}
}

func TestQueryReturnsNothingForANonMember(t *testing.T) {
	s, _ := newSvc() // member of nothing
	index(s, "m1", "c1", "bob", 1, "anything")

	hits, err := s.Query(ctx(), "mallory", "anything", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("non-member got %d hits", len(hits))
	}
}

// TestQueryResolvesScopeOnceNotPerHit is the N+1 regression guard.
//
// Membership used to be checked per CANDIDATE DOCUMENT, after the backend had
// already picked a global top page — so a query cost up to a hundred probes, and
// nearly all of them answered "no, that is a stranger's chat". Now the scope is
// resolved once, before the backend sees the query, and IsMember is not consulted
// at all unless the caller named a specific chat.
func TestQueryResolvesScopeOnceNotPerHit(t *testing.T) {
	s, chats := newSvc("c1|alice")
	for i := 0; i < 10; i++ {
		index(s, string(rune('a'+i)), "c1", "bob", uint64(i), "repeated term")
	}

	hits, err := s.Query(ctx(), "alice", "repeated", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 10 {
		t.Fatalf("got %d hits, want 10", len(hits))
	}
	chats.mu.Lock()
	probes, scopes := chats.calls, chats.scopeCalls
	chats.mu.Unlock()
	if scopes != 1 {
		t.Errorf("%d scope lookups, want exactly 1", scopes)
	}
	if probes != 0 {
		t.Errorf("%d per-document membership probes; the scope makes them unnecessary", probes)
	}
}

// TestQueryFindsOwnMessagesAmongManyStrangers is the CORRECTNESS bug, and it is
// the reason the scope had to move into the backend.
//
// The backend used to be asked for the globally best matches and the service
// filtered afterwards. With enough other people writing the same word, the
// caller's own messages were not in that global page — so the search reported
// nothing for messages it was holding. Here one user has a single match buried
// under a hundred from strangers.
func TestQueryFindsOwnMessagesAmongManyStrangers(t *testing.T) {
	s, _ := newSvc("mine|alice")
	// A hundred strangers' messages, all newer than Alice's, all matching.
	for i := 0; i < 100; i++ {
		index(s, "stranger"+string(rune('a'+i%26))+string(rune('a'+i/26)),
			"theirs"+string(rune('a'+i%26)), "bob", uint64(100+i), "standup meeting")
	}
	index(s, "alices-own", "mine", "alice", 1, "standup meeting")

	hits, err := s.Query(ctx(), "alice", "standup", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want exactly Alice's own", len(hits))
	}
	if hits[0].MessageID != "alices-own" {
		t.Fatalf("found %q, want alices-own", hits[0].MessageID)
	}
}

// TestQueryRanksByRecencyNotPerChatSeq pins the ranking fix. Ordering by Seq
// ranked a chat with a million messages above every other chat by construction,
// because Seq counts within a chat and means nothing across them.
func TestQueryRanksByRecencyNotPerChatSeq(t *testing.T) {
	s, _ := newSvc("busy|alice", "quiet|alice")
	// The busy chat's message is OLD but has a huge per-chat sequence.
	s.backend.Index(ctx(), Doc{MessageID: "old-busy", ChatID: "busy", SenderID: "bob",
		Seq: 1_000_000, Text: "deadline", CreatedAt: 1_000})
	// The quiet chat's message is NEW with a tiny sequence.
	s.backend.Index(ctx(), Doc{MessageID: "new-quiet", ChatID: "quiet", SenderID: "bob",
		Seq: 2, Text: "deadline", CreatedAt: 9_000})

	hits, err := s.Query(ctx(), "alice", "deadline", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(hits))
	}
	if hits[0].MessageID != "new-quiet" {
		t.Fatalf("ranked %q first; recency means the newer message wins regardless "+
			"of how many messages its chat has", hits[0].MessageID)
	}
}

// TestQueryInOneChatChecksMembership makes sure the narrowing filter is not
// mistaken for an authorization. A chat id is guessable, so "search in this chat"
// has to verify the caller is in it.
func TestQueryInOneChatChecksMembership(t *testing.T) {
	s, chats := newSvc("mine|alice")
	index(s, "m1", "mine", "bob", 1, "visible")
	index(s, "m2", "theirs", "bob", 2, "visible")

	// Alice's own chat: allowed.
	hits, err := s.QueryFiltered(ctx(), Query{UserID: "alice", Text: "visible", ChatID: "mine", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].MessageID != "m1" {
		t.Fatalf("own chat returned %+v", hits)
	}

	// A chat she is not in: nothing, and the membership check is what refuses it.
	hits, err = s.QueryFiltered(ctx(), Query{UserID: "alice", Text: "visible", ChatID: "theirs", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("a chat the caller is not in returned %d hits", len(hits))
	}
	chats.mu.Lock()
	probes := chats.calls
	chats.mu.Unlock()
	if probes != 2 {
		t.Errorf("%d membership probes for two named-chat queries, want 2", probes)
	}
}

// TestQueryBySenderNarrowsWithinScope checks the sender filter composes with the
// scope rather than replacing it.
func TestQueryBySenderNarrowsWithinScope(t *testing.T) {
	s, _ := newSvc("c1|alice")
	index(s, "from-bob", "c1", "bob", 1, "lunch plans")
	index(s, "from-carol", "c1", "carol", 2, "lunch plans")

	hits, err := s.QueryFiltered(ctx(), Query{UserID: "alice", Text: "lunch", SenderID: "carol", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].MessageID != "from-carol" {
		t.Fatalf("sender filter returned %+v", hits)
	}
}

// TestQueryWithNoScopeReturnsNothing is the fail-closed case. An unscoped query
// is a caller bug, and the safe reading of a bug in a permission filter is that
// nothing is visible — not that everything is.
func TestQueryWithNoScopeReturnsNothing(t *testing.T) {
	s, _ := newSvc()
	index(s, "m1", "c1", "bob", 1, "secret plans")

	hits, err := s.Query(ctx(), "", "secret", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("an unscoped query returned %d hits", len(hits))
	}

	// And a user who is in no chats at all finds nothing rather than everything.
	hits, err = s.Query(ctx(), "nobody", "secret", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("a user in no chats returned %d hits", len(hits))
	}
}

func TestQueryHonoursTheLimit(t *testing.T) {
	s, _ := newSvc("c1|alice")
	for i := 0; i < 30; i++ {
		index(s, string(rune('a'+i)), "c1", "bob", uint64(i), "common word")
	}

	hits, err := s.Query(ctx(), "alice", "common", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 5 {
		t.Fatalf("got %d hits, want the requested 5", len(hits))
	}
}

func TestQueryAppliesADefaultLimit(t *testing.T) {
	s, _ := newSvc("c1|alice")
	for i := 0; i < 50; i++ {
		index(s, string(rune('a'+i)), "c1", "bob", uint64(i), "common word")
	}
	// A zero limit must not mean "everything" — that would let one query pull the
	// whole index.
	hits, err := s.Query(ctx(), "alice", "common", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || len(hits) > 20 {
		t.Fatalf("got %d hits for limit=0, want a bounded default", len(hits))
	}
}

func TestQueryIsCaseInsensitive(t *testing.T) {
	s, _ := newSvc("c1|alice")
	index(s, "m1", "c1", "bob", 1, "Pineapple Pizza")

	hits, _ := s.Query(ctx(), "alice", "PINEAPPLE", 10)
	if len(hits) != 1 {
		t.Fatalf("case-insensitive match failed: %+v", hits)
	}
}

func TestQueryWithNoMatches(t *testing.T) {
	s, _ := newSvc("c1|alice")
	index(s, "m1", "c1", "bob", 1, "hello")

	hits, err := s.Query(ctx(), "alice", "goodbye", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("got %d hits for a term that was never indexed", len(hits))
	}
}

func TestEmptyQueryReturnsNothing(t *testing.T) {
	s, _ := newSvc("c1|alice")
	index(s, "m1", "c1", "bob", 1, "hello")

	hits, err := s.Query(ctx(), "alice", "   ", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("an empty query matched %d documents", len(hits))
	}
}

// --- index maintenance -----------------------------------------------------

func TestReindexReplacesTheOldText(t *testing.T) {
	s, _ := newSvc("c1|alice")
	index(s, "m1", "c1", "bob", 1, "original text")
	index(s, "m1", "c1", "bob", 1, "replacement text")

	// The old term must no longer match, or an edit would leave the pre-edit text
	// searchable — which for a message someone corrected is a real leak.
	if hits, _ := s.Query(ctx(), "alice", "original", 10); len(hits) != 0 {
		t.Fatalf("pre-edit text still searchable: %+v", hits)
	}
	hits, _ := s.Query(ctx(), "alice", "replacement", 10)
	if len(hits) != 1 {
		t.Fatalf("edited text not searchable: %+v", hits)
	}
	if hits[0].Text != "replacement text" {
		t.Fatalf("stored text not updated: %q", hits[0].Text)
	}
}

func TestDeleteRemovesFromTheIndex(t *testing.T) {
	s, _ := newSvc("c1|alice")
	index(s, "m1", "c1", "bob", 1, "deletable")
	s.backend.Delete(ctx(), "m1")

	if hits, _ := s.Query(ctx(), "alice", "deletable", 10); len(hits) != 0 {
		t.Fatalf("deleted message still searchable: %+v", hits)
	}
}

func TestDeleteOfAnUnknownDocumentIsSafe(t *testing.T) {
	s, _ := newSvc("c1|alice")
	s.backend.Delete(ctx(), "never-existed") // must not panic
}

// --- event consumption -----------------------------------------------------

func TestMessageEventsDriveTheIndex(t *testing.T) {
	s, _ := newSvc("c1|alice")
	bus := eventbus.NewMemory()
	if err := s.Start(bus); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// The handlers are exercised directly rather than through the bus: the memory
	// bus dispatches asynchronously, and what is under test here is the handler,
	// not the delivery.
	created := wire.Marshal(wire.NewMessageBody{
		MessageID: "m1", ChatID: "c1", SenderID: "bob", ChatSeq: 1, Text: "indexed by event",
	})
	if err := s.onUpsert(ctx(), eventbus.Event{Subject: eventbus.SubjMessageCreated, Data: created}); err != nil {
		t.Fatalf("onUpsert: %v", err)
	}
	if hits, _ := s.Query(ctx(), "alice", "indexed", 10); len(hits) != 1 {
		t.Fatalf("created event did not index: %+v", hits)
	}

	// An edit arrives on the same handler and replaces the text.
	edited := wire.Marshal(wire.NewMessageBody{
		MessageID: "m1", ChatID: "c1", SenderID: "bob", ChatSeq: 1, Text: "rewritten", Edited: true,
	})
	if err := s.onUpsert(ctx(), eventbus.Event{Subject: eventbus.SubjMessageEdited, Data: edited}); err != nil {
		t.Fatalf("onUpsert(edit): %v", err)
	}
	if hits, _ := s.Query(ctx(), "alice", "indexed", 10); len(hits) != 0 {
		t.Fatal("pre-edit text still searchable after an edit event")
	}

	// A delete tombstone removes it.
	deleted := wire.Marshal(wire.NewMessageBody{MessageID: "m1", ChatID: "c1", Deleted: true})
	if err := s.onDelete(ctx(), eventbus.Event{Subject: eventbus.SubjMessageDeleted, Data: deleted}); err != nil {
		t.Fatalf("onDelete: %v", err)
	}
	if hits, _ := s.Query(ctx(), "alice", "rewritten", 10); len(hits) != 0 {
		t.Fatal("deleted message still searchable")
	}
}

// A created event that already carries the deleted flag must not be indexed —
// otherwise a tombstone replayed through the created subject resurrects text.
func TestUpsertOfADeletedMessageRemovesIt(t *testing.T) {
	s, _ := newSvc("c1|alice")
	index(s, "m1", "c1", "bob", 1, "doomed")

	tombstone := wire.Marshal(wire.NewMessageBody{
		MessageID: "m1", ChatID: "c1", SenderID: "bob", Text: "doomed", Deleted: true,
	})
	if err := s.onUpsert(ctx(), eventbus.Event{Data: tombstone}); err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.Query(ctx(), "alice", "doomed", 10); len(hits) != 0 {
		t.Fatalf("a deleted message was indexed: %+v", hits)
	}
}

func TestHandlersRejectMalformedEvents(t *testing.T) {
	s, _ := newSvc("c1|alice")
	garbage := eventbus.Event{Data: []byte{0xff, 0xfe, 0xfd, 0x00, 0x01}}
	if err := s.onUpsert(ctx(), garbage); err == nil {
		t.Fatal("onUpsert accepted an undecodable event")
	}
	if err := s.onDelete(ctx(), garbage); err == nil {
		t.Fatal("onDelete accepted an undecodable event")
	}
}

func TestStartSubscribesWithoutError(t *testing.T) {
	s, _ := newSvc()
	if err := s.Start(eventbus.NewMemory()); err != nil {
		t.Fatalf("Start: %v", err)
	}
}

func TestBackendIsSafeUnderConcurrency(t *testing.T) {
	s, _ := newSvc("c1|alice")
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := string(rune('a' + i%20))
			index(s, id, "c1", "bob", uint64(i), "concurrent text")
			_, _ = s.Query(ctx(), "alice", "concurrent", 5)
			if i%3 == 0 {
				s.backend.Delete(ctx(), id)
			}
		}(i)
	}
	wg.Wait() // the assertion is the race detector staying quiet
}
