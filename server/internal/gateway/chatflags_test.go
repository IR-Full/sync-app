package gateway_test

import (
	"testing"
	"time"

	"github.com/IR-Full/sync-app/server/pkg/wire"
)

/*
Muting a chat used to be impossible while looking supported from every angle.

chat_members.muted shipped in the first migration. The model carried it, the gRPC
converters moved it back and forth, and the store read and wrote it — and nothing
ever CONSULTED it: there was no protocol message a client could send to set it, and
the notification path never looked. A schema that implies a feature nobody can use
is worse than one that omits it, because it is invisible in review.

These tests exercise the whole path through the real gateway: set the flag, read it
back on the chat list, and check the boundaries (a past deadline is not a mute, and
a member can only ever write their own row).
*/

// setFlags sends CHAT_FLAGS and returns what the server stored.
func setFlags(t *testing.T, c *testClient, reqID uint64, body wire.ChatFlagsBody) wire.ChatFlagsSetBody {
	t.Helper()
	c.send(t, wire.MsgChatFlags, reqID, body)
	e := c.readUntil(t, wire.MsgChatFlagsSet)
	var out wire.ChatFlagsSetBody
	if err := wire.Unmarshal(e.Body, &out); err != nil {
		t.Fatalf("decode CHAT_FLAGS_SET: %v", err)
	}
	return out
}

// findChat locates one chat in a list page.
func findChat(t *testing.T, list wire.ChatsBody, chatID string) wire.ChatSummary {
	t.Helper()
	for _, row := range list.Chats {
		if row.ChatID == chatID {
			return row
		}
	}
	t.Fatalf("chat %s not in the list", chatID)
	return wire.ChatSummary{}
}

// newGroup creates a group chat and returns its id.
func newGroup(t *testing.T, c *testClient, reqID uint64, title string) string {
	t.Helper()
	c.send(t, wire.MsgChatCreate, reqID, wire.ChatCreateBody{Type: "group", Title: title})
	var ci wire.ChatInfoBody
	if err := wire.Unmarshal(c.readUntil(t, wire.MsgChatInfo).Body, &ci); err != nil {
		t.Fatalf("decode CHAT_INFO: %v", err)
	}
	return ci.ChatID
}

// TestChatFlagsRoundTripThroughTheList is the end-to-end case: a flag set with
// CHAT_FLAGS comes back on the chat list, which is the only place a client would
// ever read it.
func TestChatFlagsRoundTripThroughTheList(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "flagalice", "secret123")
	chatID := newGroup(t, alice, 1, "work")

	until := time.Now().Add(8 * time.Hour).UnixMilli()
	got := setFlags(t, alice, 2, wire.ChatFlagsBody{
		ChatID: chatID, MutedUntil: until, Pinned: true,
	})
	if got.MutedUntil != until || !got.Pinned || got.Archived {
		t.Fatalf("server stored %+v", got)
	}

	list := chatList(t, alice, 3, wire.ChatListBody{Limit: 10})
	row := findChat(t, list, chatID)
	if row.MutedUntil != until {
		t.Fatalf("list reports muted_until=%d, want %d", row.MutedUntil, until)
	}
	if !row.Pinned {
		t.Fatal("list does not report the chat as pinned")
	}

	// Unmuting is a zero deadline, not a separate verb.
	got = setFlags(t, alice, 4, wire.ChatFlagsBody{ChatID: chatID, Pinned: true})
	if got.MutedUntil != 0 {
		t.Fatalf("unmute left muted_until=%d", got.MutedUntil)
	}
}

// TestMuteDeadlineInThePastIsNotAMute checks the normalisation. Storing an expired
// deadline verbatim would make every client interpret a timestamp before it could
// draw a toggle.
func TestMuteDeadlineInThePastIsNotAMute(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "pastalice", "secret123")
	chatID := newGroup(t, alice, 1, "old")

	got := setFlags(t, alice, 2, wire.ChatFlagsBody{
		ChatID: chatID, MutedUntil: time.Now().Add(-time.Hour).UnixMilli(),
	})
	if got.MutedUntil != 0 {
		t.Fatalf("an expired deadline was stored as %d, want 0", got.MutedUntil)
	}
}

// TestChatFlagsAreScopedToTheCallersOwnMembership is the authorization test. Flags
// are one person's settings about a shared conversation, so a non-member naming a
// chat must change nothing — and must not acquire a membership by asking.
func TestChatFlagsAreScopedToTheCallersOwnMembership(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "scopeflagalice", "secret123")
	bob := connect(t, addr, "scopeflagbob", "secret123")
	chatID := newGroup(t, alice, 1, "private")

	// Bob is not a member. His write must be refused rather than silently stored.
	bob.send(t, wire.MsgChatFlags, 2, wire.ChatFlagsBody{
		ChatID: chatID, MutedUntil: time.Now().Add(time.Hour).UnixMilli(),
	})
	e := bob.read(t)
	if e.Type == wire.MsgChatFlagsSet {
		t.Fatal("a non-member set flags on a chat they cannot see")
	}
	if e.Type != wire.MsgError {
		t.Fatalf("want an error for a non-member, got %s", e.Type)
	}

	// And Bob did not become a member by trying.
	list := chatList(t, bob, 3, wire.ChatListBody{Limit: 10})
	for _, row := range list.Chats {
		if row.ChatID == chatID {
			t.Fatal("the failed flag write created a membership")
		}
	}

	// Alice's own flags are untouched by Bob's attempt.
	aliceList := chatList(t, alice, 4, wire.ChatListBody{Limit: 10})
	if row := findChat(t, aliceList, chatID); row.MutedUntil != 0 {
		t.Fatalf("another account's write changed Alice's flags: %d", row.MutedUntil)
	}
}

// TestArchivedChatLeavesTheMainList checks the filter, including that it is
// per-member: archiving must not remove the chat for the other participant.
func TestArchivedChatLeavesTheMainList(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "archalice", "secret123")
	bob := connect(t, addr, "archbob", "secret123")

	alice.send(t, wire.MsgSend, 1, wire.SendBody{
		ChatID: "@archbob", DedupKey: "a1", Text: "hello",
	})
	var ack wire.SendAckBody
	if err := wire.Unmarshal(alice.readUntil(t, wire.MsgSendAck).Body, &ack); err != nil {
		t.Fatal(err)
	}

	if got := setFlags(t, alice, 2, wire.ChatFlagsBody{ChatID: ack.ChatID, Archived: true}); !got.Archived {
		t.Fatal("archive not stored")
	}

	main := chatList(t, alice, 3, wire.ChatListBody{Limit: 10})
	for _, row := range main.Chats {
		if row.ChatID == ack.ChatID {
			t.Fatal("archived chat is still in the main list")
		}
	}
	archived := chatList(t, alice, 4, wire.ChatListBody{Limit: 10, IncludeArchived: true})
	if row := findChat(t, archived, ack.ChatID); !row.Archived {
		t.Fatal("the archived pile does not mark the row archived")
	}

	// Bob still sees it: archiving is Alice's opinion about a shared chat.
	bobList := chatList(t, bob, 5, wire.ChatListBody{Limit: 10})
	if row := findChat(t, bobList, ack.ChatID); row.Archived {
		t.Fatal("Alice's archive flag leaked onto Bob's row")
	}
}

// TestChatListCarriesPreviewAndUnread pins the fields that let a client draw the
// list without a follow-up call per chat — the N+1 that used to live on the client.
func TestChatListCarriesPreviewAndUnread(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "previewalice", "secret123")
	bob := connect(t, addr, "previewbob", "secret123")

	bob.send(t, wire.MsgSend, 1, wire.SendBody{
		ChatID: "@previewalice", DedupKey: "b1", Text: "first",
	})
	var ack wire.SendAckBody
	if err := wire.Unmarshal(bob.readUntil(t, wire.MsgSendAck).Body, &ack); err != nil {
		t.Fatal(err)
	}
	bob.send(t, wire.MsgSend, 2, wire.SendBody{ChatID: ack.ChatID, DedupKey: "b2", Text: "second"})
	_ = bob.readUntil(t, wire.MsgSendAck)

	list := chatList(t, alice, 3, wire.ChatListBody{Limit: 10})
	row := findChat(t, list, ack.ChatID)
	if row.LastMessage == nil {
		t.Fatal("no last-message preview: the client would have to call HISTORY per chat")
	}
	if row.LastMessage.Text != "second" {
		t.Fatalf("preview is %q, want the newest message", row.LastMessage.Text)
	}
	if row.UnreadCount != 2 {
		t.Fatalf("unread = %d, want 2", row.UnreadCount)
	}
	if row.LastActivityAt == 0 {
		t.Fatal("no activity timestamp: the list has no sort key and no cursor")
	}

	// Reading up to the first message leaves exactly one unread. Polled because the
	// read cursor is written on the server's own schedule, not the client's.
	alice.send(t, wire.MsgRead, 4, wire.ReadBody{ChatID: ack.ChatID, UpToChatSeq: 1})
	deadline := time.Now().Add(readTimeout)
	for time.Now().Before(deadline) {
		row = findChat(t, chatList(t, alice, 5, wire.ChatListBody{Limit: 10}), ack.ChatID)
		if row.UnreadCount == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("unread = %d after reading one of two, want 1", row.UnreadCount)
}
