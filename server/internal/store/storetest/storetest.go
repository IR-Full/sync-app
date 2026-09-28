// Package storetest is the conformance suite for the store contracts.
//
// The interfaces in `internal/store` exist so the message log can move to a
// wide-column store and the metadata can stay in Postgres (ARCHITECTURE.md §8),
// and that promise is only real if every backend behaves the SAME. A test
// written against one implementation proves nothing about the other, and the
// differences that matter are exactly the ones a type checker cannot see:
// whether a duplicate insert consumes a sequence, whether a block survives a
// re-add, whether a capped invite can be over-redeemed.
//
// So the behaviour lives here once, and each backend runs it:
//
//	func TestConformance(t *testing.T) {
//	    storetest.Run(t, func(t *testing.T) store.Stores { ... })
//	}
//
// Every id is a NUMERIC string. The Postgres backend stores ids as bigint and
// parses them on the way in, so a suite using "alice" would pass on memory and
// fail on the backend it is meant to certify.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
)

// NewStores builds a fresh, empty set of stores for one subtest.
type NewStores func(t *testing.T) store.Stores

// idCounter hands out unique numeric ids.
//
// Seeded from the clock rather than starting at zero, because a Postgres run
// keeps its data: a counter that restarts each process would hand the second run
// the ids the first one already inserted, and the suite would fail with a
// conflict that says nothing about the code under test. The seed is shifted into
// a high range so these ids cannot collide with a snowflake from pkg/id either.
var idCounter atomic.Int64

func init() {
	idCounter.Store(time.Now().UnixMicro() % 1_000_000_000_000)
}

func nextID() string {
	return fmt.Sprintf("%d", idCounter.Add(1)+8_000_000_000_000_000)
}

// Run executes the whole conformance suite against one backend.
func Run(t *testing.T, newStores NewStores) {
	t.Helper()
	for _, tc := range []struct {
		name string
		fn   func(*testing.T, store.Stores)
	}{
		{"Users", testUsers},
		{"Devices", testDevices},
		{"Sessions", testSessions},
		{"Chats", testChats},
		{"Membership", testMembership},
		{"MemberPaging", testMemberPaging},
		{"Sequence", testSequence},
		{"Messages", testMessages},
		{"Idempotency", testIdempotency},
		{"History", testHistory},
		{"EditDelete", testEditDelete},
		{"Outbox", testOutbox},
		{"ReadState", testReadState},
		{"Reactions", testReactions},
		{"Contacts", testContacts},
		{"Drafts", testDrafts},
		{"Pins", testPins},
		{"Scheduled", testScheduled},
		{"Invites", testInvites},
		{"SecretQueue", testSecretQueue},
		{"ChatList", testChatList},
		{"ChatUsername", testChatUsername},
		{"Polls", testPolls},
		{"Calls", testCalls},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.fn(t, newStores(t))
		})
	}
}

// --- helpers ---------------------------------------------------------------

func ctx() context.Context { return context.Background() }

// mkUser creates a user and returns its id.
func mkUser(t *testing.T, s store.Stores) string {
	t.Helper()
	id := nextID()
	u := &model.User{
		ID: id, Username: "u" + id, DisplayName: "User " + id,
		PasswordHash: "argon2id$x$y", CreatedAt: 1000,
	}
	if err := s.Users.CreateUser(ctx(), u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return id
}

// mkChat creates a group chat owned by ownerID, with ownerID as a member.
func mkChat(t *testing.T, s store.Stores, ownerID string) string {
	t.Helper()
	id := nextID()
	c := &model.Chat{ID: id, Type: model.ChatGroup, Title: "chat " + id, OwnerID: ownerID, CreatedAt: 1000}
	if err := s.Chats.CreateChat(ctx(), c); err != nil {
		t.Fatalf("CreateChat: %v", err)
	}
	addMember(t, s, id, ownerID, model.RoleOwner)
	return id
}

func addMember(t *testing.T, s store.Stores, chatID, userID string, role model.MemberRole) {
	t.Helper()
	m := &model.ChatMember{ChatID: chatID, UserID: userID, Role: role, JoinedAt: 1000}
	if err := s.Chats.AddMember(ctx(), m); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
}

// send inserts a message. Note it does NOT call BumpSeq: InsertMessage allocates
// the sequence itself, inside its own transaction (see the contract note on
// store.MessageStore). Bumping first would burn a sequence per message.
func send(t *testing.T, s store.Stores, chatID, senderID, text string) *model.Message {
	t.Helper()
	m := &model.Message{
		ID: nextID(), ChatID: chatID, SenderID: senderID,
		Text: text, CreatedAt: 1000,
	}
	stored, dup, err := s.Messages.InsertMessage(ctx(), m, nextID(), nil)
	if err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if dup {
		t.Fatal("fresh message reported as duplicate")
	}
	return stored
}

// --- users -----------------------------------------------------------------

func testUsers(t *testing.T, s store.Stores) {
	id := mkUser(t, s)

	got, err := s.Users.GetUser(ctx(), id)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.ID != id || got.PasswordHash == "" {
		t.Fatalf("round trip lost fields: %+v", got)
	}

	byName, err := s.Users.GetUserByUsername(ctx(), got.Username)
	if err != nil || byName.ID != id {
		t.Fatalf("GetUserByUsername: %v / %+v", err, byName)
	}

	if _, err := s.Users.GetUser(ctx(), nextID()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing user: got %v, want ErrNotFound", err)
	}
	if _, err := s.Users.GetUserByUsername(ctx(), "nobody-at-all"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing username: got %v, want ErrNotFound", err)
	}

	// A taken username must conflict, not silently create a second account.
	dup := &model.User{ID: nextID(), Username: got.Username, PasswordHash: "h", CreatedAt: 1}
	if err := s.Users.CreateUser(ctx(), dup); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate username: got %v, want ErrConflict", err)
	}

	// UpdateProfile takes final values: an empty string CLEARS rather than means
	// "unchanged", which is the only way a user can remove their avatar.
	if err := s.Users.UpdateProfile(ctx(), id, "New Name", "media-ref-1"); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	got, _ = s.Users.GetUser(ctx(), id)
	if got.DisplayName != "New Name" || got.AvatarRef != "media-ref-1" {
		t.Fatalf("profile not written: %+v", got)
	}
	if err := s.Users.UpdateProfile(ctx(), id, "New Name", ""); err != nil {
		t.Fatalf("UpdateProfile clear: %v", err)
	}
	got, _ = s.Users.GetUser(ctx(), id)
	if got.AvatarRef != "" {
		t.Fatalf("avatar not cleared: %q", got.AvatarRef)
	}
}

// --- devices ---------------------------------------------------------------

func testDevices(t *testing.T, s store.Stores) {
	owner := mkUser(t, s)
	devID := nextID()

	d := &model.Device{ID: devID, UserID: owner, Platform: "ios", CreatedAt: 1, LastSeen: 1}
	if err := s.Users.UpsertDevice(ctx(), d); err != nil {
		t.Fatalf("UpsertDevice: %v", err)
	}
	// Upsert is idempotent for the same owner.
	if err := s.Users.UpsertDevice(ctx(), d); err != nil {
		t.Fatalf("UpsertDevice repeat: %v", err)
	}

	got, err := s.Users.GetDevice(ctx(), devID)
	if err != nil || got.UserID != owner {
		t.Fatalf("GetDevice: %v / %+v", err, got)
	}

	list, err := s.Users.ListDevices(ctx(), owner)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListDevices: %v / %d rows", err, len(list))
	}

	// A device id is client-asserted, so a SECOND account claiming it must not be
	// able to take the row over — that would repoint the first account's pushes.
	intruder := mkUser(t, s)
	stolen := &model.Device{ID: devID, UserID: intruder, Platform: "android", CreatedAt: 2, LastSeen: 2}
	if err := s.Users.UpsertDevice(ctx(), stolen); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("device takeover: got %v, want ErrConflict", err)
	}
	got, _ = s.Users.GetDevice(ctx(), devID)
	if got.UserID != owner {
		t.Fatalf("device owner changed to %s", got.UserID)
	}

	// Push tokens are scoped by owner for the same reason.
	if err := s.Users.SetPushToken(ctx(), owner, devID, "tok-1"); err != nil {
		t.Fatalf("SetPushToken: %v", err)
	}
	got, _ = s.Users.GetDevice(ctx(), devID)
	if got.PushToken != "tok-1" {
		t.Fatalf("push token not stored: %q", got.PushToken)
	}
	// Wrong owner must not write.
	_ = s.Users.SetPushToken(ctx(), intruder, devID, "attacker-token")
	got, _ = s.Users.GetDevice(ctx(), devID)
	if got.PushToken == "attacker-token" {
		t.Fatal("another account rewrote this device's push token")
	}
	// Empty clears (that is how "notifications off" stops the push at the source).
	if err := s.Users.SetPushToken(ctx(), owner, devID, ""); err != nil {
		t.Fatalf("clear push token: %v", err)
	}
	got, _ = s.Users.GetDevice(ctx(), devID)
	if got.PushToken != "" {
		t.Fatalf("push token not cleared: %q", got.PushToken)
	}
}

// --- sessions --------------------------------------------------------------

func testSessions(t *testing.T, s store.Stores) {
	user := mkUser(t, s)
	devID := nextID()
	if err := s.Users.UpsertDevice(ctx(), &model.Device{ID: devID, UserID: user, Platform: "cli", CreatedAt: 1, LastSeen: 1}); err != nil {
		t.Fatalf("device: %v", err)
	}

	sess := &model.Session{
		ID: nextID(), UserID: user, DeviceID: devID,
		Token: "hash-" + nextID(), ResumeToken: "resume-" + nextID(),
		CreatedAt: 1000, ExpiresAt: 9_000_000_000_000,
	}
	if err := s.Sessions.CreateSession(ctx(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	byTok, err := s.Sessions.GetSessionByToken(ctx(), sess.Token)
	if err != nil || byTok.ID != sess.ID {
		t.Fatalf("GetSessionByToken: %v / %+v", err, byTok)
	}
	byResume, err := s.Sessions.GetSessionByResumeToken(ctx(), sess.ResumeToken)
	if err != nil || byResume.ID != sess.ID {
		t.Fatalf("GetSessionByResumeToken: %v / %+v", err, byResume)
	}
	// The two tokens are separate namespaces.
	if _, err := s.Sessions.GetSessionByToken(ctx(), sess.ResumeToken); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("resume token accepted as bearer: %v", err)
	}

	list, err := s.Sessions.ListSessions(ctx(), user)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListSessions: %v / %d", err, len(list))
	}

	// TouchSession is what makes the session TTL a rolling window: using a session
	// pushes its expiry out, so one in daily use never expires while one that goes
	// quiet for the full TTL does.
	later := sess.ExpiresAt + 60_000
	if err := s.Sessions.TouchSession(ctx(), sess.ID, later); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}
	byTok, err = s.Sessions.GetSessionByToken(ctx(), sess.Token)
	if err != nil || byTok.ExpiresAt != later {
		t.Fatalf("TouchSession did not extend the expiry: %v / %d want %d", err, byTok.ExpiresAt, later)
	}

	// Never backwards. Two connections of one account can touch concurrently, and
	// the older of two in-flight values must not win and shorten the session.
	if err := s.Sessions.TouchSession(ctx(), sess.ID, later-30_000); err != nil {
		t.Fatalf("TouchSession (earlier): %v", err)
	}
	byTok, _ = s.Sessions.GetSessionByToken(ctx(), sess.Token)
	if byTok.ExpiresAt != later {
		t.Fatalf("TouchSession moved the expiry backwards to %d", byTok.ExpiresAt)
	}

	// Revocation is why this system uses opaque tokens instead of JWTs, so the
	// flag has to actually land on the stored row.
	if err := s.Sessions.RevokeSession(ctx(), sess.ID, 5000); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	byTok, err = s.Sessions.GetSessionByToken(ctx(), sess.Token)
	if err != nil {
		t.Fatalf("GetSessionByToken after revoke: %v", err)
	}
	if byTok.RevokedAt == 0 {
		t.Fatal("revoked_at not recorded — the session still reads as live")
	}

	// And a revoked session must stay revoked. Extending one would turn "log out"
	// into "log out until the device reconnects", which is not a logout — this is
	// the case the WHERE clause in the Postgres implementation exists for.
	revokedExpiry := byTok.ExpiresAt
	if err := s.Sessions.TouchSession(ctx(), sess.ID, revokedExpiry+120_000); err != nil {
		t.Fatalf("TouchSession on a revoked session should be a no-op, got: %v", err)
	}
	byTok, _ = s.Sessions.GetSessionByToken(ctx(), sess.Token)
	if byTok.ExpiresAt != revokedExpiry {
		t.Fatal("TouchSession revived a revoked session")
	}
}

// --- chats -----------------------------------------------------------------

func testChats(t *testing.T, s store.Stores) {
	owner := mkUser(t, s)
	peer := mkUser(t, s)

	chatID := mkChat(t, s, owner)
	got, err := s.Chats.GetChat(ctx(), chatID)
	if err != nil || got.OwnerID != owner || got.Type != model.ChatGroup {
		t.Fatalf("GetChat: %v / %+v", err, got)
	}
	if _, err := s.Chats.GetChat(ctx(), nextID()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing chat: got %v, want ErrNotFound", err)
	}

	// A direct chat is canonical per unordered pair: asking twice — in either
	// order — must return the same chat, or two people end up in two different
	// "1:1" conversations with each other.
	d1, err := s.Chats.GetOrCreateDirect(ctx(), owner, peer, nextID())
	if err != nil {
		t.Fatalf("GetOrCreateDirect: %v", err)
	}
	d2, err := s.Chats.GetOrCreateDirect(ctx(), peer, owner, nextID())
	if err != nil {
		t.Fatalf("GetOrCreateDirect reversed: %v", err)
	}
	if d1.ID != d2.ID {
		t.Fatalf("direct chat not canonical: %s vs %s", d1.ID, d2.ID)
	}
	if d1.Type != model.ChatDirect {
		t.Fatalf("direct chat type = %q", d1.Type)
	}
	// Both parties are members of it.
	for _, u := range []string{owner, peer} {
		ok, err := s.Chats.IsMember(ctx(), d1.ID, u)
		if err != nil || !ok {
			t.Fatalf("direct chat missing member %s: %v", u, err)
		}
	}

	// GetDirect never creates — that distinction is what lets only NEW chat
	// creation be rate-limited.
	found, err := s.Chats.GetDirect(ctx(), owner, peer)
	if err != nil || found.ID != d1.ID {
		t.Fatalf("GetDirect: %v / %+v", err, found)
	}
	stranger := mkUser(t, s)
	if _, err := s.Chats.GetDirect(ctx(), owner, stranger); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetDirect for a pair with no chat: got %v, want ErrNotFound", err)
	}
}

// --- membership ------------------------------------------------------------

func testMembership(t *testing.T, s store.Stores) {
	owner := mkUser(t, s)
	member := mkUser(t, s)
	outsider := mkUser(t, s)
	chatID := mkChat(t, s, owner)
	addMember(t, s, chatID, member, model.RoleMember)

	ok, err := s.Chats.IsMember(ctx(), chatID, member)
	if err != nil || !ok {
		t.Fatalf("IsMember(member): %v / %v", err, ok)
	}
	ok, err = s.Chats.IsMember(ctx(), chatID, outsider)
	if err != nil || ok {
		t.Fatalf("IsMember(outsider): %v / %v", err, ok)
	}

	got, err := s.Chats.GetMember(ctx(), chatID, owner)
	if err != nil || got.Role != model.RoleOwner {
		t.Fatalf("GetMember(owner): %v / %+v", err, got)
	}
	if _, err := s.Chats.GetMember(ctx(), chatID, outsider); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetMember(outsider): got %v, want ErrNotFound", err)
	}

	members, err := s.Chats.ListMembers(ctx(), chatID)
	if err != nil || len(members) != 2 {
		t.Fatalf("ListMembers: %v / %d", err, len(members))
	}

	n, err := s.Chats.CountMembersWithRole(ctx(), chatID, model.RoleOwner)
	if err != nil || n != 1 {
		t.Fatalf("CountMembersWithRole(owner): %v / %d", err, n)
	}

	chats, err := s.Chats.ListUserChats(ctx(), member)
	if err != nil || len(chats) != 1 || chats[0] != chatID {
		t.Fatalf("ListUserChats: %v / %v", err, chats)
	}

	// Role changes are optional capability; exercise it when present.
	if roles, okCast := s.Chats.(store.MemberRoleStore); okCast {
		if err := roles.SetMemberRole(ctx(), chatID, member, model.RoleAdmin); err != nil {
			t.Fatalf("SetMemberRole: %v", err)
		}
		got, _ = s.Chats.GetMember(ctx(), chatID, member)
		if got.Role != model.RoleAdmin {
			t.Fatalf("role not updated: %q", got.Role)
		}
	}

	if err := s.Chats.RemoveMember(ctx(), chatID, member); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	ok, _ = s.Chats.IsMember(ctx(), chatID, member)
	if ok {
		t.Fatal("member still present after removal")
	}
}

// Keyset paging is the only way to walk a channel with millions of members, so
// the page boundary has to be exact: no gaps, no repeats, and the cursor is the
// last id rather than an offset.
func testMemberPaging(t *testing.T, s store.Stores) {
	owner := mkUser(t, s)
	chatID := mkChat(t, s, owner)

	want := map[string]bool{owner: true}
	for i := 0; i < 6; i++ {
		u := mkUser(t, s)
		addMember(t, s, chatID, u, model.RoleMember)
		want[u] = true
	}

	seen := map[string]bool{}
	after := ""
	for pages := 0; pages < 20; pages++ {
		page, err := s.Chats.ListMemberIDsPage(ctx(), chatID, after, 3)
		if err != nil {
			t.Fatalf("ListMemberIDsPage: %v", err)
		}
		if len(page) == 0 {
			break
		}
		if len(page) > 3 {
			t.Fatalf("page over the limit: %d", len(page))
		}
		for _, id := range page {
			if seen[id] {
				t.Fatalf("member %s returned twice across pages", id)
			}
			seen[id] = true
		}
		after = page[len(page)-1]
	}
	if len(seen) != len(want) {
		t.Fatalf("walked %d members, want %d", len(seen), len(want))
	}
	for id := range want {
		if !seen[id] {
			t.Fatalf("member %s never appeared in any page", id)
		}
	}

	// The same walk with roles attached.
	withRoles, err := s.Chats.ListMembersPage(ctx(), chatID, "", 100)
	if err != nil || len(withRoles) != len(want) {
		t.Fatalf("ListMembersPage: %v / %d rows", err, len(withRoles))
	}
	for _, m := range withRoles {
		if m.Role == "" {
			t.Fatalf("member %s came back with no role", m.UserID)
		}
	}
}

// --- sequence --------------------------------------------------------------

// The per-chat sequence is the client-visible ordering, and the guarantee is
// that it is gap-free and strictly increasing even under concurrency.
func testSequence(t *testing.T, s store.Stores) {
	owner := mkUser(t, s)
	chatID := mkChat(t, s, owner)

	first, err := s.Chats.BumpSeq(ctx(), chatID)
	if err != nil {
		t.Fatalf("BumpSeq: %v", err)
	}
	second, _ := s.Chats.BumpSeq(ctx(), chatID)
	if second != first+1 {
		t.Fatalf("sequence not contiguous: %d then %d", first, second)
	}

	const workers = 16
	var wg sync.WaitGroup
	got := make([]uint64, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			n, err := s.Chats.BumpSeq(ctx(), chatID)
			if err != nil {
				t.Errorf("concurrent BumpSeq: %v", err)
				return
			}
			got[i] = n
		}(i)
	}
	wg.Wait()

	seen := map[uint64]bool{}
	for _, n := range got {
		if n == 0 {
			continue // an error already reported above
		}
		if seen[n] {
			t.Fatalf("sequence %d handed out twice — ordering is not unique", n)
		}
		seen[n] = true
	}
	if len(seen) != workers {
		t.Fatalf("got %d distinct sequences from %d concurrent bumps", len(seen), workers)
	}
}

// --- messages --------------------------------------------------------------

func testMessages(t *testing.T, s store.Stores) {
	owner := mkUser(t, s)
	chatID := mkChat(t, s, owner)

	stored := send(t, s, chatID, owner, "hello")
	if stored.Seq == 0 {
		t.Fatal("stored message has no sequence")
	}

	got, err := s.Messages.GetMessage(ctx(), chatID, stored.ID)
	if err != nil || got.Text != "hello" {
		t.Fatalf("GetMessage: %v / %+v", err, got)
	}
	if _, err := s.Messages.GetMessage(ctx(), chatID, nextID()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing message: got %v, want ErrNotFound", err)
	}
}

// A retried send must resolve to the stored message AND consume no sequence:
// the bump and the insert share one transaction precisely so a dedup rollback
// cannot leave a permanent hole in a gap-free ordering.
func testIdempotency(t *testing.T, s store.Stores) {
	owner := mkUser(t, s)
	chatID := mkChat(t, s, owner)
	dedup := nextID()

	m1 := &model.Message{ID: nextID(), ChatID: chatID, SenderID: owner, Text: "once", CreatedAt: 1}
	first, dup, err := s.Messages.InsertMessage(ctx(), m1, dedup, nil)
	if err != nil || dup {
		t.Fatalf("first insert: %v dup=%v", err, dup)
	}

	// The retry arrives with a fresh message id, as a reconnecting client's would —
	// only the dedup key is stable.
	m2 := &model.Message{ID: nextID(), ChatID: chatID, SenderID: owner, Text: "once", CreatedAt: 2}
	second, dup, err := s.Messages.InsertMessage(ctx(), m2, dedup, nil)
	if err != nil {
		t.Fatalf("retry insert: %v", err)
	}
	if !dup {
		t.Fatal("retry with the same dedup key was not reported as a duplicate")
	}
	if second.ID != first.ID {
		t.Fatalf("retry produced a second message: %s vs %s", second.ID, first.ID)
	}
	if second.Seq != first.Seq {
		t.Fatalf("duplicate resolved to a different sequence: %d vs %d", second.Seq, first.Seq)
	}

	// And it consumed NO sequence. The bump and the insert share one transaction
	// precisely so a deduped write rolls the bump back — otherwise every retry
	// would leave a permanent hole in an ordering that promises to be gap-free.
	next := send(t, s, chatID, owner, "after the retry")
	if next.Seq != first.Seq+1 {
		t.Fatalf("retry burned a sequence: first=%d next=%d (expected %d)",
			first.Seq, next.Seq, first.Seq+1)
	}

	// A different sender may reuse the key: idempotency is scoped to (sender, key).
	other := mkUser(t, s)
	addMember(t, s, chatID, other, model.RoleMember)
	m3 := &model.Message{ID: nextID(), ChatID: chatID, SenderID: other, Text: "mine", CreatedAt: 3}
	_, dup, err = s.Messages.InsertMessage(ctx(), m3, dedup, nil)
	if err != nil || dup {
		t.Fatalf("another sender's identical key: %v dup=%v", err, dup)
	}
}

func testHistory(t *testing.T, s store.Stores) {
	owner := mkUser(t, s)
	chatID := mkChat(t, s, owner)

	var sent []*model.Message
	for i := 0; i < 5; i++ {
		sent = append(sent, send(t, s, chatID, owner, fmt.Sprintf("m%d", i)))
	}

	// beforeSeq 0 means "latest", newest first.
	page, err := s.Messages.History(ctx(), chatID, 0, 3)
	if err != nil || len(page) != 3 {
		t.Fatalf("History latest: %v / %d rows", err, len(page))
	}
	if page[0].Seq <= page[1].Seq || page[1].Seq <= page[2].Seq {
		t.Fatalf("history not newest-first: %d %d %d", page[0].Seq, page[1].Seq, page[2].Seq)
	}
	if page[0].Seq != sent[4].Seq {
		t.Fatalf("newest page starts at seq %d, want %d", page[0].Seq, sent[4].Seq)
	}

	// Paging backwards from the oldest seq of the previous page.
	older, err := s.Messages.History(ctx(), chatID, page[2].Seq, 10)
	if err != nil {
		t.Fatalf("History older: %v", err)
	}
	for _, m := range older {
		if m.Seq >= page[2].Seq {
			t.Fatalf("cursor leaked seq %d, cursor was %d", m.Seq, page[2].Seq)
		}
	}
	if len(older) != 2 {
		t.Fatalf("older page has %d rows, want 2", len(older))
	}
}

func testEditDelete(t *testing.T, s store.Stores) {
	owner := mkUser(t, s)
	chatID := mkChat(t, s, owner)
	m := send(t, s, chatID, owner, "before")

	edited, err := s.Messages.EditMessage(ctx(), chatID, m.ID, "after", 2000, nil)
	if err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	if edited.Text != "after" || !edited.Edited {
		t.Fatalf("edit not applied: %+v", edited)
	}
	if edited.Seq != m.Seq {
		t.Fatalf("edit moved the message from seq %d to %d", m.Seq, edited.Seq)
	}

	deleted, err := s.Messages.DeleteMessage(ctx(), chatID, m.ID, 3000, nil)
	if err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if !deleted.Deleted {
		t.Fatalf("delete not applied: %+v", deleted)
	}
	// Tombstone, not removal: the row keeps its position so the sequence stays
	// gap-free, which is what every client orders by.
	got, err := s.Messages.GetMessage(ctx(), chatID, m.ID)
	if err != nil {
		t.Fatalf("deleted message unreadable: %v", err)
	}
	if !got.Deleted || got.Seq != m.Seq {
		t.Fatalf("tombstone lost its position: %+v", got)
	}

	if _, err := s.Messages.EditMessage(ctx(), chatID, nextID(), "x", 1, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("edit of a missing message: got %v, want ErrNotFound", err)
	}
}

// --- outbox ----------------------------------------------------------------

// The outbox is what makes delivery durable: the event is staged in the same
// transaction as the message, so a crash between commit and publish cannot lose
// it. These are the three operations the relay depends on.
func testOutbox(t *testing.T, s store.Stores) {
	if s.Outbox == nil {
		t.Skip("backend has no outbox")
	}
	owner := mkUser(t, s)
	chatID := mkChat(t, s, owner)

	m := &model.Message{ID: nextID(), ChatID: chatID, SenderID: owner, Text: "staged", CreatedAt: 1}
	recID := nextID()
	_, _, err := s.Messages.InsertMessage(ctx(), m, nextID(), func(stored *model.Message) *store.OutboxRecord {
		// Built inside the write transaction, AFTER the row and its sequence exist —
		// that ordering is the whole point, since the event body carries the final
		// seq and a builder run beforehand could only guess it.
		if stored.Seq == 0 {
			t.Errorf("outbox builder ran before the sequence was assigned")
		}
		return &store.OutboxRecord{
			ID: recID, Subject: "message.created", Key: chatID,
			Data:  []byte(`{"hello":"world"}`),
			Trace: map[string]string{"traceparent": "00-abc-def-01"},
		}
	})
	if err != nil {
		t.Fatalf("InsertMessage with outbox: %v", err)
	}

	records, err := s.Outbox.Poll(ctx(), 10)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	var found *store.OutboxRecord
	for i := range records {
		if records[i].ID == recID {
			found = &records[i]
		}
	}
	if found == nil {
		t.Fatal("staged event not returned by Poll — it would never reach the bus")
	}
	if found.Subject != "message.created" || string(found.Data) != `{"hello":"world"}` {
		t.Fatalf("event altered in transit: %+v", found)
	}
	// The trace context is what lets one trace span send → outbox → fanout.
	if found.Trace["traceparent"] != "00-abc-def-01" {
		t.Fatalf("trace context lost: %v", found.Trace)
	}

	if err := s.Outbox.MarkSent(ctx(), []string{recID}); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}
	records, _ = s.Outbox.Poll(ctx(), 10)
	for _, r := range records {
		if r.ID == recID {
			t.Fatal("a sent record was polled again — it would be published twice")
		}
	}

	// Collection: a staged event is a COPY of the message, so a backend that only
	// marks rows sent turns the handoff table into a second permanent message log.
	// How the row goes is the backend's business — the memory store compacts a
	// fully-drained prefix inside MarkSent, Postgres deletes on a retention
	// sweep — so what is asserted is the outcome: the call succeeds and the record
	// is not still sitting there afterwards.
	if _, err := s.Outbox.PurgeSent(ctx(), 9_000_000_000_000, 100); err != nil {
		t.Fatalf("PurgeSent: %v", err)
	}
	remaining, err := s.Outbox.Poll(ctx(), 1000)
	if err != nil {
		t.Fatalf("Poll after purge: %v", err)
	}
	for _, r := range remaining {
		if r.ID == recID {
			t.Fatal("a sent-and-purged record is still pollable")
		}
	}
}

// --- read state ------------------------------------------------------------

func testReadState(t *testing.T, s store.Stores) {
	owner := mkUser(t, s)
	chatID := mkChat(t, s, owner)

	if err := s.Reads.SetRead(ctx(), &model.ReadState{ChatID: chatID, UserID: owner, UpToSeq: 5, UpdatedAt: 10}); err != nil {
		t.Fatalf("SetRead: %v", err)
	}
	got, err := s.Reads.GetRead(ctx(), chatID, owner)
	if err != nil || got.UpToSeq != 5 {
		t.Fatalf("GetRead: %v / %+v", err, got)
	}

	// A read cursor only ever moves forward. Receipts arrive out of order across
	// devices, and a cursor that could go backwards would resurrect unread badges
	// the user already cleared.
	if err := s.Reads.SetRead(ctx(), &model.ReadState{ChatID: chatID, UserID: owner, UpToSeq: 3, UpdatedAt: 20}); err != nil {
		t.Fatalf("SetRead backwards: %v", err)
	}
	got, _ = s.Reads.GetRead(ctx(), chatID, owner)
	if got.UpToSeq != 5 {
		t.Fatalf("read cursor moved backwards to %d", got.UpToSeq)
	}

	if err := s.Reads.SetRead(ctx(), &model.ReadState{ChatID: chatID, UserID: owner, UpToSeq: 9, UpdatedAt: 30}); err != nil {
		t.Fatalf("SetRead forward: %v", err)
	}
	got, _ = s.Reads.GetRead(ctx(), chatID, owner)
	if got.UpToSeq != 9 {
		t.Fatalf("read cursor did not advance: %d", got.UpToSeq)
	}

	// An unset cursor reads as zero rather than an error: a chat nobody has read
	// is the normal case, not a missing row to handle.
	fresh := mkUser(t, s)
	got, err = s.Reads.GetRead(ctx(), chatID, fresh)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetRead for a never-read chat: %v", err)
	}
	if err == nil && got.UpToSeq != 0 {
		t.Fatalf("never-read cursor = %d, want 0", got.UpToSeq)
	}
}

// --- reactions -------------------------------------------------------------

func testReactions(t *testing.T, s store.Stores) {
	if s.Reactions == nil {
		t.Skip("backend has no reactions")
	}
	owner := mkUser(t, s)
	chatID := mkChat(t, s, owner)
	m := send(t, s, chatID, owner, "react to me")

	r := &model.Reaction{ChatID: chatID, MessageID: m.ID, UserID: owner, Emoji: "👍", CreatedAt: 1}
	added, err := s.Reactions.SetReaction(ctx(), r)
	if err != nil || !added {
		t.Fatalf("first reaction: %v added=%v", err, added)
	}

	// Same emoji again toggles it OFF.
	added, err = s.Reactions.SetReaction(ctx(), r)
	if err != nil || added {
		t.Fatalf("repeat reaction should toggle off: %v added=%v", err, added)
	}
	list, _ := s.Reactions.ListReactions(ctx(), chatID, m.ID)
	if len(list) != 0 {
		t.Fatalf("toggled-off reaction still listed: %+v", list)
	}

	// A different emoji REPLACES rather than accumulates — one reaction per person.
	if _, err := s.Reactions.SetReaction(ctx(), r); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	other := &model.Reaction{ChatID: chatID, MessageID: m.ID, UserID: owner, Emoji: "🎉", CreatedAt: 2}
	added, err = s.Reactions.SetReaction(ctx(), other)
	if err != nil || !added {
		t.Fatalf("replace reaction: %v added=%v", err, added)
	}
	list, _ = s.Reactions.ListReactions(ctx(), chatID, m.ID)
	if len(list) != 1 || list[0].Emoji != "🎉" {
		t.Fatalf("one reaction per user violated: %+v", list)
	}
}

// --- contacts --------------------------------------------------------------

func testContacts(t *testing.T, s store.Stores) {
	if s.Contacts == nil {
		t.Skip("backend has no contacts")
	}
	owner := mkUser(t, s)
	peer := mkUser(t, s)

	c := &model.Contact{OwnerID: owner, UserID: peer, Name: "Peer", CreatedAt: 10, UpdatedAt: 10}
	if err := s.Contacts.UpsertContact(ctx(), c); err != nil {
		t.Fatalf("UpsertContact: %v", err)
	}
	got, err := s.Contacts.GetContact(ctx(), owner, peer)
	if err != nil || got.Name != "Peer" {
		t.Fatalf("GetContact: %v / %+v", err, got)
	}

	// Rename through the same upsert.
	c.Name = "Renamed"
	c.UpdatedAt = 20
	if err := s.Contacts.UpsertContact(ctx(), c); err != nil {
		t.Fatalf("rename: %v", err)
	}
	got, _ = s.Contacts.GetContact(ctx(), owner, peer)
	if got.Name != "Renamed" {
		t.Fatalf("rename lost: %+v", got)
	}

	// Incremental sync: `since` is what keeps a reconnect from refetching the
	// whole address book.
	all, err := s.Contacts.ListContacts(ctx(), owner, 0, 100)
	if err != nil || len(all) != 1 {
		t.Fatalf("ListContacts(0): %v / %d", err, len(all))
	}
	recent, err := s.Contacts.ListContacts(ctx(), owner, 15, 100)
	if err != nil || len(recent) != 1 {
		t.Fatalf("ListContacts(since=15): %v / %d", err, len(recent))
	}
	none, err := s.Contacts.ListContacts(ctx(), owner, 999, 100)
	if err != nil || len(none) != 0 {
		t.Fatalf("ListContacts(since=999): %v / %d rows, want 0", err, len(none))
	}

	// Blocking someone who was never a contact must work — that is the usual case.
	stranger := mkUser(t, s)
	if err := s.Contacts.SetBlocked(ctx(), owner, stranger, true, 30); err != nil {
		t.Fatalf("SetBlocked on a non-contact: %v", err)
	}
	blocked, err := s.Contacts.IsBlocked(ctx(), owner, stranger)
	if err != nil || !blocked {
		t.Fatalf("IsBlocked: %v / %v", err, blocked)
	}
	// Blocking is directional at the store level; the service is what makes the
	// delivery rule symmetric.
	blocked, err = s.Contacts.IsBlocked(ctx(), stranger, owner)
	if err != nil || blocked {
		t.Fatalf("block leaked to the other direction: %v / %v", err, blocked)
	}

	if err := s.Contacts.SetBlocked(ctx(), owner, stranger, false, 40); err != nil {
		t.Fatalf("unblock: %v", err)
	}
	blocked, _ = s.Contacts.IsBlocked(ctx(), owner, stranger)
	if blocked {
		t.Fatal("still blocked after unblock")
	}

	if err := s.Contacts.DeleteContact(ctx(), owner, peer); err != nil {
		t.Fatalf("DeleteContact: %v", err)
	}
	if _, err := s.Contacts.GetContact(ctx(), owner, peer); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted contact still present: %v", err)
	}
}

// --- drafts ----------------------------------------------------------------

func testDrafts(t *testing.T, s store.Stores) {
	if s.Drafts == nil {
		t.Skip("backend has no drafts")
	}
	owner := mkUser(t, s)
	chatID := mkChat(t, s, owner)

	d := &model.Draft{UserID: owner, ChatID: chatID, Text: "unsent", UpdatedAt: 10}
	if err := s.Drafts.SetDraft(ctx(), d); err != nil {
		t.Fatalf("SetDraft: %v", err)
	}
	list, err := s.Drafts.ListDrafts(ctx(), owner, 0, 100)
	if err != nil || len(list) != 1 || list[0].Text != "unsent" {
		t.Fatalf("ListDrafts: %v / %+v", err, list)
	}

	d.Text = "changed"
	d.UpdatedAt = 20
	if err := s.Drafts.SetDraft(ctx(), d); err != nil {
		t.Fatalf("overwrite draft: %v", err)
	}
	list, _ = s.Drafts.ListDrafts(ctx(), owner, 0, 100)
	if len(list) != 1 || list[0].Text != "changed" {
		t.Fatalf("draft not replaced: %+v", list)
	}

	// A draft is private to its author even though it names a shared chat.
	other := mkUser(t, s)
	list, err = s.Drafts.ListDrafts(ctx(), other, 0, 100)
	if err != nil || len(list) != 0 {
		t.Fatalf("another user sees the draft: %v / %+v", err, list)
	}

	if err := s.Drafts.DeleteDraft(ctx(), owner, chatID); err != nil {
		t.Fatalf("DeleteDraft: %v", err)
	}
	list, _ = s.Drafts.ListDrafts(ctx(), owner, 0, 100)
	if len(list) != 0 {
		t.Fatalf("draft survived deletion: %+v", list)
	}
}

// --- pins ------------------------------------------------------------------

func testPins(t *testing.T, s store.Stores) {
	if s.Pins == nil {
		t.Skip("backend has no pins")
	}
	owner := mkUser(t, s)
	chatID := mkChat(t, s, owner)
	m := send(t, s, chatID, owner, "pin me")

	p := &model.PinnedMessage{ChatID: chatID, MessageID: m.ID, PinnedBy: owner, PinnedAt: 10}
	if err := s.Pins.Pin(ctx(), p); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	// Pinning twice must not produce two entries.
	if err := s.Pins.Pin(ctx(), p); err != nil {
		t.Fatalf("Pin repeat: %v", err)
	}
	list, err := s.Pins.ListPins(ctx(), chatID)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListPins: %v / %d rows", err, len(list))
	}

	if err := s.Pins.Unpin(ctx(), chatID, m.ID); err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	list, _ = s.Pins.ListPins(ctx(), chatID)
	if len(list) != 0 {
		t.Fatalf("pin survived unpin: %+v", list)
	}
}

// --- scheduled -------------------------------------------------------------

func testScheduled(t *testing.T, s store.Stores) {
	if s.Schedule == nil {
		t.Skip("backend has no schedule")
	}
	owner := mkUser(t, s)
	chatID := mkChat(t, s, owner)

	due := &model.ScheduledMessage{
		ID: nextID(), ChatID: chatID, SenderID: owner, Text: "later", SendAt: 100, CreatedAt: 1,
	}
	future := &model.ScheduledMessage{
		ID: nextID(), ChatID: chatID, SenderID: owner, Text: "much later", SendAt: 9_000_000_000_000, CreatedAt: 1,
	}
	for _, m := range []*model.ScheduledMessage{due, future} {
		if err := s.Schedule.CreateScheduled(ctx(), m); err != nil {
			t.Fatalf("CreateScheduled: %v", err)
		}
	}

	list, err := s.Schedule.ListScheduled(ctx(), owner, chatID)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListScheduled: %v / %d", err, len(list))
	}

	// Claiming is atomic so two dispatchers cannot both fire the same message.
	claimed, err := s.Schedule.ClaimDueScheduled(ctx(), 200, 10)
	if err != nil {
		t.Fatalf("ClaimDueScheduled: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != due.ID {
		t.Fatalf("claimed %d rows, want just the due one: %+v", len(claimed), claimed)
	}
	again, err := s.Schedule.ClaimDueScheduled(ctx(), 200, 10)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	for _, m := range again {
		if m.ID == due.ID {
			t.Fatal("the same scheduled message was claimed twice — it would send twice")
		}
	}

	// Cancelling is scoped to the sender.
	if err := s.Schedule.CancelScheduled(ctx(), future.ID, owner); err != nil {
		t.Fatalf("CancelScheduled: %v", err)
	}
	list, _ = s.Schedule.ListScheduled(ctx(), owner, chatID)
	for _, m := range list {
		if m.ID == future.ID {
			t.Fatal("cancelled message still pending")
		}
	}

	if _, err := s.Schedule.PurgeSentScheduled(ctx(), 9_000_000_000_000, 100); err != nil {
		t.Fatalf("PurgeSentScheduled: %v", err)
	}
}

// --- invites ---------------------------------------------------------------

// An invite link is a credential: bounded, revocable, and — the part worth a
// test — impossible to over-redeem by racing.
func testInvites(t *testing.T, s store.Stores) {
	if s.Invites == nil {
		t.Skip("backend has no invites")
	}
	owner := mkUser(t, s)
	chatID := mkChat(t, s, owner)

	code := "code-" + nextID()
	link := &model.InviteLink{Code: code, ChatID: chatID, CreatedBy: owner, CreatedAt: 1, MaxUses: 3}
	if err := s.Invites.CreateInvite(ctx(), link); err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	got, err := s.Invites.GetInvite(ctx(), code)
	if err != nil || got.ChatID != chatID {
		t.Fatalf("GetInvite: %v / %+v", err, got)
	}
	list, err := s.Invites.ListInvites(ctx(), chatID)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListInvites: %v / %d", err, len(list))
	}

	// Concurrent redemption of a link capped at 3 must yield exactly 3 successes.
	const racers = 12
	var wg sync.WaitGroup
	var okCount atomic.Int32
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Invites.UseInvite(ctx(), code, 10); err == nil {
				okCount.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := okCount.Load(); n != 3 {
		t.Fatalf("a link capped at 3 uses was redeemed %d times", n)
	}

	// Revocation stops further use.
	code2 := "code-" + nextID()
	if err := s.Invites.CreateInvite(ctx(), &model.InviteLink{Code: code2, ChatID: chatID, CreatedBy: owner, CreatedAt: 1}); err != nil {
		t.Fatalf("CreateInvite 2: %v", err)
	}
	if err := s.Invites.RevokeInvite(ctx(), code2, chatID); err != nil {
		t.Fatalf("RevokeInvite: %v", err)
	}
	if _, err := s.Invites.UseInvite(ctx(), code2, 10); err == nil {
		t.Fatal("a revoked link was still redeemable")
	}

	// An expired link is refused too.
	code3 := "code-" + nextID()
	if err := s.Invites.CreateInvite(ctx(), &model.InviteLink{
		Code: code3, ChatID: chatID, CreatedBy: owner, CreatedAt: 1, ExpiresAt: 50,
	}); err != nil {
		t.Fatalf("CreateInvite 3: %v", err)
	}
	if _, err := s.Invites.UseInvite(ctx(), code3, 100); err == nil {
		t.Fatal("an expired link was still redeemable")
	}

	if _, err := s.Invites.GetInvite(ctx(), "no-such-code"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing invite: got %v, want ErrNotFound", err)
	}
}

func testChatUsername(t *testing.T, s store.Stores) {
	if s.Invites == nil {
		t.Skip("backend has no invites")
	}
	owner := mkUser(t, s)
	chatID := mkChat(t, s, owner)
	handle := "handle" + nextID()

	if err := s.Invites.SetChatUsername(ctx(), chatID, handle); err != nil {
		t.Fatalf("SetChatUsername: %v", err)
	}
	got, err := s.Invites.GetChatByUsername(ctx(), handle)
	if err != nil || got.ID != chatID {
		t.Fatalf("GetChatByUsername: %v / %+v", err, got)
	}

	// A handle is a phishing surface if two chats can hold the same one.
	other := mkChat(t, s, owner)
	if err := s.Invites.SetChatUsername(ctx(), other, handle); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate handle: got %v, want ErrConflict", err)
	}

	// Clearing releases it.
	if err := s.Invites.SetChatUsername(ctx(), chatID, ""); err != nil {
		t.Fatalf("clear handle: %v", err)
	}
	if _, err := s.Invites.GetChatByUsername(ctx(), handle); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cleared handle still resolves: %v", err)
	}
}

// --- polls -----------------------------------------------------------------

func testPolls(t *testing.T, s store.Stores) {
	if s.Polls == nil {
		t.Skip("backend has no polls")
	}
	owner := mkUser(t, s)
	voter := mkUser(t, s)
	chatID := mkChat(t, s, owner)
	addMember(t, s, chatID, voter, model.RoleMember)
	m := send(t, s, chatID, owner, "poll")

	p := &model.Poll{
		ID: nextID(), ChatID: chatID, MessageID: m.ID, CreatorID: owner,
		Question: "Tabs or spaces?", Options: []string{"Tabs", "Spaces"}, CreatedAt: 1,
	}
	if err := s.Polls.CreatePoll(ctx(), p); err != nil {
		t.Fatalf("CreatePoll: %v", err)
	}
	got, err := s.Polls.GetPoll(ctx(), p.ID)
	if err != nil || len(got.Options) != 2 {
		t.Fatalf("GetPoll: %v / %+v", err, got)
	}
	byMsg, err := s.Polls.GetPollByMessage(ctx(), m.ID)
	if err != nil || byMsg.ID != p.ID {
		t.Fatalf("GetPollByMessage: %v / %+v", err, byMsg)
	}

	if _, err := s.Polls.Vote(ctx(), &model.PollVote{PollID: p.ID, UserID: voter, OptionIndex: 1, CreatedAt: 2}, false); err != nil {
		t.Fatalf("Vote: %v", err)
	}
	tally, err := s.Polls.Tally(ctx(), p.ID)
	if err != nil || tally[1] != 1 {
		t.Fatalf("Tally: %v / %v", err, tally)
	}

	// Single-choice REPLACES the voter's previous pick rather than adding.
	if _, err := s.Polls.Vote(ctx(), &model.PollVote{PollID: p.ID, UserID: voter, OptionIndex: 0, CreatedAt: 3}, false); err != nil {
		t.Fatalf("Vote replace: %v", err)
	}
	tally, _ = s.Polls.Tally(ctx(), p.ID)
	if tally[0] != 1 || tally[1] != 0 {
		t.Fatalf("single-choice vote accumulated instead of replacing: %v", tally)
	}
	mine, err := s.Polls.VotedOptions(ctx(), p.ID, voter)
	if err != nil || len(mine) != 1 || mine[0] != 0 {
		t.Fatalf("VotedOptions: %v / %v", err, mine)
	}

	// Closing stops votes — enforced in the store, because the caller's check and
	// its write are two steps a concurrent close can land between.
	if err := s.Polls.ClosePoll(ctx(), p.ID); err != nil {
		t.Fatalf("ClosePoll: %v", err)
	}
	if _, err := s.Polls.Vote(ctx(), &model.PollVote{PollID: p.ID, UserID: owner, OptionIndex: 1, CreatedAt: 4}, false); !errors.Is(err, store.ErrPollClosed) {
		t.Fatalf("vote on a closed poll: got %v, want ErrPollClosed", err)
	}
}

// --- calls -----------------------------------------------------------------

func testCalls(t *testing.T, s store.Stores) {
	if s.Calls == nil {
		t.Skip("backend has no calls")
	}
	owner := mkUser(t, s)
	peer := mkUser(t, s)
	chatID := mkChat(t, s, owner)
	addMember(t, s, chatID, peer, model.RoleMember)

	c := &model.Call{
		ID: nextID(), ChatID: chatID, InitiatorID: owner,
		Kind: model.CallAudio, State: model.CallRinging, CreatedAt: 1,
	}
	if err := s.Calls.CreateCall(ctx(), c); err != nil {
		t.Fatalf("CreateCall: %v", err)
	}
	got, err := s.Calls.GetCall(ctx(), c.ID)
	if err != nil || got.State != model.CallRinging {
		t.Fatalf("GetCall: %v / %+v", err, got)
	}

	// A second caller must JOIN the in-progress room rather than open a rival one,
	// which is what this lookup is for.
	active, err := s.Calls.ActiveCallForChat(ctx(), chatID)
	if err != nil || active.ID != c.ID {
		t.Fatalf("ActiveCallForChat: %v / %+v", err, active)
	}

	for _, p := range []*model.CallParticipant{
		{CallID: c.ID, UserID: owner, DeviceID: "dev-a", State: model.PartJoined, JoinedAt: 1},
		{CallID: c.ID, UserID: peer, State: model.PartInvited},
	} {
		if err := s.Calls.UpsertParticipant(ctx(), p); err != nil {
			t.Fatalf("UpsertParticipant: %v", err)
		}
	}
	parts, err := s.Calls.ListParticipants(ctx(), c.ID)
	if err != nil || len(parts) != 2 {
		t.Fatalf("ListParticipants: %v / %d", err, len(parts))
	}

	// Upsert updates in place rather than adding a row.
	if err := s.Calls.UpsertParticipant(ctx(), &model.CallParticipant{
		CallID: c.ID, UserID: peer, DeviceID: "dev-b", State: model.PartJoined, JoinedAt: 2,
	}); err != nil {
		t.Fatalf("UpsertParticipant update: %v", err)
	}
	parts, _ = s.Calls.ListParticipants(ctx(), c.ID)
	if len(parts) != 2 {
		t.Fatalf("participant duplicated on update: %d rows", len(parts))
	}

	if err := s.Calls.SetCallState(ctx(), c.ID, model.CallEnded, 99); err != nil {
		t.Fatalf("SetCallState: %v", err)
	}
	got, _ = s.Calls.GetCall(ctx(), c.ID)
	if got.State != model.CallEnded {
		t.Fatalf("state not updated: %+v", got)
	}
	// An ended room is no longer the chat's active call.
	if _, err := s.Calls.ActiveCallForChat(ctx(), chatID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ended call still reported active: %v", err)
	}
}

// testSecretQueue certifies the offline queue for end-to-end ciphertext.
//
// Everything here is a property the gateway RELIES ON but cannot enforce, which
// is why it belongs in the conformance suite rather than in one backend's tests:
// a memory store that ordered oldest-first and a Postgres store that did not
// would type-check identically and deliver a conversation backwards.
func testSecretQueue(t *testing.T, s store.Stores) {
	if s.SecretQ == nil {
		t.Skip("no secret queue store")
	}
	to, from := nextID(), nextID()
	const dev = "laptop-1"

	mk := func(n int) *model.SecretEnvelope {
		return &model.SecretEnvelope{
			ID: nextID(), ToUserID: to, ToDeviceID: dev,
			FromUserID: from, FromDeviceID: "phone-1",
			Header:     []byte(fmt.Sprintf("hdr-%d", n)),
			Ciphertext: []byte(fmt.Sprintf("ct-%d", n)),
			CreatedAt:  int64(n), ExpiresAt: 9_000_000_000_000,
		}
	}

	first, second := mk(1), mk(2)
	for _, e := range []*model.SecretEnvelope{first, second} {
		if err := s.SecretQ.EnqueueSecret(ctx(), e, 100); err != nil {
			t.Fatalf("EnqueueSecret: %v", err)
		}
	}

	// Oldest first. Ratchet messages decrypt in order far more cheaply than out
	// of it, so a backend that returned the newest first would make the receiver
	// derive a skipped key for every envelope behind it.
	page, err := s.SecretQ.PendingSecrets(ctx(), to, dev, "", 10)
	if err != nil {
		t.Fatalf("PendingSecrets: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("want 2 queued, got %d", len(page))
	}
	if page[0].ID != first.ID || page[1].ID != second.ID {
		t.Fatal("queue is not oldest-first")
	}
	if string(page[0].Ciphertext) != "ct-1" || string(page[0].Header) != "hdr-1" {
		t.Fatalf("payload did not round-trip: %q / %q", page[0].Header, page[0].Ciphertext)
	}

	// The cursor is the id of the last envelope taken, so the entry that MATCHES
	// it is the boundary and not a result — otherwise an interrupted sync
	// redelivers the message it already confirmed, forever.
	rest, err := s.SecretQ.PendingSecrets(ctx(), to, dev, first.ID, 10)
	if err != nil {
		t.Fatalf("PendingSecrets(after): %v", err)
	}
	if len(rest) != 1 || rest[0].ID != second.ID {
		t.Fatalf("cursor paging wrong: %d rows", len(rest))
	}

	// A different device of the SAME user shares nothing: a secret message is
	// encrypted to one device's session and no sibling can read it.
	if other, err := s.SecretQ.PendingSecrets(ctx(), to, "phone-9", "", 10); err != nil || len(other) != 0 {
		t.Fatalf("another device sees this queue: %d rows, err=%v", len(other), err)
	}

	// The ack is scoped by owner. An envelope id travels to the client, so a
	// delete that trusted the id alone would let anyone drop anyone's mail.
	if n, err := s.SecretQ.AckSecrets(ctx(), nextID(), dev, []string{first.ID}); err != nil || n != 0 {
		t.Fatalf("a stranger deleted %d envelopes (err=%v)", n, err)
	}
	if n, err := s.SecretQ.AckSecrets(ctx(), to, dev, []string{first.ID}); err != nil || n != 1 {
		t.Fatalf("AckSecrets: n=%d err=%v", n, err)
	}
	left, _ := s.SecretQ.PendingSecrets(ctx(), to, dev, "", 10)
	if len(left) != 1 || left[0].ID != second.ID {
		t.Fatalf("ack removed the wrong row: %d left", len(left))
	}

	// The per-device cap drops from the FRONT. The oldest undelivered ciphertext
	// is the one whose ratchet session is least likely to still exist, and an
	// uncapped queue addressed by a client-asserted device id is a way to fill
	// the database on somebody else's behalf.
	capDev := "capped-1"
	var lastID string
	for i := 0; i < 5; i++ {
		e := mk(10 + i)
		e.ToDeviceID = capDev
		lastID = e.ID
		if err := s.SecretQ.EnqueueSecret(ctx(), e, 3); err != nil {
			t.Fatalf("EnqueueSecret(capped): %v", err)
		}
	}
	capped, err := s.SecretQ.PendingSecrets(ctx(), to, capDev, "", 10)
	if err != nil {
		t.Fatalf("PendingSecrets(capped): %v", err)
	}
	if len(capped) != 3 {
		t.Fatalf("cap not enforced: %d rows retained, want 3", len(capped))
	}
	if capped[len(capped)-1].ID != lastID {
		t.Fatal("the cap evicted the newest envelope instead of the oldest")
	}

	// Expiry is not optional: every queued row records that one account messaged
	// another at a moment, which the stateless relay never wrote down.
	exp := mk(99)
	exp.ToDeviceID = "expiring-1"
	exp.ExpiresAt = 1
	if err := s.SecretQ.EnqueueSecret(ctx(), exp, 100); err != nil {
		t.Fatalf("EnqueueSecret(expiring): %v", err)
	}
	if n, err := s.SecretQ.PurgeExpiredSecrets(ctx(), 1000, 100); err != nil || n < 1 {
		t.Fatalf("PurgeExpiredSecrets: n=%d err=%v", n, err)
	}
	if gone, _ := s.SecretQ.PendingSecrets(ctx(), to, "expiring-1", "", 10); len(gone) != 0 {
		t.Fatal("an expired envelope survived collection")
	}
	// And collection must not touch rows that are still live.
	if live, _ := s.SecretQ.PendingSecrets(ctx(), to, dev, "", 10); len(live) != 1 {
		t.Fatalf("collection removed a live envelope: %d left", len(live))
	}
}

// testChatList certifies the one-query chat list.
//
// It is in the conformance suite and not in one backend's tests because the two
// implementations answer the same question by completely different means — a SQL
// join with a LATERAL, and a sort over in-process maps. They must agree on the
// order, the unread arithmetic and the cursor semantics, and none of that is
// visible to a type checker.
func testChatList(t *testing.T, s store.Stores) {
	reader, ok := s.Chats.(store.ChatSummaryReader)
	if !ok {
		t.Skip("backend cannot answer the chat list in one query")
	}
	flags, ok := s.Chats.(store.MemberFlagStore)
	if !ok {
		t.Skip("backend has no member flags")
	}
	me, peer := nextID(), nextID()

	// Three chats whose creation order is the REVERSE of their activity order, so
	// a backend that still sorted by id would fail on every position.
	type seed struct {
		chatID   string
		activity int64
	}
	seeds := []seed{}
	for i := 0; i < 3; i++ {
		chatID := nextID()
		if err := s.Chats.CreateChat(ctx(), &model.Chat{
			ID: chatID, Type: model.ChatGroup, Title: "c" + chatID, OwnerID: me, CreatedAt: 1,
		}); err != nil {
			t.Fatalf("CreateChat: %v", err)
		}
		for _, uid := range []string{me, peer} {
			if err := s.Chats.AddMember(ctx(), &model.ChatMember{
				ChatID: chatID, UserID: uid, Role: model.RoleMember, JoinedAt: 1,
			}); err != nil {
				t.Fatalf("AddMember: %v", err)
			}
		}
		// Later-created chats get OLDER activity.
		activity := int64(3000 - i*1000)
		if _, _, err := s.Messages.InsertMessage(ctx(), &model.Message{
			ID: nextID(), ChatID: chatID, SenderID: peer,
			Text: "msg in " + chatID, CreatedAt: activity,
		}, "", nil); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
		seeds = append(seeds, seed{chatID, activity})
	}

	page, err := reader.UserChatSummaries(ctx(), me, 0, "", 10, false)
	if err != nil {
		t.Fatalf("UserChatSummaries: %v", err)
	}
	if len(page) != 3 {
		t.Fatalf("got %d rows, want 3", len(page))
	}
	for i := 1; i < len(page); i++ {
		if page[i-1].LastActivityAt < page[i].LastActivityAt {
			t.Fatalf("not in activity order: %d before %d",
				page[i-1].LastActivityAt, page[i].LastActivityAt)
		}
	}
	if page[0].LastActivityAt != 3000 || page[0].Chat.ID != seeds[0].chatID {
		t.Fatalf("top row is %s at %d, want %s at 3000",
			page[0].Chat.ID, page[0].LastActivityAt, seeds[0].chatID)
	}
	// The row carries what a list draws, which it previously did not.
	if page[0].LastMessage == nil {
		t.Fatal("no last-message preview")
	}
	if page[0].LastMessage.Text != "msg in "+seeds[0].chatID {
		t.Fatalf("preview is %q", page[0].LastMessage.Text)
	}
	if page[0].UnreadCount != 1 {
		t.Fatalf("unread = %d with nothing read, want 1", page[0].UnreadCount)
	}
	if page[0].MyRole != model.RoleMember {
		t.Fatalf("role = %q", page[0].MyRole)
	}

	// Unread follows the read cursor, and CLAMPS rather than going negative when
	// the cursor sits past the newest live message.
	if err := s.Reads.SetRead(ctx(), &model.ReadState{
		ChatID: seeds[0].chatID, UserID: me, UpToSeq: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("SetRead: %v", err)
	}
	page, _ = reader.UserChatSummaries(ctx(), me, 0, "", 10, false)
	if page[0].UnreadCount != 0 {
		t.Fatalf("unread = %d after reading the only message, want 0", page[0].UnreadCount)
	}
	if err := s.Reads.SetRead(ctx(), &model.ReadState{
		ChatID: seeds[0].chatID, UserID: me, UpToSeq: 999, UpdatedAt: 2,
	}); err != nil {
		t.Fatalf("SetRead: %v", err)
	}
	page, _ = reader.UserChatSummaries(ctx(), me, 0, "", 10, false)
	if page[0].UnreadCount != 0 {
		t.Fatalf("unread = %d for an over-advanced cursor, want 0", page[0].UnreadCount)
	}

	// Keyset paging covers every row exactly once. Both halves of the cursor are
	// passed, which is what keeps a page boundary stable while the list reorders.
	seen := map[string]int{}
	var afterAct int64
	var afterID string
	for i := 0; i < 5; i++ {
		got, err := reader.UserChatSummaries(ctx(), me, afterAct, afterID, 1, false)
		if err != nil {
			t.Fatalf("page %d: %v", i, err)
		}
		if len(got) == 0 {
			break
		}
		seen[got[0].Chat.ID]++
		afterAct, afterID = got[0].LastActivityAt, got[0].Chat.ID
	}
	if len(seen) != 3 {
		t.Fatalf("paging saw %d distinct chats, want 3", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("chat %s appeared %d times across pages", id, n)
		}
	}

	// Archiving is one member's opinion: it hides the chat from their list and from
	// nobody else's.
	if err := flags.SetMemberFlags(ctx(), seeds[0].chatID, me, model.MemberFlags{Archived: true}); err != nil {
		t.Fatalf("SetMemberFlags: %v", err)
	}
	main, _ := reader.UserChatSummaries(ctx(), me, 0, "", 10, false)
	if len(main) != 2 {
		t.Fatalf("main list has %d rows after archiving one, want 2", len(main))
	}
	withArchived, _ := reader.UserChatSummaries(ctx(), me, 0, "", 10, true)
	if len(withArchived) != 3 {
		t.Fatalf("archived pile has %d rows, want 3", len(withArchived))
	}
	peerList, _ := reader.UserChatSummaries(ctx(), peer, 0, "", 10, false)
	if len(peerList) != 3 {
		t.Fatalf("one member's archive hid a chat from another: %d rows", len(peerList))
	}

	// Flags round-trip, and the mute deadline is what the notification path reads.
	want := model.MemberFlags{MutedUntil: 5_000, Pinned: true, Archived: false}
	if err := flags.SetMemberFlags(ctx(), seeds[1].chatID, me, want); err != nil {
		t.Fatalf("SetMemberFlags: %v", err)
	}
	got, err := flags.GetMemberFlags(ctx(), seeds[1].chatID, me)
	if err != nil {
		t.Fatalf("GetMemberFlags: %v", err)
	}
	if got != want {
		t.Fatalf("flags round-trip: got %+v want %+v", got, want)
	}
	if !got.MutedAt(4_000) || got.MutedAt(6_000) {
		t.Fatalf("mute deadline is not honoured as a deadline: %+v", got)
	}

	// A non-member's flag write touches nothing rather than creating a membership.
	if err := flags.SetMemberFlags(ctx(), seeds[1].chatID, nextID(), want); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a non-member's flag write returned %v, want ErrNotFound", err)
	}
}
