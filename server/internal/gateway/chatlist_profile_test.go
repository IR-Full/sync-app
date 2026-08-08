package gateway_test

import (
	"net"
	"testing"

	"github.com/synapse-chat/synapse/pkg/wire"
)

// connectWithName registers an account WITH a display name and returns the
// client together with the AUTH_OK body, which is the only place a client is
// ever told who it is.
func connectWithName(t *testing.T, addr, user, pass, displayName string) (*testClient, wire.AuthOKBody) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	cl := &testClient{conn: wire.NewConn(wire.NewTCPTransport(c), false)}
	cl.send(t, wire.MsgHello, 0, wire.HelloBody{ClientVersion: "test", Platform: "cli"})
	if e := cl.read(t); e.Type != wire.MsgWelcome {
		t.Fatalf("want WELCOME got %s", e.Type)
	}
	cl.send(t, wire.MsgAuth, 1, wire.AuthBody{
		Username: user, Password: pass, Register: true, DisplayName: displayName,
	})
	e := cl.read(t)
	if e.Type != wire.MsgAuthOK {
		t.Fatalf("want AUTH_OK got %s", e.Type)
	}
	var ok wire.AuthOKBody
	if err := wire.Unmarshal(e.Body, &ok); err != nil {
		t.Fatal(err)
	}
	cl.userID, cl.deviceID, cl.resumeToken = ok.UserID, ok.DeviceID, ok.ResumeToken
	return cl, ok
}

// readProfile waits for the PROFILE that answers a specific request. A profile
// change is also PUSHED to the user's own devices, and the sending connection is
// one of them, so a correlated read is the only way to tell the reply from the
// mirror of an earlier change.
func readProfile(t *testing.T, c *testClient, reqID uint64) wire.ProfileBody {
	t.Helper()
	for {
		e := c.readUntil(t, wire.MsgProfile)
		if e.RequestID != reqID {
			continue
		}
		var p wire.ProfileBody
		if err := wire.Unmarshal(e.Body, &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
}

func chatList(t *testing.T, c *testClient, reqID uint64, body wire.ChatListBody) wire.ChatsBody {
	t.Helper()
	c.send(t, wire.MsgChatList, reqID, body)
	e := c.readUntil(t, wire.MsgChats)
	var out wire.ChatsBody
	if err := wire.Unmarshal(e.Body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestChatListRebuildsAFreshInstall is the case the chat list exists for: a
// client with NO local state learns which conversations it is in. Before
// CHAT_LIST the membership was only observable as a side effect of traffic, so
// a reinstalled app stayed empty until somebody happened to write.
func TestChatListRebuildsAFreshInstall(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "listalice", "secret123")
	bob := connect(t, addr, "listbob", "secret123")

	// One direct chat (created by resolving @listbob) and one group.
	alice.send(t, wire.MsgSend, 1, wire.SendBody{ChatID: "@listbob", DedupKey: "d1", Text: "hi"})
	ack := alice.readUntil(t, wire.MsgSendAck)
	var ab wire.SendAckBody
	_ = wire.Unmarshal(ack.Body, &ab)
	alice.send(t, wire.MsgChatCreate, 2, wire.ChatCreateBody{Type: "group", Title: "Standup"})
	info := alice.readUntil(t, wire.MsgChatInfo)
	var ci wire.ChatInfoBody
	_ = wire.Unmarshal(info.Body, &ci)

	// A brand-new connection for the same account: no cached chats anywhere.
	fresh := login(t, addr, "listalice", "secret123")
	list := chatList(t, fresh, 1, wire.ChatListBody{})
	if !list.Done {
		t.Fatalf("two chats should fit one page: %+v", list)
	}
	if len(list.Chats) != 2 {
		t.Fatalf("want 2 chats, got %d (%+v)", len(list.Chats), list.Chats)
	}

	byID := map[string]wire.ChatSummary{}
	for _, s := range list.Chats {
		byID[s.ChatID] = s
	}
	direct, ok := byID[ab.ChatID]
	if !ok {
		t.Fatalf("the direct chat is missing from the list: %+v", list.Chats)
	}
	if direct.Type != "direct" {
		t.Fatalf("direct chat typed %q", direct.Type)
	}
	// A 1:1 chat has no title, so the peer is the only thing to name it after.
	if direct.PeerID != bob.userID {
		t.Fatalf("peer_id=%q want %q", direct.PeerID, bob.userID)
	}
	if direct.LastSeq != 1 {
		t.Fatalf("last_seq=%d want 1 (one message sent)", direct.LastSeq)
	}

	group, ok := byID[ci.ChatID]
	if !ok {
		t.Fatalf("the group is missing from the list: %+v", list.Chats)
	}
	if group.Title != "Standup" || group.MyRole != "owner" || group.PeerID != "" {
		t.Fatalf("bad group summary: %+v", group)
	}

	// Bob sees the direct chat and NOT alice's group — the list is per member.
	bobList := chatList(t, bob, 1, wire.ChatListBody{})
	if len(bobList.Chats) != 1 || bobList.Chats[0].ChatID != ab.ChatID {
		t.Fatalf("bob's list leaked or lost chats: %+v", bobList.Chats)
	}
	if bobList.Chats[0].PeerID != alice.userID {
		t.Fatalf("bob's peer is %q, want alice", bobList.Chats[0].PeerID)
	}
}

// TestChatListPagesByCursor walks the list in pages. The cursor is the last id
// of a page, so a client that stopped can resume without re-reading (or
// skipping) rows — which is what an offset would get wrong the moment a chat is
// created between two pages.
func TestChatListPagesByCursor(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "pagealice", "secret123")

	want := map[string]bool{}
	for _, title := range []string{"one", "two", "three"} {
		alice.send(t, wire.MsgChatCreate, 1, wire.ChatCreateBody{Type: "group", Title: title})
		e := alice.readUntil(t, wire.MsgChatInfo)
		var ci wire.ChatInfoBody
		_ = wire.Unmarshal(e.Body, &ci)
		want[ci.ChatID] = true
	}

	first := chatList(t, alice, 2, wire.ChatListBody{Limit: 2})
	if len(first.Chats) != 2 || first.Done {
		t.Fatalf("first page: got %d chats, done=%v", len(first.Chats), first.Done)
	}
	if first.NextAfter != first.Chats[1].ChatID {
		t.Fatalf("cursor %q is not the last row %q", first.NextAfter, first.Chats[1].ChatID)
	}

	second := chatList(t, alice, 3, wire.ChatListBody{After: first.NextAfter, Limit: 2})
	if len(second.Chats) != 1 || !second.Done {
		t.Fatalf("second page: got %d chats, done=%v", len(second.Chats), second.Done)
	}

	seen := map[string]bool{}
	for _, s := range append(first.Chats, second.Chats...) {
		if seen[s.ChatID] {
			t.Fatalf("chat %s appeared on both pages", s.ChatID)
		}
		seen[s.ChatID] = true
		if !want[s.ChatID] {
			t.Fatalf("unknown chat %s in the list", s.ChatID)
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("paging lost chats: saw %d of %d", len(seen), len(want))
	}
}

// TestProfileIsReadableAndWritable covers the whole loop the protocol used to be
// missing: a name given at registration comes back, can be changed afterwards,
// survives a new login, and is visible to someone who knows only the handle.
func TestProfileIsReadableAndWritable(t *testing.T) {
	addr := startGateway(t)
	alice, ok := connectWithName(t, addr, "profalice", "secret123", "Alice Liddell")
	if ok.Username != "profalice" || ok.DisplayName != "Alice Liddell" {
		t.Fatalf("AUTH_OK did not carry the identity: %+v", ok)
	}

	alice.send(t, wire.MsgProfileSet, 1, wire.ProfileSetBody{
		DisplayName: "Alice in Wonderland", AvatarRef: "m123-abc",
	})
	if p := readProfile(t, alice, 1); p.DisplayName != "Alice in Wonderland" || p.AvatarRef != "m123-abc" {
		t.Fatalf("profile not updated: %+v", p)
	}

	// A partial update leaves the other field alone (empty means "as is").
	alice.send(t, wire.MsgProfileSet, 2, wire.ProfileSetBody{DisplayName: "Alice"})
	if p := readProfile(t, alice, 2); p.DisplayName != "Alice" || p.AvatarRef != "m123-abc" {
		t.Fatalf("partial update clobbered the avatar: %+v", p)
	}

	// Clearing is explicit, because an empty string cannot mean both things.
	alice.send(t, wire.MsgProfileSet, 3, wire.ProfileSetBody{ClearAvatar: true})
	if p := readProfile(t, alice, 3); p.AvatarRef != "" || p.DisplayName != "Alice" {
		t.Fatalf("clear_avatar did the wrong thing: %+v", p)
	}

	// A stranger resolves the handle — this is also the user lookup.
	bob := connect(t, addr, "profbob", "secret123")
	bob.send(t, wire.MsgProfileGet, 1, wire.ProfileGetBody{Target: "@profalice"})
	if p := readProfile(t, bob, 1); p.UserID != alice.userID || p.DisplayName != "Alice" {
		t.Fatalf("handle lookup returned %+v", p)
	}

	// A later login reports the CURRENT name, and does not revert it to whatever
	// the logging-in client happened to send.
	again := loginWithName(t, addr, "profalice", "secret123", "Stale Client Name")
	if again.DisplayName != "Alice" {
		t.Fatalf("login rewrote the display name: %+v", again)
	}
}

// loginWithName logs an EXISTING account in while sending a display name, which
// must be ignored: only registration and PROFILE_SET may write it.
func loginWithName(t *testing.T, addr, user, pass, displayName string) wire.AuthOKBody {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	cl := &testClient{conn: wire.NewConn(wire.NewTCPTransport(c), false)}
	cl.send(t, wire.MsgHello, 0, wire.HelloBody{ClientVersion: "test", Platform: "cli"})
	if e := cl.read(t); e.Type != wire.MsgWelcome {
		t.Fatalf("want WELCOME got %s", e.Type)
	}
	cl.send(t, wire.MsgAuth, 1, wire.AuthBody{Username: user, Password: pass, DisplayName: displayName})
	e := cl.read(t)
	if e.Type != wire.MsgAuthOK {
		t.Fatalf("want AUTH_OK got %s", e.Type)
	}
	var ok wire.AuthOKBody
	_ = wire.Unmarshal(e.Body, &ok)
	return ok
}

// TestProfileChangeReachesOtherDevices: a profile is per ACCOUNT, so a name
// changed on the phone must not stay stale on the desktop until that session
// happens to reconnect.
func TestProfileChangeReachesOtherDevices(t *testing.T) {
	addr := startGateway(t)
	phone := connect(t, addr, "multialice", "secret123")
	desktop := login(t, addr, "multialice", "secret123")

	phone.send(t, wire.MsgProfileSet, 1, wire.ProfileSetBody{DisplayName: "Renamed"})
	e := desktop.readUntil(t, wire.MsgProfile)
	var p wire.ProfileBody
	_ = wire.Unmarshal(e.Body, &p)
	if p.DisplayName != "Renamed" || p.UserID != phone.userID {
		t.Fatalf("the other device was not told: %+v", p)
	}
}

// TestProfileRejectsUnrenderableNames keeps the display name a name. A control
// character is refused rather than stripped: a label that renders differently
// from what was sent is a spoofing tool, and silently rewriting it would hide
// that from the person who typed it.
func TestProfileRejectsUnrenderableNames(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "badnamealice", "secret123")

	for i, name := range []string{"two\nlines", "zero\x00width", string(make([]byte, 200))} {
		alice.send(t, wire.MsgProfileSet, uint64(i+1), wire.ProfileSetBody{DisplayName: name})
		e := alice.readUntil(t, wire.MsgError)
		var eb wire.ErrorBody
		_ = wire.Unmarshal(e.Body, &eb)
		if eb.Code != wire.ErrBadArg {
			t.Fatalf("name %q got code %d, want bad-arg", name, eb.Code)
		}
	}

	// An unknown handle is not found, not an internal error.
	alice.send(t, wire.MsgProfileGet, 9, wire.ProfileGetBody{Target: "@nobody-at-all"})
	e := alice.readUntil(t, wire.MsgError)
	var eb wire.ErrorBody
	_ = wire.Unmarshal(e.Body, &eb)
	if eb.Code != wire.ErrNotFound {
		t.Fatalf("unknown handle got code %d, want not-found", eb.Code)
	}
}
