package gateway_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/auth"
	"github.com/SyncApp-chat/SyncApp/internal/billing"
	"github.com/SyncApp-chat/SyncApp/internal/chat"
	"github.com/SyncApp-chat/SyncApp/internal/contact"
	"github.com/SyncApp-chat/SyncApp/internal/delivery"
	"github.com/SyncApp-chat/SyncApp/internal/fanout"
	"github.com/SyncApp-chat/SyncApp/internal/gateway"
	"github.com/SyncApp-chat/SyncApp/internal/keydir"
	"github.com/SyncApp-chat/SyncApp/internal/message"
	"github.com/SyncApp-chat/SyncApp/internal/outbox"
	"github.com/SyncApp-chat/SyncApp/internal/presence"
	"github.com/SyncApp-chat/SyncApp/internal/replay"
	"github.com/SyncApp-chat/SyncApp/internal/router"
	"github.com/SyncApp-chat/SyncApp/internal/search"
	"github.com/SyncApp-chat/SyncApp/internal/store/memory"
	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
	"github.com/SyncApp-chat/SyncApp/pkg/id"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
)

// startGateway wires an in-memory stack and serves it on an ephemeral TCP port.
// Optional opts tweak the gateway Config (e.g. short timers for reaper tests).
func startGateway(t *testing.T, opts ...func(*gateway.Config)) string {
	addr, _ := startGatewayWithPresence(t, opts...)
	return addr
}

// startGatewayWithPresence is startGateway plus the presence service, for tests
// that assert on presence state rather than on delivered frames.
func startGatewayWithPresence(t *testing.T, opts ...func(*gateway.Config)) (string, *presence.Service) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ids, _ := id.NewGenerator(1)
	st := memory.New().Stores()
	bus := eventbus.NewMemory()
	hub := delivery.NewHub()

	// Events now flow through the transactional outbox, so the relay must run for
	// fanout/search to receive them.
	relayCtx, relayCancel := context.WithCancel(context.Background())
	t.Cleanup(relayCancel)
	go outbox.New(st.Outbox, bus, log).Run(relayCtx)

	authSvc := auth.New(st.Users, st.Sessions, ids)
	chatSvc := chat.New(st.Chats, ids)
	msgSvc := message.New(st.Messages, st.Reads, chatSvc, bus, ids)
	presSvc := presence.New(presence.NewMemoryBackend(), bus, time.Minute)

	rtr := router.NewMemory()
	fan := fanout.New(bus, chatSvc, rtr, log)
	if err := fan.Start(); err != nil {
		t.Fatal(err)
	}
	searchSvc := search.New(search.NewMemoryBackend(), chatSvc, log)
	if err := searchSvc.Start(bus); err != nil {
		t.Fatal(err)
	}

	cfg := gateway.DefaultConfig()
	cfg.Heartbeat = time.Hour // don't ping during the test
	cfg.NodeID = "1"
	for _, o := range opts {
		o(&cfg)
	}
	gw := gateway.New(gateway.Services{
		Auth: authSvc, Chat: chatSvc, Msg: msgSvc, Broker: message.NewBroker(msgSvc, log), Presence: presSvc,
		Users: st.Users, Hub: hub, Search: searchSvc, KeyDir: keydir.NewMemory(),
		Bus: bus, Router: rtr, Replay: replay.NewMemory(),
		// The offline queue for secret chats. Wired here rather than only in the
		// tests that assert on it: without it SECRET_SEND silently reverts to the
		// relay-only behaviour this queue was added to fix, and every other secret
		// test would keep passing while the regression went unnoticed.
		SecretQ: st.SecretQ, IDs: ids,
		// Wired so the harness exercises the block checks. Without it every
		// contacts handler answers ErrUnsupported and the blocklist gates — which
		// guard `Contacts != nil` — are skipped, so a regression in them would pass
		// the suite unnoticed. With no blocks set it changes nothing else.
		Contacts: contact.New(st.Contacts, st.Users),
		// Billing WITHOUT an acquirer, which is the shape every self-hosted
		// deployment has: a BillingStore always exists (both backends implement
		// one), so the service is always constructed, while the payment provider is
		// the genuinely optional part.
		//
		// Leaving it nil here is what hid a real defect for a long time. The
		// entitlement gates asked `Billing == nil`, the harness made that true, and
		// the suite therefore only ever exercised a branch that no real deployment
		// reaches — while production fell to the FREE tier and locked secret chats
		// behind a purchase it could not take.
		Billing: billing.New(st.Billing, bus, ids, log),
	}, cfg, log)
	if err := gw.StartDelivery(); err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go gw.ServeTCP(ctx, ln)
	return ln.Addr().String(), presSvc
}

// startGatewayWithoutKeyDir is startGateway with NO prekey directory, for the
// tests that assert the server refuses secret chats it could not actually carry.
//
// A separate constructor rather than an option, because "there is nowhere to
// publish keys" is a whole deployment shape rather than a tweak: several handlers
// answer ErrUnsupported in it, and a test that means to exercise that should say
// so at the call site.
func startGatewayWithoutKeyDir(t *testing.T) string {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ids, _ := id.NewGenerator(1)
	st := memory.New().Stores()
	bus := eventbus.NewMemory()
	hub := delivery.NewHub()

	relayCtx, relayCancel := context.WithCancel(context.Background())
	t.Cleanup(relayCancel)
	go outbox.New(st.Outbox, bus, log).Run(relayCtx)

	authSvc := auth.New(st.Users, st.Sessions, ids)
	chatSvc := chat.New(st.Chats, ids)
	msgSvc := message.New(st.Messages, st.Reads, chatSvc, bus, ids)
	presSvc := presence.New(presence.NewMemoryBackend(), bus, time.Minute)
	rtr := router.NewMemory()

	cfg := gateway.DefaultConfig()
	cfg.Heartbeat = time.Hour
	cfg.NodeID = "1"
	gw := gateway.New(gateway.Services{
		Auth: authSvc, Chat: chatSvc, Msg: msgSvc, Broker: message.NewBroker(msgSvc, log),
		Presence: presSvc, Users: st.Users, Hub: hub,
		Bus: bus, Router: rtr, Replay: replay.NewMemory(),
		SecretQ: st.SecretQ, IDs: ids,
		// KeyDir deliberately absent.
	}, cfg, log)
	if err := gw.StartDelivery(); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go gw.ServeTCP(ctx, ln)
	return ln.Addr().String()
}

// startTwoNodes wires TWO gateway nodes sharing one bus, router, and stores (as
// separate pods would share Redis/NATS/Postgres). It returns both listen
// addresses. A message sent to a client on node 1 must reach a client on node 2
// purely via the router + bus — the cross-node delivery path.
func startTwoNodes(t *testing.T) (addr1, addr2 string) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ids, _ := id.NewGenerator(1)
	st := memory.New().Stores()
	bus := eventbus.NewMemory()
	rtr := router.NewMemory()

	relayCtx, relayCancel := context.WithCancel(context.Background())
	t.Cleanup(relayCancel)
	go outbox.New(st.Outbox, bus, log).Run(relayCtx)

	authSvc := auth.New(st.Users, st.Sessions, ids)
	chatSvc := chat.New(st.Chats, ids)
	msgSvc := message.New(st.Messages, st.Reads, chatSvc, bus, ids)
	presSvc := presence.New(presence.NewMemoryBackend(), bus, time.Minute)
	fan := fanout.New(bus, chatSvc, rtr, log)
	if err := fan.Start(); err != nil {
		t.Fatal(err)
	}

	newNode := func(nodeID string) string {
		cfg := gateway.DefaultConfig()
		cfg.Heartbeat = time.Hour
		cfg.NodeID = nodeID
		gw := gateway.New(gateway.Services{
			Auth: authSvc, Chat: chatSvc, Msg: msgSvc, Broker: message.NewBroker(msgSvc, log), Presence: presSvc,
			Users: st.Users, Hub: delivery.NewHub(), Bus: bus, Router: rtr, KeyDir: keydir.NewMemory(),
		}, cfg, log)
		if err := gw.StartDelivery(); err != nil {
			t.Fatal(err)
		}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		go gw.ServeTCP(ctx, ln)
		return ln.Addr().String()
	}
	return newNode("1"), newNode("2")
}

func TestCrossNodeDelivery(t *testing.T) {
	addr1, addr2 := startTwoNodes(t)
	alice := connect(t, addr1, "xalice", "secret123") // node 1
	bob := connect(t, addr2, "xbob", "secret123")     // node 2

	// Alice (node 1) sends to @xbob; Bob is connected only on node 2. Delivery
	// must cross nodes via the router + bus.
	alice.send(t, wire.MsgSend, 1, wire.SendBody{ChatID: "@xbob", DedupKey: "cn1", Text: "cross-node hi"})
	_ = alice.readUntil(t, wire.MsgSendAck)

	nw := bob.readUntil(t, wire.MsgNew)
	var nb wire.NewMessageBody
	_ = wire.Unmarshal(nw.Body, &nb)
	if nb.Text != "cross-node hi" {
		t.Fatalf("bob (node 2) got %q", nb.Text)
	}
	if nb.SenderID != alice.userID {
		t.Fatalf("sender mismatch")
	}
}

// testClient is a minimal protocol client for assertions.
type testClient struct {
	conn        *wire.Conn
	userID      string
	deviceID    string
	sessionID   string
	token       string
	resumeToken string
	seq         uint64
}

// connect registers a new account and returns an authed client.
func connect(t *testing.T, addr, user, pass string) *testClient {
	return connectAs(t, addr, user, pass, true)
}

// login authenticates an existing account (e.g. a second device).
func login(t *testing.T, addr, user, pass string) *testClient {
	return connectAs(t, addr, user, pass, false)
}

// connectWithDevice authenticates while ASSERTING a device id in HELLO (the
// client-controlled field).
func connectWithDevice(t *testing.T, addr, user, pass, deviceID string) *testClient {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	cl := &testClient{conn: wire.NewConn(wire.NewTCPTransport(c), false)}
	cl.send(t, wire.MsgHello, 0, wire.HelloBody{ClientVersion: "test", Platform: "cli", DeviceID: deviceID})
	if e := cl.read(t); e.Type != wire.MsgWelcome {
		t.Fatalf("want WELCOME got %s", e.Type)
	}
	cl.send(t, wire.MsgAuth, 1, wire.AuthBody{Username: user, Password: pass, Register: true})
	e := cl.read(t)
	if e.Type != wire.MsgAuthOK {
		t.Fatalf("want AUTH_OK got %s", e.Type)
	}
	var ok wire.AuthOKBody
	_ = wire.Unmarshal(e.Body, &ok)
	cl.userID, cl.deviceID = ok.UserID, ok.DeviceID
	return cl
}

// connectWithCaps registers while ADVERTISING capabilities in HELLO, so a test
// can exercise a negotiated branch rather than only the default one.
func connectWithCaps(t *testing.T, addr, user, pass string, caps wire.Cap) *testClient {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	cl := &testClient{conn: wire.NewConn(wire.NewTCPTransport(c), false)}
	cl.send(t, wire.MsgHello, 0, wire.HelloBody{ClientVersion: "test", Platform: "cli", Caps: caps})
	if e := cl.read(t); e.Type != wire.MsgWelcome {
		t.Fatalf("want WELCOME got %s", e.Type)
	}
	cl.send(t, wire.MsgAuth, 1, wire.AuthBody{Username: user, Password: pass, Register: true})
	e := cl.read(t)
	if e.Type != wire.MsgAuthOK {
		t.Fatalf("want AUTH_OK got %s", e.Type)
	}
	var ok wire.AuthOKBody
	_ = wire.Unmarshal(e.Body, &ok)
	cl.userID, cl.deviceID = ok.UserID, ok.DeviceID
	cl.sessionID, cl.token, cl.resumeToken = ok.SessionID, ok.Token, ok.ResumeToken
	return cl
}

// loginWithDeviceAndCaps authenticates an EXISTING account while both asserting
// a device id and advertising capabilities.
//
// Both at once, because the secret-chat queue needs both: the queue is addressed
// by (user, device), so a reconnecting client that does not reclaim its device id
// is addressed at nobody — and the durable half of the protocol is gated on
// CapSecretQueue, so a client that does not advertise it gets the old silence.
func loginWithDeviceAndCaps(t *testing.T, addr, user, pass, deviceID string, caps wire.Cap) *testClient {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	cl := &testClient{conn: wire.NewConn(wire.NewTCPTransport(c), false)}
	cl.send(t, wire.MsgHello, 0, wire.HelloBody{
		ClientVersion: "test", Platform: "cli", DeviceID: deviceID, Caps: caps,
	})
	if e := cl.read(t); e.Type != wire.MsgWelcome {
		t.Fatalf("want WELCOME got %s", e.Type)
	}
	cl.send(t, wire.MsgAuth, 1, wire.AuthBody{Username: user, Password: pass})
	e := cl.read(t)
	if e.Type != wire.MsgAuthOK {
		t.Fatalf("want AUTH_OK got %s", e.Type)
	}
	var ok wire.AuthOKBody
	_ = wire.Unmarshal(e.Body, &ok)
	cl.userID, cl.deviceID = ok.UserID, ok.DeviceID
	cl.sessionID, cl.token, cl.resumeToken = ok.SessionID, ok.Token, ok.ResumeToken
	if cl.deviceID != deviceID {
		t.Fatalf("device id was not honoured: asked for %q, got %q", deviceID, cl.deviceID)
	}
	return cl
}

func connectAs(t *testing.T, addr, user, pass string, register bool) *testClient {
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
	cl.send(t, wire.MsgAuth, 1, wire.AuthBody{Username: user, Password: pass, Register: register})
	e := cl.read(t)
	if e.Type != wire.MsgAuthOK {
		t.Fatalf("want AUTH_OK got %s", e.Type)
	}
	var ok wire.AuthOKBody
	_ = wire.Unmarshal(e.Body, &ok)
	cl.userID = ok.UserID
	cl.deviceID = ok.DeviceID
	cl.sessionID = ok.SessionID
	cl.token = ok.Token
	cl.resumeToken = ok.ResumeToken
	return cl
}

// authenticates reports whether a bearer token still opens a connection.
//
// This is the only honest way to test revocation: asserting that a store row has
// revoked_at set proves the write happened, not that anything checks it. A token
// that still authenticates after "log out" is exactly the bug this feature
// exists to fix, and only a real AUTH round trip can see it.
func authenticates(t *testing.T, addr, token string) bool {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	cl := &testClient{conn: wire.NewConn(wire.NewTCPTransport(c), false)}
	cl.send(t, wire.MsgHello, 0, wire.HelloBody{ClientVersion: "test", Platform: "cli"})
	if e := cl.read(t); e.Type != wire.MsgWelcome {
		t.Fatalf("want WELCOME got %s", e.Type)
	}
	cl.send(t, wire.MsgAuth, 1, wire.AuthBody{Token: token})

	// A rejected auth may answer ERROR or simply drop the connection, so a read
	// failure counts as "no" rather than as a test failure.
	type res struct {
		e   wire.Envelope
		err error
	}
	ch := make(chan res, 1)
	go func() {
		e, err := cl.conn.ReadEnvelope()
		ch <- res{e, err}
	}()
	select {
	case r := <-ch:
		return r.err == nil && r.e.Type == wire.MsgAuthOK
	case <-time.After(readTimeout):
		t.Fatal("auth probe timed out")
		return false
	}
}

func (c *testClient) send(t *testing.T, typ wire.MsgType, reqID uint64, body any) {
	t.Helper()
	c.seq++
	if err := c.conn.Send(typ, c.seq, 0, reqID, body); err != nil {
		t.Fatal(err)
	}
}

func (c *testClient) read(t *testing.T) wire.Envelope {
	t.Helper()
	type res struct {
		e   wire.Envelope
		err error
	}
	ch := make(chan res, 1)
	go func() {
		e, err := c.conn.ReadEnvelope()
		ch <- res{e, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("read: %v", r.err)
		}
		return r.e
	case <-time.After(readTimeout):
		t.Fatal("read timeout")
		return wire.Envelope{}
	}
}

// readTimeout only exists so a broken path fails instead of hanging: a working
// one answers in milliseconds. It is generous because the whole suite runs in
// parallel and CI adds the race detector on top — a deadline tight enough to
// catch a real hang is also tight enough to fail on a loaded machine, and a test
// that fails for being busy teaches everyone to ignore it.
const readTimeout = 15 * time.Second

// tryRead reads one frame within a short window, reporting whether anything
// arrived. Unlike read it does not fail the test on silence — callers use it to
// POLL for a state the server reaches asynchronously, instead of sleeping a
// guessed interval and hoping the machine is not busy.
func (c *testClient) tryRead(t *testing.T, within time.Duration) (wire.Envelope, bool) {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(within))
	defer func() { _ = c.conn.SetReadDeadline(time.Time{}) }()
	e, err := c.conn.ReadEnvelope()
	if err != nil {
		return wire.Envelope{}, false
	}
	return e, true
}

// countUntilQuiet counts frames of one type until the stream stops producing
// them. Unlike read/readUntil it must NOT fail on timeout — going quiet is the
// expected outcome when a flood has been throttled away.
func (c *testClient) countUntilQuiet(t *testing.T, want wire.MsgType, within time.Duration) int {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(within))
	defer func() { _ = c.conn.SetReadDeadline(time.Time{}) }()
	n := 0
	for {
		e, err := c.conn.ReadEnvelope()
		if err != nil {
			return n
		}
		if e.Type == want {
			n++
		}
	}
}

// readUntil reads until it sees the wanted type (skipping pings/acks), or fails.
func (c *testClient) readUntil(t *testing.T, want wire.MsgType) wire.Envelope {
	t.Helper()
	deadline := time.Now().Add(readTimeout)
	for time.Now().Before(deadline) {
		e := c.read(t)
		if e.Type == want {
			return e
		}
	}
	t.Fatalf("did not receive %s in time", want)
	return wire.Envelope{}
}

func TestEndToEndDirectMessage(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "alice", "secret123")
	bob := connect(t, addr, "bob", "secret123")

	// Alice sends to @bob; the server resolves/creates the direct chat.
	alice.send(t, wire.MsgSend, 10, wire.SendBody{
		ChatID: "@bob", DedupKey: "k1", Text: "hello bob",
	})

	// Alice gets a SendAck.
	ack := alice.readUntil(t, wire.MsgSendAck)
	var ab wire.SendAckBody
	_ = wire.Unmarshal(ack.Body, &ab)
	if ab.MessageID == "" || ab.ChatSeq != 1 {
		t.Fatalf("bad ack: %+v", ab)
	}

	// Bob receives the message via fanout.
	nw := bob.readUntil(t, wire.MsgNew)
	var nb wire.NewMessageBody
	_ = wire.Unmarshal(nw.Body, &nb)
	if nb.Text != "hello bob" {
		t.Fatalf("bob got %q", nb.Text)
	}
	if nb.SenderID != alice.userID {
		t.Fatalf("sender mismatch: %s vs %s", nb.SenderID, alice.userID)
	}
}

func TestIdempotentSend(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "alice2", "secret123")
	_ = connect(t, addr, "bob2", "secret123")

	// Same dedup key twice → second is a duplicate, same message id, no new seq.
	alice.send(t, wire.MsgSend, 1, wire.SendBody{ChatID: "@bob2", DedupKey: "dup", Text: "once"})
	a1 := alice.readUntil(t, wire.MsgSendAck)
	var b1 wire.SendAckBody
	_ = wire.Unmarshal(a1.Body, &b1)

	alice.send(t, wire.MsgSend, 2, wire.SendBody{ChatID: "@bob2", DedupKey: "dup", Text: "once"})
	a2 := alice.readUntil(t, wire.MsgSendAck)
	var b2 wire.SendAckBody
	_ = wire.Unmarshal(a2.Body, &b2)

	if b1.MessageID != b2.MessageID {
		t.Fatalf("dedup failed: %s != %s", b1.MessageID, b2.MessageID)
	}
	if !b2.Duplicate {
		t.Fatal("second send should be flagged duplicate")
	}
	if b1.ChatSeq != b2.ChatSeq {
		t.Fatalf("dedup consumed a seq: %d != %d", b1.ChatSeq, b2.ChatSeq)
	}
}

func TestSearchFindsMessage(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "alice3", "secret123")
	_ = connect(t, addr, "bob3", "secret123")

	alice.send(t, wire.MsgSend, 1, wire.SendBody{ChatID: "@bob3", DedupKey: "s1", Text: "lets get pizza tonight"})
	_ = alice.readUntil(t, wire.MsgSendAck)

	// The indexer consumes the event asynchronously; poll the search endpoint.
	deadline := time.Now().Add(readTimeout)
	for time.Now().Before(deadline) {
		alice.send(t, wire.MsgSearch, 2, wire.SearchBody{Query: "pizza", Limit: 10})
		res := alice.readUntil(t, wire.MsgSearchResults)
		var rb wire.SearchResultsBody
		_ = wire.Unmarshal(res.Body, &rb)
		if len(rb.Hits) > 0 {
			if rb.Hits[0].Text != "lets get pizza tonight" {
				t.Fatalf("unexpected hit: %+v", rb.Hits[0])
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("search did not find the indexed message")
}

// TestReaperIdleClose verifies the shared reaper (not a per-connection goroutine)
// closes a connection that stops sending past the idle timeout.
func TestReaperIdleClose(t *testing.T) {
	addr := startGateway(t, func(c *gateway.Config) {
		c.Heartbeat = 50 * time.Millisecond
		c.IdleTimeout = 150 * time.Millisecond
	})
	cl := connect(t, addr, "idler", "secret123")

	// Stop sending; the client never pongs, so the server sees no inbound and the
	// reaper must close it. Reading should eventually return an error.
	deadline := time.Now().Add(readTimeout)
	for time.Now().Before(deadline) {
		_ = cl.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		if _, err := cl.conn.ReadEnvelope(); err != nil {
			return // connection closed by the reaper — success
		}
	}
	t.Fatal("reaper did not idle-close the connection")
}

func TestServerSeqMonotonic(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "alice5", "secret123")
	bob := connect(t, addr, "bob5", "secret123")

	// Alice fires many sends quickly; the server responds with acks AND fans out
	// to Bob. On Bob's socket, every frame's Seq must be strictly increasing —
	// this is the invariant the single-writer refactor guarantees.
	const n = 30
	go func() {
		for i := 0; i < n; i++ {
			alice.send(t, wire.MsgSend, uint64(100+i), wire.SendBody{
				ChatID: "@bob5", DedupKey: randDedup(i), Text: "m",
			})
		}
	}()

	var last uint64
	got := 0
	deadline := time.Now().Add(5 * time.Second)
	for got < n && time.Now().Before(deadline) {
		e := bob.read(t)
		if e.Seq <= last {
			t.Fatalf("server Seq went backwards: %d after %d (frame %s)", e.Seq, last, e.Type)
		}
		last = e.Seq
		if e.Type == wire.MsgNew {
			got++
		}
	}
	if got < n {
		t.Fatalf("only received %d/%d messages", got, n)
	}
}

func randDedup(i int) string { return "seq-" + itoaTest(i) }
func itoaTest(i int) string {
	if i == 0 {
		return "0"
	}
	var b [12]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}

func TestMultiDeviceKeyFetchAll(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "alice6", "secret123")
	// Bob logs in on two devices (two connections), each publishing keys.
	bob1 := connect(t, addr, "bob6", "secret123")
	bob2 := login(t, addr, "bob6", "secret123") // second device: log in, don't re-register
	if bob1.deviceID == bob2.deviceID {
		t.Fatal("expected distinct device ids for the two connections")
	}
	bob1.send(t, wire.MsgKeyPublish, 5, validKeyPublish(t))
	bob2.send(t, wire.MsgKeyPublish, 5, validKeyPublish(t))
	// Publish ACKs with KEY_STATE, so both publishes can be waited on directly
	// instead of polling the fetch until the second device shows up.
	bob1.readUntil(t, wire.MsgKeyState)
	bob2.readUntil(t, wire.MsgKeyState)

	alice.send(t, wire.MsgKeyFetchAll, 6, wire.KeyFetchBody{UserID: bob1.userID})
	var kb wire.KeyBundlesBody
	_ = wire.Unmarshal(alice.readUntil(t, wire.MsgKeyBundles).Body, &kb)
	if len(kb.Bundles) != 2 {
		t.Fatalf("expected 2 device bundles, got %d", len(kb.Bundles))
	}
}

func TestChatExportAuthorization(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "alice7", "secret123")
	bob := connect(t, addr, "bob7", "secret123")

	// Alice starts the chat (she becomes owner) and sends two messages.
	alice.send(t, wire.MsgSend, 1, wire.SendBody{ChatID: "@bob7", DedupKey: "e1", Text: "one"})
	ack := alice.readUntil(t, wire.MsgSendAck)
	var ab wire.SendAckBody
	_ = wire.Unmarshal(ack.Body, &ab)
	chatID := ab.ChatID
	alice.send(t, wire.MsgSend, 2, wire.SendBody{ChatID: chatID, DedupKey: "e2", Text: "two"})
	_ = alice.readUntil(t, wire.MsgSendAck)

	// Owner (Alice) can export: the export streams as a header frame (members),
	// message pages, then a Done frame. Accumulate until Done.
	alice.send(t, wire.MsgChatExport, 3, wire.ChatExportBody{ChatID: chatID})
	var (
		members int
		msgs    []wire.NewMessageBody
	)
	for {
		ex := alice.readUntil(t, wire.MsgChatExportResult)
		var xb wire.ChatExportResultBody
		_ = wire.Unmarshal(ex.Body, &xb)
		if len(xb.Members) > 0 {
			members = len(xb.Members)
		}
		msgs = append(msgs, xb.Messages...)
		if xb.Done {
			break
		}
	}
	if len(msgs) != 2 || members != 2 {
		t.Fatalf("export: msgs=%d members=%d", len(msgs), members)
	}

	// Non-owner (Bob, a member but not owner) is denied.
	bob.send(t, wire.MsgChatExport, 1, wire.ChatExportBody{ChatID: chatID})
	er := bob.readUntil(t, wire.MsgError)
	var eb wire.ErrorBody
	_ = wire.Unmarshal(er.Body, &eb)
	if eb.Code != wire.ErrForbidden {
		t.Fatalf("expected forbidden, got %d", eb.Code)
	}
}

func TestResumeReplaysMissedFrames(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "ralice", "secret123")
	bob := connect(t, addr, "rbob", "secret123")

	// Bob sends 3 messages; the gateway delivers+buffers 3 NEW frames to Alice.
	for i := 0; i < 3; i++ {
		bob.send(t, wire.MsgSend, uint64(10+i), wire.SendBody{ChatID: "@ralice", DedupKey: randDedup(i), Text: "m"})
	}
	// Wait for all three to be delivered AND buffered by reading them: the frames
	// themselves are the signal that the work happened, which no sleep can be.
	// (Alice re-reads from the buffer after the resume below.)
	first := alice.readUntil(t, wire.MsgNew)
	_ = alice.readUntil(t, wire.MsgNew)
	_ = alice.readUntil(t, wire.MsgNew)
	lastAck := first.Seq
	_ = alice.conn.Close()

	// Alice reconnects and RESUMEs from lastAck; the gateway must replay the two
	// frames she missed (Seq > lastAck) from the session buffer.
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	a2 := &testClient{conn: wire.NewConn(wire.NewTCPTransport(c), false)}
	a2.send(t, wire.MsgHello, 0, wire.HelloBody{ClientVersion: "test", Platform: "cli"})
	if e := a2.read(t); e.Type != wire.MsgWelcome {
		t.Fatalf("want WELCOME got %s", e.Type)
	}
	a2.send(t, wire.MsgResume, 1, wire.ResumeBody{ResumeToken: alice.resumeToken, LastAckSeq: lastAck})

	replayed := 0
	for {
		e := a2.read(t)
		if e.Type == wire.MsgNew {
			if e.Seq <= lastAck {
				t.Fatalf("replayed frame seq %d not > lastAck %d", e.Seq, lastAck)
			}
			replayed++
		}
		if e.Type == wire.MsgResumeOK {
			break
		}
	}
	if replayed != 2 {
		t.Fatalf("expected 2 replayed frames, got %d", replayed)
	}
}

func TestSecretRelayIsOpaque(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "alice4", "secret123")
	bob := connect(t, addr, "bob4", "secret123")

	// Alice relays opaque ciphertext addressed to Bob's device. The server never
	// interprets it — it just stamps sender and forwards.
	alice.send(t, wire.MsgSecretSend, 1, wire.SecretMsgBody{
		ToUserID: bob.userID, ToDeviceID: bob.deviceID,
		RatchetHeader: "aGVhZGVy", Ciphertext: "Y2lwaGVydGV4dA==",
	})

	recv := bob.readUntil(t, wire.MsgSecretRecv)
	var sb wire.SecretMsgBody
	_ = wire.Unmarshal(recv.Body, &sb)
	if sb.Ciphertext != "Y2lwaGVydGV4dA==" {
		t.Fatalf("ciphertext altered: %q", sb.Ciphertext)
	}
	if sb.FromUserID != alice.userID || sb.FromDeviceID != alice.deviceID {
		t.Fatalf("sender not stamped: %+v", sb)
	}
}

// A device id is asserted by the client, and real ones are not snowflakes: the
// web client uses "web-<random>", the mobile ones a UUID. Validating the secret
// relay's recipient device as if it were numeric would reject every one of them
// — silently, since a dropped relay frame produces no error the sender sees.
func TestSecretRelayAcceptsClientAssertedDeviceIDs(t *testing.T) {
	addr := startGateway(t)
	alice := connectWithDevice(t, addr, "alice-dev", "secret123", "web-3f2a9c")
	bob := connectWithDevice(t, addr, "bob-dev", "secret123", "7f3e1b2a-0c4d-4e5f-8a9b-0c1d2e3f4a5b")

	alice.send(t, wire.MsgSecretSend, 1, wire.SecretMsgBody{
		ToUserID: bob.userID, ToDeviceID: bob.deviceID,
		RatchetHeader: "aGVhZGVy", Ciphertext: "Y2lwaGVydGV4dA==",
	})

	recv := bob.readUntil(t, wire.MsgSecretRecv)
	var sb wire.SecretMsgBody
	_ = wire.Unmarshal(recv.Body, &sb)
	if sb.Ciphertext != "Y2lwaGVydGV4dA==" {
		t.Fatalf("ciphertext altered: %q", sb.Ciphertext)
	}
	if sb.FromDeviceID != alice.deviceID {
		t.Fatalf("sender device not stamped: %+v", sb)
	}
}

// A blocked sender must not be able to reach the person who blocked them through
// the relay. Every other path to a user goes through resolveChat, which refuses;
// this one is addressed by user id and has no chat behind it.
func TestSecretRelayRefusesBlockedRecipient(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "alice-blk", "secret123")
	bob := connect(t, addr, "bob-blk", "secret123")

	// Bob blocks Alice.
	bob.send(t, wire.MsgBlock, 1, wire.BlockBody{Target: alice.userID, Blocked: true})
	bob.readUntil(t, wire.MsgContactList)

	alice.send(t, wire.MsgSecretSend, 2, wire.SecretMsgBody{
		ToUserID: bob.userID, ToDeviceID: bob.deviceID,
		RatchetHeader: "aGVhZGVy", Ciphertext: "Y2lwaGVydGV4dA==",
	})

	e := alice.readUntil(t, wire.MsgError)
	var errBody wire.ErrorBody
	_ = wire.Unmarshal(e.Body, &errBody)
	if errBody.Code != wire.ErrForbidden {
		t.Fatalf("blocked secret send: got code %d, want ErrForbidden", errBody.Code)
	}
}

// TestForwardAndTTLReachTheClient pins the two fields a client can only render
// if the SERVER puts them on the wire: forward provenance and the self-destruct
// deadline. Both are stored on the message, so store-level tests still pass
// when the delivery converters drop them — the loss shows up only here, and to
// the user as a forward with no origin and a message with no visible deadline.
func TestForwardAndTTLReachTheClient(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "fwdalice", "secret123")
	bob := connect(t, addr, "fwdbob", "secret123")

	alice.send(t, wire.MsgSend, 1, wire.SendBody{
		ChatID: "@fwdbob", DedupKey: "f1", Text: "burn me", TTLSeconds: 30,
	})
	ack := alice.readUntil(t, wire.MsgSendAck)
	var ab wire.SendAckBody
	_ = wire.Unmarshal(ack.Body, &ab)

	live := bob.readUntil(t, wire.MsgNew)
	var lb wire.NewMessageBody
	_ = wire.Unmarshal(live.Body, &lb)
	if lb.ExpiresAt == 0 {
		t.Fatal("self-destruct deadline missing from live delivery")
	}

	// Forwarding back into the same chat is enough to exercise provenance: the
	// copy must credit the original author and name the chat it came from.
	alice.send(t, wire.MsgForward, 2, wire.ForwardBody{
		FromChatID: ab.ChatID, MessageID: ab.MessageID, ToChatID: ab.ChatID, DedupKey: "f2",
	})
	fwd := bob.readUntil(t, wire.MsgNew)
	var fb wire.NewMessageBody
	_ = wire.Unmarshal(fwd.Body, &fb)
	if fb.Forward == nil {
		t.Fatal("forward provenance missing from live delivery")
	}
	if fb.Forward.MessageID != ab.MessageID || fb.Forward.SenderID != alice.userID || fb.Forward.ChatID != ab.ChatID {
		t.Fatalf("wrong provenance: %+v", fb.Forward)
	}
	if fb.ExpiresAt != 0 {
		t.Fatalf("the copy inherited the original's deadline: %d", fb.ExpiresAt)
	}

	// History renders the same shape as live delivery — a second converter that
	// has to carry the same fields.
	bob.send(t, wire.MsgHistory, 3, wire.HistoryBody{ChatID: ab.ChatID, Limit: 10})
	var sawTTL, sawForward bool
	for i := 0; i < 10; i++ {
		e := bob.read(t)
		if e.Type == wire.MsgHistoryOK {
			break
		}
		if e.Type != wire.MsgNew {
			continue
		}
		var hb wire.NewMessageBody
		_ = wire.Unmarshal(e.Body, &hb)
		sawTTL = sawTTL || hb.ExpiresAt > 0
		sawForward = sawForward || hb.Forward != nil
	}
	if !sawTTL || !sawForward {
		t.Fatalf("history dropped fields: ttl=%v forward=%v", sawTTL, sawForward)
	}
}

// TestTypingIsThrottled pins the cheapest-frame/most-expensive-delivery case: a
// typing indicator costs the sender nothing and costs the server one delivery
// per chat member, on every node holding one. Unthrottled it is an amplifier, so
// a burst must reach the chat as a couple of frames, not as all of them.
func TestTypingIsThrottled(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "typealice", "secret123")
	bob := connect(t, addr, "typebob", "secret123")

	// Open the direct chat with a real message first, so typing resolves a chat
	// that already exists (the flood must be cheap to reject, not to create).
	alice.send(t, wire.MsgSend, 1, wire.SendBody{ChatID: "@typebob", DedupKey: "t1", Text: "hi"})
	_ = bob.readUntil(t, wire.MsgNew)

	const flood = 20
	for i := 0; i < flood; i++ {
		alice.send(t, wire.MsgTyping, 0, wire.TypingBody{ChatID: "@typebob", Active: true})
	}

	got := bob.countUntilQuiet(t, wire.MsgTyping, 400*time.Millisecond)
	if got == 0 {
		t.Fatal("typing never arrived; the throttle swallowed the whole indicator")
	}
	if got > 3 { // per-chat burst is 2; the slack absorbs bucket refill during the read
		t.Fatalf("typing flood not throttled: %d of %d frames fanned out", got, flood)
	}
}

// TestPresenceOutlivesOneDevice pins presence as a property of the USER, not of
// a connection. Closing a desktop while a phone is still connected used to
// announce the user offline, and nothing ever corrected it — the next heartbeat
// restores the stored flag but publishes no transition, so contacts kept showing
// a lie. The second half proves the offline path still fires when the last
// device goes.
func TestPresenceOutlivesOneDevice(t *testing.T) {
	addr, pres := startGatewayWithPresence(t)
	phone := connect(t, addr, "twodev", "secret123")
	desktop := login(t, addr, "twodev", "secret123")
	if desktop.userID != phone.userID {
		t.Fatalf("expected one user on two devices, got %s and %s", phone.userID, desktop.userID)
	}

	_ = desktop.conn.Close()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		p, err := pres.Get(context.Background(), phone.userID)
		if err != nil {
			t.Fatal(err)
		}
		if !p.Online {
			t.Fatal("one device disconnecting marked the whole user offline")
		}
		time.Sleep(20 * time.Millisecond)
	}

	_ = phone.conn.Close()
	deadline = time.Now().Add(readTimeout)
	for time.Now().Before(deadline) {
		p, err := pres.Get(context.Background(), phone.userID)
		if err != nil {
			t.Fatal(err)
		}
		if !p.Online {
			return // last device gone → offline, as it should be
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("user stayed online after the last device disconnected")
}

// TestDeviceIDCannotBeSquatted pins device ids to their owner. The id is
// asserted by the client in HELLO, so without ownership scoping a second account
// could name someone else's device and rewrite that row — in particular clearing
// its push token, which silences the victim without touching their account. The
// squatter must simply be handed a different id.
func TestDeviceIDCannotBeSquatted(t *testing.T) {
	addr := startGateway(t)
	victim := connect(t, addr, "squatvictim", "secret123")
	if victim.deviceID == "" {
		t.Fatal("no device id assigned")
	}

	attacker := connectWithDevice(t, addr, "squatattacker", "secret123", victim.deviceID)
	if attacker.deviceID == victim.deviceID {
		t.Fatalf("attacker was given the victim's device id %s", victim.deviceID)
	}
	if attacker.userID == victim.userID {
		t.Fatal("test error: both clients are the same user")
	}

	// The victim's own session must still work — the row is theirs.
	victim.send(t, wire.MsgSend, 1, wire.SendBody{ChatID: "@squatattacker", DedupKey: "d1", Text: "still mine"})
	nw := attacker.readUntil(t, wire.MsgNew)
	var nb wire.NewMessageBody
	_ = wire.Unmarshal(nw.Body, &nb)
	if nb.Text != "still mine" {
		t.Fatalf("victim's session broke after the squat attempt: %+v", nb)
	}
}

// TestCreateGroupOverTheProtocol pins the gap that made every membership feature
// untestable from a client: groups and channels existed in the model but nothing
// could bring one into existence over the wire, so handles, invites and roles
// could only ever be exercised against a direct chat.
func TestCreateGroupOverTheProtocol(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "grpalice", "secret123")
	bob := connect(t, addr, "grpbob", "secret123")

	alice.send(t, wire.MsgChatCreate, 1, wire.ChatCreateBody{
		Type: "group", Title: "Release war room", Members: []string{"@grpbob"},
	})
	info := alice.readUntil(t, wire.MsgChatInfo)
	var ci wire.ChatInfoBody
	_ = wire.Unmarshal(info.Body, &ci)
	if ci.ChatID == "" || ci.Type != "group" || ci.Title != "Release war room" {
		t.Fatalf("bad chat info: %+v", ci)
	}
	if ci.OwnerID != alice.userID {
		t.Fatalf("creator is not the owner: %s vs %s", ci.OwnerID, alice.userID)
	}

	// The named member is really in it: a message reaches them without an invite.
	alice.send(t, wire.MsgSend, 2, wire.SendBody{ChatID: ci.ChatID, DedupKey: "g1", Text: "we ship friday"})
	nw := bob.readUntil(t, wire.MsgNew)
	var nb wire.NewMessageBody
	_ = wire.Unmarshal(nw.Body, &nb)
	if nb.Text != "we ship friday" || nb.ChatID != ci.ChatID {
		t.Fatalf("member did not receive the group message: %+v", nb)
	}

	// A member who was never named is not in it, and cannot post.
	carol := connect(t, addr, "grpcarol", "secret123")
	carol.send(t, wire.MsgSend, 1, wire.SendBody{ChatID: ci.ChatID, DedupKey: "c1", Text: "hello?"})
	er := carol.readUntil(t, wire.MsgError)
	var eb wire.ErrorBody
	_ = wire.Unmarshal(er.Body, &eb)
	if eb.Code != wire.ErrForbidden {
		t.Fatalf("outsider got %d, want forbidden", eb.Code)
	}

	// Unknown members are refused rather than silently dropped.
	alice.send(t, wire.MsgChatCreate, 3, wire.ChatCreateBody{
		Type: "group", Title: "ghosts", Members: []string{"@nobody-here"},
	})
	er2 := alice.readUntil(t, wire.MsgError)
	_ = wire.Unmarshal(er2.Body, &eb)
	if eb.Code != wire.ErrNotFound {
		t.Fatalf("unknown member got %d, want not-found", eb.Code)
	}
}

// TestExpensiveActionsAreLimitedPerUser pins the difference between limiting a
// socket and limiting an account. The per-connection flood bucket is trivially
// sidestepped by opening a second connection; the work these actions cause —
// building an export, running a search, minting an upload ticket — is paid by
// the server once per request no matter which socket asked.
func TestExpensiveActionsAreLimitedPerUser(t *testing.T) {
	addr := startGateway(t)
	first := connect(t, addr, "limituser", "secret123")
	second := login(t, addr, "limituser", "secret123") // same account, new socket

	// Spend the account's search budget on the first connection.
	spent := false
	for i := 0; i < 40; i++ {
		first.send(t, wire.MsgSearch, uint64(i+1), wire.SearchBody{Query: "hello", Limit: 5})
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !spent {
		e := first.read(t)
		if e.Type == wire.MsgError {
			var eb wire.ErrorBody
			_ = wire.Unmarshal(e.Body, &eb)
			if eb.Code == wire.ErrRateLimited {
				spent = true
			}
		}
	}
	if !spent {
		t.Fatal("the per-user search budget was never exhausted")
	}

	// The SECOND connection of the same user must inherit the exhausted budget.
	second.send(t, wire.MsgSearch, 1, wire.SearchBody{Query: "hello", Limit: 5})
	e := second.readUntil(t, wire.MsgError)
	var eb wire.ErrorBody
	_ = wire.Unmarshal(e.Body, &eb)
	if eb.Code != wire.ErrRateLimited {
		t.Fatalf("a second connection bought a fresh budget (got %d)", eb.Code)
	}
}

// A peer that negotiates CAP_BATCHING gets one page frame instead of a hundred
// NEW frames plus a terminator. The capability bit was declared and unused until
// this existed, so the negotiation is what keeps it backward compatible: a
// client that does not ask still gets the stream.
func TestHistoryBatchesForCapableClients(t *testing.T) {
	addr := startGateway(t)
	alice := connectWithCaps(t, addr, "batchalice", "secret123", wire.CapBatching)
	bob := connect(t, addr, "batchbob", "secret123")

	const n = 5
	for i := 0; i < n; i++ {
		alice.send(t, wire.MsgSend, uint64(10+i), wire.SendBody{
			ChatID: "@batchbob", DedupKey: "batch" + itoaTest(i), Text: "m" + itoaTest(i),
		})
		alice.readUntil(t, wire.MsgSendAck)
	}

	alice.send(t, wire.MsgHistory, 100, wire.HistoryBody{ChatID: "@batchbob", Limit: 50})
	e := alice.readUntil(t, wire.MsgHistoryPage)

	var page wire.HistoryPageBody
	if err := wire.Unmarshal(e.Body, &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if len(page.Messages) != n {
		t.Fatalf("page carried %d messages, want %d", len(page.Messages), n)
	}
	if !page.Done {
		t.Error("a short page must be marked done")
	}
	// The RESOLVED id, not the "@handle" that was asked for: this is the one
	// frame that says which chat the page came from.
	if page.ChatID == "" || page.ChatID[0] == '@' {
		t.Fatalf("page chat id is %q, want a resolved id", page.ChatID)
	}
	_ = bob
}

// The default client advertises no batching, so it must still get the stream —
// otherwise adding the page would have broken every shipped mobile build.
func TestHistoryStreamsForClientsWithoutBatching(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "streamalice", "secret123")
	bob := connect(t, addr, "streambob", "secret123")

	alice.send(t, wire.MsgSend, 10, wire.SendBody{
		ChatID: "@streambob", DedupKey: "s1", Text: "only",
	})
	alice.readUntil(t, wire.MsgSendAck)

	alice.send(t, wire.MsgHistory, 100, wire.HistoryBody{ChatID: "@streambob", Limit: 50})

	// A NEW frame per message, then the terminator.
	nw := alice.readUntil(t, wire.MsgNew)
	var msg wire.NewMessageBody
	_ = wire.Unmarshal(nw.Body, &msg)
	if msg.Text != "only" {
		t.Fatalf("history streamed %q", msg.Text)
	}
	done := alice.readUntil(t, wire.MsgHistoryOK)
	var ok wire.HistoryOKBody
	_ = wire.Unmarshal(done.Body, &ok)
	if !ok.Done {
		t.Error("a short page must be marked done")
	}
	_ = bob
}
