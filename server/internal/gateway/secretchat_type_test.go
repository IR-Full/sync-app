package gateway_test

import (
	"testing"

	"github.com/SyncApp-chat/SyncApp/pkg/wire"
)

/*
A secret chat behaving like a chat.

The change these tests pin is not a new feature but a change of KIND: an
end-to-end conversation used to exist only as a relay with no chat row, so it had
no entry in the chat list, no title, no unread count and no settings — and every
client put it in a modal window beside the product, because that is what the data
model said it was.

So the assertions are mostly "the ordinary screens work": it is created through
CHAT_CREATE, it shows up in CHAT_LIST, it takes flags. The other half is the set of
refusals, because several server features assume they can read a chat's content and
each one would silently break the guarantee.
*/

// newSecretChat creates one with a peer and returns its id.
func newSecretChat(t *testing.T, c *testClient, reqID uint64, peerHandle string) string {
	t.Helper()
	c.send(t, wire.MsgChatCreate, reqID, wire.ChatCreateBody{
		Type: "secret", Members: []string{"@" + peerHandle},
	})
	e := c.readUntil(t, wire.MsgChatInfo)
	var ci wire.ChatInfoBody
	if err := wire.Unmarshal(e.Body, &ci); err != nil {
		t.Fatalf("decode CHAT_INFO: %v", err)
	}
	if ci.Type != "secret" {
		t.Fatalf("created a %q chat, want secret", ci.Type)
	}
	return ci.ChatID
}

// TestSecretChatIsCreatedLikeAnyOtherChat is the headline: CHAT_CREATE, not a
// separate call, which is what makes it a chat type rather than a side channel.
func TestSecretChatIsCreatedLikeAnyOtherChat(t *testing.T) {
	addr := startGateway(t)
	alice := connectWithCaps(t, addr, "sctalice", "secret123", secretPeer)
	bob := connectWithCaps(t, addr, "sctbob", "secret123", secretPeer)

	chatID := newSecretChat(t, alice, 1, "sctbob")

	// It is in the chat list — the thing it never had before.
	list := chatList(t, alice, 2, wire.ChatListBody{Limit: 10})
	row := findChat(t, list, chatID)
	if row.Type != "secret" {
		t.Fatalf("the list reports type %q", row.Type)
	}
	if row.PeerID != bob.userID {
		t.Fatalf("peer = %q, want Bob; a 1:1 chat has no title, so the row cannot be named without it", row.PeerID)
	}

	// And Bob sees it too: a chat is a shared object, not one side's view.
	bobList := chatList(t, bob, 3, wire.ChatListBody{Limit: 10})
	bobRow := findChat(t, bobList, chatID)
	if bobRow.PeerID != alice.userID {
		t.Fatalf("Bob's peer = %q, want Alice", bobRow.PeerID)
	}
}

// TestSecretChatIsCanonicalPerPair: asking twice returns the same chat, which is
// what lets a client call CHAT_CREATE whenever the user taps "secret chat" without
// first checking whether one exists. A second row would appear in the list with its
// own badge and its own history, and nothing could explain which was which.
func TestSecretChatIsCanonicalPerPair(t *testing.T) {
	addr := startGateway(t)
	alice := connectWithCaps(t, addr, "canonalice", "secret123", secretPeer)
	_ = connectWithCaps(t, addr, "canonbob", "secret123", secretPeer)

	first := newSecretChat(t, alice, 1, "canonbob")
	second := newSecretChat(t, alice, 2, "canonbob")
	if first != second {
		t.Fatalf("two secret chats for one pair: %s and %s", first, second)
	}
	list := chatList(t, alice, 3, wire.ChatListBody{Limit: 10})
	n := 0
	for _, row := range list.Chats {
		if row.Type == "secret" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d secret chats in the list, want 1", n)
	}
}

// TestSecretChatCoexistsWithTheDirectChat: both must be reachable, because
// choosing the secret one is the whole point of having it. Keying the canonical
// index on the pair alone would have made the second collide with the first.
func TestSecretChatCoexistsWithTheDirectChat(t *testing.T) {
	addr := startGateway(t)
	alice := connectWithCaps(t, addr, "coexalice", "secret123", secretPeer)
	_ = connectWithCaps(t, addr, "coexbob", "secret123", secretPeer)

	// An ordinary chat first.
	alice.send(t, wire.MsgSend, 1, wire.SendBody{ChatID: "@coexbob", DedupKey: "d1", Text: "hi"})
	var ack wire.SendAckBody
	if err := wire.Unmarshal(alice.readUntil(t, wire.MsgSendAck).Body, &ack); err != nil {
		t.Fatal(err)
	}
	secretID := newSecretChat(t, alice, 2, "coexbob")

	if secretID == ack.ChatID {
		t.Fatal("the secret chat reused the direct chat row")
	}
	list := chatList(t, alice, 3, wire.ChatListBody{Limit: 10})
	direct := findChat(t, list, ack.ChatID)
	secret := findChat(t, list, secretID)
	if direct.Type != "direct" || secret.Type != "secret" {
		t.Fatalf("types are %q and %q", direct.Type, secret.Type)
	}
}

// TestSecretChatTakesFlagsLikeAnyChat: mute, pin and archive are the settings a
// chat has, and a secret chat has them for the same reason it has a list row — it
// is a chat.
func TestSecretChatTakesFlagsLikeAnyChat(t *testing.T) {
	addr := startGateway(t)
	alice := connectWithCaps(t, addr, "flagsctalice", "secret123", secretPeer)
	_ = connectWithCaps(t, addr, "flagsctbob", "secret123", secretPeer)
	chatID := newSecretChat(t, alice, 1, "flagsctbob")

	if got := setFlags(t, alice, 2, wire.ChatFlagsBody{ChatID: chatID, Pinned: true}); !got.Pinned {
		t.Fatal("a secret chat could not be pinned")
	}
	list := chatList(t, alice, 3, wire.ChatListBody{Limit: 10})
	if row := findChat(t, list, chatID); !row.Pinned {
		t.Fatal("the pin did not survive to the list")
	}
}

// TestSecretChatRejectsBadMemberCounts. Not "at most one": a secret chat with
// nobody has no session to run, and one with several has no session they all
// share. Both are the same mistake.
func TestSecretChatRejectsBadMemberCounts(t *testing.T) {
	addr := startGateway(t)
	alice := connectWithCaps(t, addr, "membsctalice", "secret123", secretPeer)
	_ = connectWithCaps(t, addr, "membsctbob", "secret123", secretPeer)
	_ = connectWithCaps(t, addr, "membsctcarol", "secret123", secretPeer)

	for name, members := range map[string][]string{
		"none": {},
		"two":  {"@membsctbob", "@membsctcarol"},
	} {
		alice.send(t, wire.MsgChatCreate, 1, wire.ChatCreateBody{Type: "secret", Members: members})
		e := alice.read(t)
		if e.Type != wire.MsgError {
			t.Fatalf("%s members: created a chat instead of refusing (got %s)", name, e.Type)
		}
	}
}

// TestSecretChatRefusesAChatWithYourself: there is no second party to run a
// ratchet against, so there is no session and nothing to encrypt to. "Saved
// messages" is an ordinary self-chat and stays one.
func TestSecretChatRefusesAChatWithYourself(t *testing.T) {
	addr := startGateway(t)
	alice := connectWithCaps(t, addr, "selfsctalice", "secret123", secretPeer)

	alice.send(t, wire.MsgChatCreate, 1, wire.ChatCreateBody{
		Type: "secret", Members: []string{"@selfsctalice"},
	})
	if e := alice.read(t); e.Type != wire.MsgError {
		t.Fatalf("a secret chat with oneself was created (got %s)", e.Type)
	}
}

// TestCloudSendIntoASecretChatIsRefused is the most important refusal: it would
// STORE the plaintext, which is the one thing the chat exists to prevent.
func TestCloudSendIntoASecretChatIsRefused(t *testing.T) {
	addr := startGateway(t)
	alice := connectWithCaps(t, addr, "sendsctalice", "secret123", secretPeer)
	_ = connectWithCaps(t, addr, "sendsctbob", "secret123", secretPeer)
	chatID := newSecretChat(t, alice, 1, "sendsctbob")

	alice.send(t, wire.MsgSend, 2, wire.SendBody{
		ChatID: chatID, DedupKey: "leak", Text: "this must never be stored",
	})
	e := alice.read(t)
	if e.Type == wire.MsgSendAck {
		t.Fatal("a cloud send into a secret chat was accepted; the plaintext is now in the message log")
	}
	if e.Type != wire.MsgError {
		t.Fatalf("want an error, got %s", e.Type)
	}
}

// TestHistoryOfASecretChatIsRefusedRatherThanEmpty. An empty page would be
// indistinguishable from a quiet chat, so a client would draw "no messages" over a
// conversation it holds locally. Saying so explicitly is what lets it use its own
// store.
func TestHistoryOfASecretChatIsRefusedRatherThanEmpty(t *testing.T) {
	addr := startGateway(t)
	alice := connectWithCaps(t, addr, "histsctalice", "secret123", secretPeer)
	_ = connectWithCaps(t, addr, "histsctbob", "secret123", secretPeer)
	chatID := newSecretChat(t, alice, 1, "histsctbob")

	alice.send(t, wire.MsgHistory, 2, wire.HistoryBody{ChatID: chatID, Limit: 10})
	e := alice.read(t)
	if e.Type == wire.MsgHistoryOK || e.Type == wire.MsgHistoryPage {
		t.Fatal("history returned a page for a secret chat; an empty one reads as a quiet chat")
	}
	if e.Type != wire.MsgError {
		t.Fatalf("want an error, got %s", e.Type)
	}
}

// TestForwardOutOfASecretChatIsRefused: it would copy content into a chat where it
// IS stored, silently undoing the choice the user made for that message.
func TestForwardOutOfASecretChatIsRefused(t *testing.T) {
	addr := startGateway(t)
	alice := connectWithCaps(t, addr, "fwdsctalice", "secret123", secretPeer)
	_ = connectWithCaps(t, addr, "fwdsctbob", "secret123", secretPeer)
	_ = connectWithCaps(t, addr, "fwdsctcarol", "secret123", secretPeer)
	secretID := newSecretChat(t, alice, 1, "fwdsctbob")

	// An ordinary chat to forward into.
	alice.send(t, wire.MsgSend, 2, wire.SendBody{ChatID: "@fwdsctcarol", DedupKey: "c1", Text: "hi"})
	var ack wire.SendAckBody
	if err := wire.Unmarshal(alice.readUntil(t, wire.MsgSendAck).Body, &ack); err != nil {
		t.Fatal(err)
	}

	alice.send(t, wire.MsgForward, 3, wire.ForwardBody{
		FromChatID: secretID, ToChatID: ack.ChatID, MessageID: "1", DedupKey: "f1",
	})
	e := alice.read(t)
	if e.Type == wire.MsgSendAck {
		t.Fatal("a message was forwarded out of a secret chat")
	}
	if e.Type != wire.MsgError {
		t.Fatalf("want an error, got %s", e.Type)
	}

	// And the other direction: forwarding INTO a secret chat is the send rule.
	alice.send(t, wire.MsgForward, 4, wire.ForwardBody{
		FromChatID: ack.ChatID, ToChatID: secretID, MessageID: ack.MessageID, DedupKey: "f2",
	})
	if e := alice.read(t); e.Type != wire.MsgError {
		t.Fatalf("a message was forwarded into a secret chat (got %s)", e.Type)
	}
}

// TestSecretChatRequiresAKeyDirectory: without one no client could start a session
// in the chat, so creating the row would produce a conversation that cannot carry
// a message. Refusing is honest; an unusable chat is not.
func TestSecretChatRequiresAKeyDirectory(t *testing.T) {
	addr := startGatewayWithoutKeyDir(t)
	alice := connectWithCaps(t, addr, "nokdalice", "secret123", secretPeer)
	_ = connectWithCaps(t, addr, "nokdbob", "secret123", secretPeer)

	alice.send(t, wire.MsgChatCreate, 1, wire.ChatCreateBody{
		Type: "secret", Members: []string{"@nokdbob"},
	})
	e := alice.read(t)
	if e.Type != wire.MsgError {
		t.Fatalf("a secret chat was created with no key directory (got %s)", e.Type)
	}
	var eb wire.ErrorBody
	_ = wire.Unmarshal(e.Body, &eb)
	if eb.Code != wire.ErrUnsupported {
		t.Errorf("error code = %d, want ErrUnsupported", eb.Code)
	}
}
