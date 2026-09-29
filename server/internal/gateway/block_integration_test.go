package gateway_test

import (
	"testing"
	"time"

	"github.com/IR-Full/sync-app/server/pkg/wire"
)

// blockedPair sets up an existing conversation, then has bob block alice. It
// returns the direct chat id and the id of a message alice sent before the
// block — both of which alice legitimately knows.
func blockedPair(t *testing.T, addr, prefix string) (alice, bob *testClient, chatID, msgID string) {
	t.Helper()
	alice = connect(t, addr, prefix+"-a", "secret123")
	bob = connect(t, addr, prefix+"-b", "secret123")

	alice.send(t, wire.MsgSend, 1, wire.SendBody{ChatID: "@" + prefix + "-b", DedupKey: "pre", Text: "before"})
	var ack wire.SendAckBody
	_ = wire.Unmarshal(alice.readUntil(t, wire.MsgSendAck).Body, &ack)
	bob.readUntil(t, wire.MsgNew)

	bob.send(t, wire.MsgBlock, 2, wire.BlockBody{Target: alice.userID, Blocked: true})
	bob.readUntil(t, wire.MsgContactList)
	return alice, bob, ack.ChatID, ack.MessageID
}

func expectForbidden(t *testing.T, c *testClient) {
	t.Helper()
	var eb wire.ErrorBody
	_ = wire.Unmarshal(c.readUntil(t, wire.MsgError).Body, &eb)
	if eb.Code != wire.ErrForbidden {
		t.Fatalf("got error code %d (%q), want ErrForbidden", eb.Code, eb.Message)
	}
}

// The block used to be checked only when the target was written "@username".
// Every message alice had already exchanged with bob told her the chat id, and
// with it she kept writing — bob received it — and kept reading his replies.
func TestBlockHoldsWhenAddressedByChatID(t *testing.T) {
	addr := startGateway(t)

	t.Run("send", func(t *testing.T) {
		alice, bob, chatID, _ := blockedPair(t, addr, "blk-send")
		alice.send(t, wire.MsgSend, 3, wire.SendBody{ChatID: chatID, DedupKey: "post", Text: "after block"})
		expectForbidden(t, alice)
		if n := bob.countUntilQuiet(t, wire.MsgNew, 300*time.Millisecond); n != 0 {
			t.Fatalf("bob received %d message(s) from someone he blocked", n)
		}
	})

	t.Run("history", func(t *testing.T) {
		alice, _, chatID, _ := blockedPair(t, addr, "blk-hist")
		alice.send(t, wire.MsgHistory, 3, wire.HistoryBody{ChatID: chatID, Limit: 10})
		expectForbidden(t, alice)
	})

	t.Run("edit", func(t *testing.T) {
		// Editing rewrites what bob is shown: after a block it is writing to him.
		alice, _, chatID, msgID := blockedPair(t, addr, "blk-edit")
		alice.send(t, wire.MsgEdit, 3, wire.EditBody{ChatID: chatID, MessageID: msgID, Text: "sneaky"})
		expectForbidden(t, alice)
	})

	t.Run("the blocker is cut off too", func(t *testing.T) {
		// Bidirectional, like the "@username" path: bob cannot keep messaging
		// alice while she cannot answer.
		_, bob, chatID, _ := blockedPair(t, addr, "blk-rev")
		bob.send(t, wire.MsgSend, 3, wire.SendBody{ChatID: chatID, DedupKey: "rev", Text: "hi"})
		expectForbidden(t, bob)
	})
}

// A block is between two people. It must not keep either of them out of a group
// they both belong to.
func TestBlockDoesNotAffectSharedGroups(t *testing.T) {
	addr := startGateway(t)
	alice, bob, _, _ := blockedPair(t, addr, "blk-grp")

	bob.send(t, wire.MsgChatCreate, 3, wire.ChatCreateBody{Type: "group", Title: "both", Members: []string{alice.userID}})
	var info wire.ChatInfoBody
	_ = wire.Unmarshal(bob.readUntil(t, wire.MsgChatInfo).Body, &info)

	alice.send(t, wire.MsgSend, 4, wire.SendBody{ChatID: info.ChatID, DedupKey: "grp", Text: "hello group"})
	alice.readUntil(t, wire.MsgSendAck)
}
