package gateway_test

import (
	"testing"
	"time"

	"github.com/IR-Full/sync-app/server/pkg/wire"
)

/*
The regression suite for the bug that made secret chats non-functional.

SECRET_SEND was a pure relay: it asked the router which nodes held the recipient,
published to each, and discarded the count. Zero nodes — the recipient offline —
meant the ciphertext was dropped, nothing was stored, no push was queued, and the
sender received no reply, so every client drew "sent". Two people who were not
online simultaneously exchanged nothing.

These tests are written against the OBSERVABLE protocol rather than the store, so
they fail if any part of the chain regresses: the device-level routing lookup,
the queue write, the ack, the sync, or the scoping on the delete.
*/

// secretPeer is the capability a client needs to take part in the durable half
// of the protocol. A peer that does not advertise it gets the old silence.
const secretPeer = wire.CapSecretQueue

// awaitSecretAck reads until the SECRET_ACK for a send arrives, tolerating any
// unrelated frames the connection may be carrying.
func awaitSecretAck(t *testing.T, c *testClient) wire.SecretAckBody {
	t.Helper()
	deadline := time.Now().Add(readTimeout)
	for time.Now().Before(deadline) {
		e, ok := c.tryRead(t, 250*time.Millisecond)
		if !ok {
			continue
		}
		if e.Type != wire.MsgSecretAck {
			continue
		}
		var ack wire.SecretAckBody
		if err := wire.Unmarshal(e.Body, &ack); err != nil {
			t.Fatalf("decode SECRET_ACK: %v", err)
		}
		return ack
	}
	t.Fatal("no SECRET_ACK arrived: the relay answered nothing, which is the bug this test exists for")
	return wire.SecretAckBody{}
}

// drainSecretSync collects one sync page: the SECRET_RECV frames plus the
// SECRET_SYNCED terminator that ends it.
func drainSecretSync(t *testing.T, c *testClient) ([]wire.SecretMsgBody, wire.SecretSyncedBody) {
	t.Helper()
	var msgs []wire.SecretMsgBody
	deadline := time.Now().Add(readTimeout)
	for time.Now().Before(deadline) {
		e, ok := c.tryRead(t, 250*time.Millisecond)
		if !ok {
			continue
		}
		switch e.Type {
		case wire.MsgSecretRecv:
			var m wire.SecretMsgBody
			if err := wire.Unmarshal(e.Body, &m); err != nil {
				t.Fatalf("decode SECRET_RECV: %v", err)
			}
			msgs = append(msgs, m)
		case wire.MsgSecretSynced:
			var done wire.SecretSyncedBody
			if err := wire.Unmarshal(e.Body, &done); err != nil {
				t.Fatalf("decode SECRET_SYNCED: %v", err)
			}
			return msgs, done
		}
	}
	t.Fatal("SECRET_SYNC never terminated")
	return nil, wire.SecretSyncedBody{}
}

// TestSecretMessageToOfflineDeviceSurvives is the headline case: the recipient is
// not connected at all when the message is sent, comes back later, and gets it.
//
// Before the queue this test could not even be written — there was nothing to
// assert on, because the ciphertext was destroyed and the sender was told
// nothing either way.
func TestSecretMessageToOfflineDeviceSurvives(t *testing.T) {
	addr := startGateway(t)
	alice := connectWithCaps(t, addr, "sqalice", "secret123", secretPeer)
	bob := connectWithCaps(t, addr, "sqbob", "secret123", secretPeer)
	bobUser, bobDevice := bob.userID, bob.deviceID

	// Bob goes away. This is the whole scenario: not a slow client, not a
	// congested lane — simply not connected.
	_ = bob.conn.Close()
	waitForOffline(t, alice, bobUser, bobDevice)

	// Sent in the BINARY form, because this client advertises CapSecretQueue and
	// that is the form such a client uses. The bytes are deliberately not valid
	// base64 and not valid UTF-8 — a ratchet payload is opaque, and anything that
	// re-encodes it on the way through has to survive that.
	header := []byte("ratchet-header\x00\xff-bytes")
	cipher := []byte{0x00, 0x01, 0xfe, 0xff, 0x7f, 0x80}
	out := wire.SecretMsgBody{ToUserID: bobUser, ToDeviceID: bobDevice}
	wire.SetSecretPayloadBinary(&out, header, cipher)
	alice.send(t, wire.MsgSecretSend, 41, out)
	ack := awaitSecretAck(t, alice)
	if ack.Devices != 0 {
		t.Fatalf("recipient is offline but the relay claims %d nodes reached it", ack.Devices)
	}
	if !ack.Queued {
		t.Fatal("ciphertext for an offline device was not queued: this is the original bug")
	}

	// Bob comes back on the SAME device — a queue addressed by any other pair
	// would be addressed at nobody, since the ratchet session belongs to this
	// device alone.
	bob2 := loginWithDeviceAndCaps(t, addr, "sqbob", "secret123", bobDevice, secretPeer)
	bob2.send(t, wire.MsgSecretSync, 42, wire.SecretSyncBody{})
	all, done := drainSecretSync(t, bob2)
	msgs := realOnly(all)

	if len(msgs) != 1 {
		t.Fatalf("want exactly the one queued envelope, got %d", len(msgs))
	}
	got := msgs[0]
	gotHeader, gotCipher, ok := wire.SecretPayload(got)
	if !ok {
		t.Fatalf("the replayed envelope carries no payload: %+v", got)
	}
	if string(gotHeader) != string(header) || string(gotCipher) != string(cipher) {
		t.Fatalf("payload did not survive the queue: header=%q cipher=%q", gotHeader, gotCipher)
	}
	// And it came back in the BINARY form, because this peer negotiated it. The
	// whole point of the change is that a capable client stops paying 33% for base64.
	if len(got.Header) == 0 || got.RatchetHeader != "" {
		t.Fatalf("a CapSecretQueue peer was sent the legacy text form: %+v", got)
	}
	if got.FromUserID != alice.userID {
		t.Fatalf("sender stamp lost: want %s got %s", alice.userID, got.FromUserID)
	}
	if got.QueueID == "" {
		t.Fatal("a replayed envelope must carry a QueueID; without one the client cannot acknowledge it")
	}
	if !done.Done {
		t.Fatalf("sync did not report completion: %+v", done)
	}

	// Acknowledging drops it, and a second sync finds nothing. This is what stops
	// the queue from redelivering forever.
	bob2.send(t, wire.MsgSecretAcked, 43, wire.SecretAckedBody{IDs: []string{got.QueueID}})
	if _, ackDone := drainSecretSync(t, bob2); ackDone.Count != 1 {
		t.Fatalf("ack did not drop the envelope: %+v", ackDone)
	}
	bob2.send(t, wire.MsgSecretSync, 44, wire.SecretSyncBody{})
	msgs2, _ := drainSecretSync(t, bob2)
	if len(realOnly(msgs2)) != 0 {
		t.Fatalf("acknowledged envelope came back: %d still queued", len(realOnly(msgs2)))
	}
}

// TestSecretQueueSurvivesUntilAcknowledged proves the queue is at-least-once:
// collecting a message is not enough to drop it, because a connection that dies
// between the write and the client persisting it would otherwise lose exactly
// the message the queue exists to protect.
func TestSecretQueueSurvivesUntilAcknowledged(t *testing.T) {
	addr := startGateway(t)
	alice := connectWithCaps(t, addr, "sqa2", "secret123", secretPeer)
	bob := connectWithCaps(t, addr, "sqb2", "secret123", secretPeer)
	bobUser, bobDevice := bob.userID, bob.deviceID
	_ = bob.conn.Close()
	waitForOffline(t, alice, bobUser, bobDevice)

	alice.send(t, wire.MsgSecretSend, 51, wire.SecretMsgBody{
		ToUserID: bobUser, ToDeviceID: bobDevice,
		Header: []byte("h"), Cipher: []byte("c"),
	})
	if ack := awaitSecretAck(t, alice); !ack.Queued {
		t.Fatal("not queued")
	}

	// Collect, then drop the connection WITHOUT acknowledging — the crash case.
	bob2 := loginWithDeviceAndCaps(t, addr, "sqb2", "secret123", bobDevice, secretPeer)
	bob2.send(t, wire.MsgSecretSync, 52, wire.SecretSyncBody{})
	if msgs, _ := drainSecretSync(t, bob2); len(realOnly(msgs)) != 1 {
		t.Fatalf("want 1 on first collection, got %d", len(realOnly(msgs)))
	}
	_ = bob2.conn.Close()

	bob3 := loginWithDeviceAndCaps(t, addr, "sqb2", "secret123", bobDevice, secretPeer)
	bob3.send(t, wire.MsgSecretSync, 53, wire.SecretSyncBody{})
	msgs, _ := drainSecretSync(t, bob3)
	if len(realOnly(msgs)) != 1 {
		t.Fatalf("an unacknowledged envelope was lost on reconnect: got %d, want 1", len(realOnly(msgs)))
	}
}

// TestSecretAckIsScopedToTheOwningDevice is the authorization test.
//
// A queue id travels to the client inside the delivery, so a delete scoped only
// by id would let any authenticated account discard another's undelivered mail
// by naming ids it had seen — or guessed, since they are snowflakes.
func TestSecretAckIsScopedToTheOwningDevice(t *testing.T) {
	addr := startGateway(t)
	alice := connectWithCaps(t, addr, "sqa3", "secret123", secretPeer)
	bob := connectWithCaps(t, addr, "sqb3", "secret123", secretPeer)
	bobUser, bobDevice := bob.userID, bob.deviceID
	_ = bob.conn.Close()
	waitForOffline(t, alice, bobUser, bobDevice)

	alice.send(t, wire.MsgSecretSend, 61, wire.SecretMsgBody{
		ToUserID: bobUser, ToDeviceID: bobDevice,
		Header: []byte("h"), Cipher: []byte("c"),
	})
	if ack := awaitSecretAck(t, alice); !ack.Queued {
		t.Fatal("not queued")
	}

	bob2 := loginWithDeviceAndCaps(t, addr, "sqb3", "secret123", bobDevice, secretPeer)
	bob2.send(t, wire.MsgSecretSync, 62, wire.SecretSyncBody{})
	all, _ := drainSecretSync(t, bob2)
	msgs := realOnly(all)
	if len(msgs) != 1 {
		t.Fatalf("want 1 queued, got %d", len(msgs))
	}
	stolen := msgs[0].QueueID

	// Alice knows the id — she is the sender, and in a real deployment it is
	// enough to have seen one. She must still not be able to delete it.
	alice.send(t, wire.MsgSecretAcked, 63, wire.SecretAckedBody{IDs: []string{stolen}})
	if _, done := drainSecretSync(t, alice); done.Count != 0 {
		t.Fatalf("another account deleted %d of Bob's envelopes", done.Count)
	}

	bob2.send(t, wire.MsgSecretSync, 64, wire.SecretSyncBody{})
	if still, _ := drainSecretSync(t, bob2); len(realOnly(still)) != 1 {
		t.Fatal("Bob's envelope was deleted by an account that does not own it")
	}
}

// TestSecretSendToAnOfflineSiblingDeviceIsQueued is the device-level routing
// test, and it is the subtlest of the set.
//
// The relay used to ask "does this USER have a live connection anywhere", which
// answers yes when someone's phone is online — while the ciphertext was
// addressed to their laptop's ratchet session, which no other device can read.
// The message was reported delivered and then discarded.
func TestSecretSendToAnOfflineSiblingDeviceIsQueued(t *testing.T) {
	addr := startGateway(t)
	alice := connectWithCaps(t, addr, "sqa4", "secret123", secretPeer)

	// Bob has two devices. The laptop goes away; the phone stays connected, so
	// the user-level lookup keeps reporting him online for the whole test.
	laptop := connectWithCaps(t, addr, "sqb4", "secret123", secretPeer)
	laptopDevice := laptop.deviceID
	phone := loginWithDeviceAndCaps(t, addr, "sqb4", "secret123", "sqb4-phone", secretPeer)
	defer func() { _ = phone.conn.Close() }()
	_ = laptop.conn.Close()
	waitForOffline(t, alice, laptop.userID, laptopDevice)

	alice.send(t, wire.MsgSecretSend, 71, wire.SecretMsgBody{
		ToUserID: laptop.userID, ToDeviceID: laptopDevice,
		Header: []byte("h"), Cipher: []byte("c"),
	})
	ack := awaitSecretAck(t, alice)
	if ack.Devices != 0 {
		t.Fatalf("the addressed device is offline; reporting %d reached it is the user-level "+
			"lookup answering a device-level question", ack.Devices)
	}
	if !ack.Queued {
		t.Fatal("ciphertext for an offline device was dropped because a SIBLING device was online")
	}

	// And the phone must not receive it: it cannot decrypt a message addressed to
	// the laptop's session, so delivering it there would be noise at best.
	if e, ok := phone.tryRead(t, 300*time.Millisecond); ok && e.Type == wire.MsgSecretRecv {
		t.Fatal("ciphertext addressed to the laptop was delivered to the phone")
	}
}

// TestSecretSendToAnOnlineDeviceIsNotQueued guards the other direction: the
// queue must not turn every live message into a stored one, or the durability
// fix becomes a metadata leak that records conversations that were delivered
// instantly.
func TestSecretSendToAnOnlineDeviceIsNotQueued(t *testing.T) {
	addr := startGateway(t)
	alice := connectWithCaps(t, addr, "sqa5", "secret123", secretPeer)
	bob := connectWithCaps(t, addr, "sqb5", "secret123", secretPeer)

	alice.send(t, wire.MsgSecretSend, 81, wire.SecretMsgBody{
		ToUserID: bob.userID, ToDeviceID: bob.deviceID,
		Header: []byte("h"), Cipher: []byte("c"),
	})
	ack := awaitSecretAck(t, alice)
	if ack.Devices == 0 {
		t.Fatal("the recipient is connected but the relay found no node for the device")
	}
	if ack.Queued {
		t.Fatal("a delivered message was also queued: that is a stored record of a conversation nobody needed")
	}

	bob.send(t, wire.MsgSecretSync, 82, wire.SecretSyncBody{})
	if msgs, _ := drainSecretSync(t, bob); len(realOnly(msgs)) != 0 {
		t.Fatalf("live delivery left %d envelopes behind", len(realOnly(msgs)))
	}
}

// probeMarker tags the envelopes waitForOffline sends. They are indistinguishable
// from real traffic to the server — which is the point, since the probe has to
// exercise the same path — so the tests filter them out by payload rather than
// pretending they were never queued.
const probeMarker = "offline-probe"

// realOnly narrows a collected page to the envelopes that actually came OUT OF
// THE QUEUE and belong to the test.
//
// Two filters, and both are load-bearing. A live SECRET_RECV carries no QueueID
// — the field is set only on a replay — so counting one as queue contents would
// report a message that was delivered instantly as also stored, which is exactly
// the metadata leak TestSecretSendToAnOnlineDeviceIsNotQueued exists to catch.
// The probe filter is the mundane half: waitForOffline's probes are real
// envelopes by design, since the probe has to exercise the same path.
func realOnly(msgs []wire.SecretMsgBody) []wire.SecretMsgBody {
	out := msgs[:0:0]
	for _, m := range msgs {
		h, _, _ := SecretPayloadForTest(m)
		if m.QueueID != "" && string(h) != probeMarker {
			out = append(out, m)
		}
	}
	return out
}

// waitForOffline blocks until the gateway stops reporting a device as reachable.
//
// A closed socket is noticed by the SERVER's read loop, not by the client's
// Close, so asserting straight after a Close would race the unbind. Polling the
// relay's own answer is the honest synchronization here: it is precisely the
// fact under test, and sleeping a guessed interval would pass on an idle machine
// and fail on a busy one.
func waitForOffline(t *testing.T, prober *testClient, userID, deviceID string) {
	t.Helper()
	deadline := time.Now().Add(readTimeout)
	for i := 0; time.Now().Before(deadline); i++ {
		prober.send(t, wire.MsgSecretSend, uint64(9000+i), wire.SecretMsgBody{
			ToUserID: userID, ToDeviceID: deviceID,
			Header: []byte(probeMarker), Cipher: []byte(probeMarker),
		})
		if ack := awaitSecretAck(t, prober); ack.Devices == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("device %s never went offline", deviceID)
}

// SecretPayloadForTest reads either payload form. A thin alias so the filter above
// reads clearly; wire.SecretPayload is the real thing.
func SecretPayloadForTest(b wire.SecretMsgBody) (header, cipher []byte, ok bool) {
	return wire.SecretPayload(b)
}

// TestLegacyAndBinaryPeersInteroperate is the compatibility test for the payload
// encoding, and it is the case that would break silently without it.
//
// The two forms are not interchangeable on the wire: a client that reads only the
// text fields gets nothing from the binary ones, so a new client messaging an old
// one would deliver an empty message — no error, no log line, just a conversation
// where one side sees blanks. The relay therefore normalises to bytes between nodes
// and re-encodes per SOCKET, which is the only place that knows what the
// destination negotiated.
func TestLegacyAndBinaryPeersInteroperate(t *testing.T) {
	addr := startGateway(t)
	// A modern peer and a peer that predates the durable protocol entirely.
	modern := connectWithCaps(t, addr, "interopnew", "secret123", secretPeer)
	legacy := connect(t, addr, "interopold", "secret123")

	header := []byte(`{"ik":"AAAA","ek":"BBBB"}`)
	cipher := []byte{0x00, 0x01, 0xfe, 0xff}

	// Modern → legacy. Sent in binary, and it must ARRIVE in the text form.
	out := wire.SecretMsgBody{ToUserID: legacy.userID, ToDeviceID: legacy.deviceID}
	wire.SetSecretPayloadBinary(&out, header, cipher)
	modern.send(t, wire.MsgSecretSend, 10, out)

	got := legacy.readUntil(t, wire.MsgSecretRecv)
	var received wire.SecretMsgBody
	if err := wire.Unmarshal(got.Body, &received); err != nil {
		t.Fatal(err)
	}
	if received.RatchetHeader == "" || len(received.Header) != 0 {
		t.Fatalf("an old client was sent the binary form it cannot read: %+v", received)
	}
	gotH, gotC, ok := wire.SecretPayload(received)
	if !ok || string(gotH) != string(header) || string(gotC) != string(cipher) {
		t.Fatalf("the payload did not survive re-encoding: %q / %q (ok=%v)", gotH, gotC, ok)
	}

	// Legacy → modern. Sent as text, and it must arrive in binary.
	back := wire.SecretMsgBody{ToUserID: modern.userID, ToDeviceID: modern.deviceID}
	wire.SetSecretPayloadLegacy(&back, header, cipher)
	legacy.send(t, wire.MsgSecretSend, 11, back)

	got = modern.readUntil(t, wire.MsgSecretRecv)
	var received2 wire.SecretMsgBody
	if err := wire.Unmarshal(got.Body, &received2); err != nil {
		t.Fatal(err)
	}
	if len(received2.Header) == 0 || received2.RatchetHeader != "" {
		t.Fatalf("a modern client was sent the legacy form: %+v", received2)
	}
	gotH, gotC, ok = wire.SecretPayload(received2)
	if !ok || string(gotH) != string(header) || string(gotC) != string(cipher) {
		t.Fatalf("the payload did not survive re-encoding back: %q / %q (ok=%v)", gotH, gotC, ok)
	}
}

// TestLegacyPeerGetsNoSecretAck keeps the other half of the compatibility promise:
// a client written against the old silence has no handler for a frame arriving
// where none ever did, so SECRET_ACK is gated on the capability.
func TestLegacyPeerGetsNoSecretAck(t *testing.T) {
	addr := startGateway(t)
	legacy := connect(t, addr, "noackold", "secret123")
	peer := connect(t, addr, "noackpeer", "secret123")

	out := wire.SecretMsgBody{ToUserID: peer.userID, ToDeviceID: peer.deviceID}
	wire.SetSecretPayloadLegacy(&out, []byte(`{"n":1}`), []byte("ct"))
	legacy.send(t, wire.MsgSecretSend, 20, out)

	// The recipient gets the message; the sender gets nothing new.
	_ = peer.readUntil(t, wire.MsgSecretRecv)
	if e, ok := legacy.tryRead(t, 300*time.Millisecond); ok && e.Type == wire.MsgSecretAck {
		t.Fatal("a peer that did not negotiate CapSecretQueue was sent a SECRET_ACK")
	}
}
