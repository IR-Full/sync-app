package gateway_test

import (
	"testing"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/gateway"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
)

/*
The regression suite for a search that could not find your own messages.

The old pipeline asked the index for the globally best `limit*5` matches and THEN
dropped the ones the caller was not a member of. On a system with more than a
handful of users that global page belongs to strangers, so a search for a common
word returned nothing — for messages the server had indexed and the caller had
written. Filtering after the limit is not a slower version of filtering before it;
it is a different, wrong answer.

The tests below go through the real gateway with two accounts, because that is the
only arrangement in which the bug appears at all: with one account in the whole
database, a global ranking and a scoped one agree.
*/

// searchFor polls the search endpoint until it returns something or the deadline
// passes. Polling is the honest synchronization: indexing is asynchronous, driven
// by the outbox relay, so a fixed sleep would report machine load rather than
// correctness.
func searchFor(t *testing.T, c *testClient, reqID uint64, body wire.SearchBody) []wire.SearchHit {
	t.Helper()
	deadline := time.Now().Add(readTimeout)
	for time.Now().Before(deadline) {
		c.send(t, wire.MsgSearch, reqID, body)
		res := c.readUntil(t, wire.MsgSearchResults)
		var rb wire.SearchResultsBody
		if err := wire.Unmarshal(res.Body, &rb); err != nil {
			t.Fatalf("decode SEARCH_RESULTS: %v", err)
		}
		if len(rb.Hits) > 0 {
			return rb.Hits
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

// searchForN polls until at least want hits come back. searchFor alone is not
// enough where a test expects several: it returns on the FIRST hit, which can be
// the only one indexed so far.
func searchForN(t *testing.T, c *testClient, reqID uint64, body wire.SearchBody, want int) []wire.SearchHit {
	t.Helper()
	deadline := time.Now().Add(readTimeout)
	var last []wire.SearchHit
	for time.Now().Before(deadline) {
		last = searchOnce(t, c, reqID, body)
		if len(last) >= want {
			return last
		}
		time.Sleep(100 * time.Millisecond)
	}
	return last
}

// searchOnce asks once and returns whatever came back, including nothing. Used
// where the expected answer IS nothing, since polling for that would just burn
// the deadline.
func searchOnce(t *testing.T, c *testClient, reqID uint64, body wire.SearchBody) []wire.SearchHit {
	t.Helper()
	c.send(t, wire.MsgSearch, reqID, body)
	res := c.readUntil(t, wire.MsgSearchResults)
	var rb wire.SearchResultsBody
	if err := wire.Unmarshal(res.Body, &rb); err != nil {
		t.Fatalf("decode SEARCH_RESULTS: %v", err)
	}
	return rb.Hits
}

// TestSearchIsScopedPerAccount is the headline case: two accounts write the same
// word in chats the other cannot see, and each must find exactly their own.
func TestSearchIsScopedPerAccount(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "scopealice", "secret123")
	bob := connect(t, addr, "scopebob", "secret123")
	// A third party each of them talks to, so neither shares a chat with the other.
	_ = connect(t, addr, "scopecarol", "secret123")
	_ = connect(t, addr, "scopedave", "secret123")

	aliceChat := directChat(t, alice, "scopecarol")
	bobChat := directChat(t, bob, "scopedave")

	alice.send(t, wire.MsgSend, 10, wire.SendBody{
		ChatID: aliceChat, Text: "standup moved to friday", DedupKey: "a1",
	})
	_ = alice.readUntil(t, wire.MsgSendAck)
	bob.send(t, wire.MsgSend, 11, wire.SendBody{
		ChatID: bobChat, Text: "standup moved to monday", DedupKey: "b1",
	})
	_ = bob.readUntil(t, wire.MsgSendAck)

	aliceHits := searchFor(t, alice, 20, wire.SearchBody{Query: "standup", Limit: 10})
	if len(aliceHits) != 1 {
		t.Fatalf("Alice got %d hits for her own message, want 1", len(aliceHits))
	}
	if aliceHits[0].ChatID != aliceChat {
		t.Fatalf("Alice's hit is in chat %s, want her own %s", aliceHits[0].ChatID, aliceChat)
	}
	if aliceHits[0].Text != "standup moved to friday" {
		t.Fatalf("Alice sees Bob's text: %q", aliceHits[0].Text)
	}

	bobHits := searchFor(t, bob, 21, wire.SearchBody{Query: "standup", Limit: 10})
	if len(bobHits) != 1 {
		t.Fatalf("Bob got %d hits, want 1", len(bobHits))
	}
	if bobHits[0].Text != "standup moved to monday" {
		t.Fatalf("Bob sees Alice's text: %q", bobHits[0].Text)
	}
}

// TestSearchFindsOwnMessageBehindManyStrangers reproduces the failure mode
// directly: the caller's single match is the OLDEST of many, so a global
// recency-ranked page of the configured size cannot contain it.
func TestSearchFindsOwnMessageBehindManyStrangers(t *testing.T) {
	// The flood budget is raised because this test has to WRITE more matches than
	// the old pipeline's over-fetch window (limit*5) in order to bury Alice's
	// message under them. That volume is the test's subject, not an attempt to
	// dodge the limiter — every other test here runs on the defaults.
	addr := startGateway(t, func(c *gateway.Config) {
		c.SendRate, c.SendBurst = 500, 500
		c.ReadRate, c.ReadBurst = 500, 500
	})
	alice := connect(t, addr, "buriedalice", "secret123")
	_ = connect(t, addr, "buriedcarol", "secret123") // the peer only needs to exist
	aliceChat := directChat(t, alice, "buriedcarol")

	// Alice writes first, so every stranger message below is newer than hers.
	alice.send(t, wire.MsgSend, 10, wire.SendBody{
		ChatID: aliceChat, Text: "quarterly budget review", DedupKey: "a1",
	})
	_ = alice.readUntil(t, wire.MsgSendAck)

	// Make sure hers is indexed before the noise, so "not found" later cannot be
	// blamed on indexing lag.
	if hits := searchFor(t, alice, 11, wire.SearchBody{Query: "budget", Limit: 10}); len(hits) != 1 {
		t.Fatalf("Alice's own message was not indexed: %d hits", len(hits))
	}

	// Now 60 newer matches from accounts Alice shares nothing with. The old
	// pipeline over-fetched limit*5 = 50 globally, so 60 is enough to push hers
	// out of the window entirely.
	bob := connect(t, addr, "buriedbob", "secret123")
	_ = connect(t, addr, "burieddave", "secret123")
	bobChat := directChat(t, bob, "burieddave")
	for i := 0; i < 60; i++ {
		bob.send(t, wire.MsgSend, uint64(100+i), wire.SendBody{
			ChatID: bobChat, Text: "quarterly budget review", DedupKey: "b" + itoaTest(i),
		})
		_ = bob.readUntil(t, wire.MsgSendAck)
	}
	// Wait until the noise is actually in the index, or the test proves nothing.
	if hits := searchFor(t, bob, 200, wire.SearchBody{Query: "budget", Limit: 10}); len(hits) == 0 {
		t.Fatal("the stranger messages never got indexed")
	}

	hits := searchFor(t, alice, 201, wire.SearchBody{Query: "budget", Limit: 10})
	if len(hits) != 1 {
		t.Fatalf("Alice got %d hits; her message is buried under strangers' matches, "+
			"which is exactly the bug: the scope must be applied before the limit", len(hits))
	}
	if hits[0].ChatID != aliceChat {
		t.Fatalf("Alice's hit is in someone else's chat: %s", hits[0].ChatID)
	}
}

// TestSearchInAChatRequiresMembership checks the narrowing filter is not mistaken
// for an authorization. A chat id is guessable, so "search in this chat" has to
// verify the caller is in it rather than just filter on it.
func TestSearchInAChatRequiresMembership(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "narrowalice", "secret123")
	bob := connect(t, addr, "narrowbob", "secret123")
	_ = connect(t, addr, "narrowdave", "secret123")
	bobChat := directChat(t, bob, "narrowdave")

	bob.send(t, wire.MsgSend, 10, wire.SendBody{
		ChatID: bobChat, Text: "confidential offsite plan", DedupKey: "b1",
	})
	_ = bob.readUntil(t, wire.MsgSendAck)
	if hits := searchFor(t, bob, 11, wire.SearchBody{Query: "offsite", Limit: 10}); len(hits) != 1 {
		t.Fatalf("Bob cannot find his own message: %d hits", len(hits))
	}

	// Alice names Bob's chat explicitly. She is not in it.
	hits := searchOnce(t, alice, 12, wire.SearchBody{
		Query: "offsite", Limit: 10, ChatID: bobChat,
	})
	if len(hits) != 0 {
		t.Fatalf("naming a chat the caller is not in returned %d hits", len(hits))
	}
}

// TestSearchBySenderNarrowsWithinTheCallersChats checks the sender filter composes
// with the scope instead of replacing it.
func TestSearchBySenderNarrowsWithinTheCallersChats(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "sendalice", "secret123")
	carol := connect(t, addr, "sendcarol", "secret123")
	chatID := directChat(t, alice, "sendcarol")

	alice.send(t, wire.MsgSend, 10, wire.SendBody{ChatID: chatID, Text: "lunch at one", DedupKey: "a1"})
	_ = alice.readUntil(t, wire.MsgSendAck)
	carol.send(t, wire.MsgSend, 11, wire.SendBody{ChatID: chatID, Text: "lunch at two", DedupKey: "c1"})
	_ = carol.readUntil(t, wire.MsgSendAck)

	// Both are visible without a filter.
	if hits := searchForN(t, alice, 12, wire.SearchBody{Query: "lunch", Limit: 10}, 2); len(hits) != 2 {
		t.Fatalf("unfiltered search got %d hits, want both", len(hits))
	}
	// Filtered to Carol: one.
	hits := searchFor(t, alice, 13, wire.SearchBody{Query: "lunch", Limit: 10, SenderID: carol.userID})
	if len(hits) != 1 {
		t.Fatalf("sender filter got %d hits, want 1", len(hits))
	}
	if hits[0].SenderID != carol.userID {
		t.Fatalf("sender filter returned a message from %s", hits[0].SenderID)
	}
}

// directChat opens (or finds) the 1:1 chat between the caller and a peer, and
// returns its id.
//
// It primes the chat with a message addressed by @handle, because that is the only
// way the protocol creates one — and reads the id off the SEND_ACK rather than
// guessing it. The priming text is chosen not to collide with any search term the
// tests use, so it never shows up as a hit.
func directChat(t *testing.T, c *testClient, peerUsername string) string {
	t.Helper()
	c.send(t, wire.MsgSend, 1, wire.SendBody{
		ChatID:   "@" + peerUsername,
		DedupKey: "prime-" + peerUsername,
		Text:     "zzprimingzz",
	})
	e := c.readUntil(t, wire.MsgSendAck)
	var ack wire.SendAckBody
	if err := wire.Unmarshal(e.Body, &ack); err != nil {
		t.Fatalf("decode SEND_ACK: %v", err)
	}
	if ack.ChatID == "" {
		t.Fatal("SEND_ACK carried no chat id")
	}
	return ack.ChatID
}
