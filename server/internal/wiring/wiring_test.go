package wiring

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/SyncApp-chat/SyncApp/internal/auth"
	"github.com/SyncApp-chat/SyncApp/internal/chat"
	"github.com/SyncApp-chat/SyncApp/internal/gateway"
	"github.com/SyncApp-chat/SyncApp/internal/message"
	"github.com/SyncApp-chat/SyncApp/internal/outbox"
	"github.com/SyncApp-chat/SyncApp/internal/platform"
	"github.com/SyncApp-chat/SyncApp/internal/presence"
	"github.com/SyncApp-chat/SyncApp/internal/replay"
	"github.com/SyncApp-chat/SyncApp/internal/router"
	"github.com/SyncApp-chat/SyncApp/internal/rpc"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/internal/store/memory"
	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
	"github.com/SyncApp-chat/SyncApp/pkg/id"
	"github.com/SyncApp-chat/SyncApp/pkg/totp"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
)

// optionalWithoutRedis lists the gateway.Services fields a process may leave
// nil, with the reason. Anything not listed must be set in BOTH topologies.
var optionalWithoutRedis = map[string]string{
	// nil means "no cross-node budget"; gateway.New substitutes a node-local one.
	"UserLimits": "cross-node limiter exists only with Redis",
}

// testBackends are the in-memory backends every daemon falls back to.
func testBackends(t *testing.T) *platform.Backends {
	t.Helper()
	ids, err := id.NewGenerator(7)
	if err != nil {
		t.Fatal(err)
	}
	st := memory.New().Stores()
	return &platform.Backends{
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		NodeID:       7,
		IDs:          ids,
		Stores:       st,
		Bus:          eventbus.NewMemory(),
		Presence:     presence.NewMemoryBackend(),
		Router:       router.NewMemory(),
		Replay:       replay.NewMemory(),
		MessageStore: st.Messages,
		MsgOutbox:    []store.OutboxStore{st.Outbox},
	}
}

func testConfig(t *testing.T) Config {
	t.Helper()
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	cfg.TCPAddr = "127.0.0.1:0"
	cfg.WSAddr = "127.0.0.1:0"
	cfg.MediaDir = t.TempDir()
	cfg.Gateway.Heartbeat = time.Hour
	return cfg
}

// unsetFields returns the names of nil fields in a gateway.Services.
func unsetFields(s gateway.Services) []string {
	var out []string
	v := reflect.ValueOf(s)
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.Interface, reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func:
			if f.IsNil() {
				out = append(out, v.Type().Field(i).Name)
			}
		}
	}
	return out
}

// TestEdgeServicesAreComplete holds both topologies to one definition of the
// gateway's dependencies. A field left nil makes the gateway answer "not
// supported" for a whole feature, with nothing failing to say so.
func TestEdgeServicesAreComplete(t *testing.T) {
	ctx := context.Background()
	lazy, err := grpc.NewClient("passthrough:///unused", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lazy.Close() })

	topologies := map[string]func(*platform.Backends, Config) (*Node, error){
		"monolith": func(b *platform.Backends, cfg Config) (*Node, error) { return Monolith(ctx, b, cfg) },
		"fleet": func(b *platform.Backends, cfg Config) (*Node, error) {
			return Fleet(ctx, b, cfg, FleetConns{Auth: lazy, Chat: lazy, Message: lazy, Presence: lazy, KeyDir: lazy})
		},
	}
	for name, build := range topologies {
		t.Run(name, func(t *testing.T) {
			node, err := build(testBackends(t), testConfig(t))
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range unsetFields(node.Edge.Services) {
				if _, ok := optionalWithoutRedis[f]; !ok {
					t.Errorf("gateway.Services.%s is nil in the %s topology", f, name)
				}
			}
		})
	}
}

func TestFromEnvReadsTheGatewaySettings(t *testing.T) {
	t.Setenv("SYNCAPP_SEND_RATE", "5")
	t.Setenv("SYNCAPP_MAX_CONNS_PER_IP", "12")
	t.Setenv("SYNCAPP_ACCEPT_RATE_PER_IP", "2.5")
	t.Setenv("SYNCAPP_TRUSTED_PROXIES", "10.0.0.0/8, 192.0.2.7")
	t.Setenv("SYNCAPP_ALLOWED_ORIGINS", "https://a.example,https://b.example")
	t.Setenv("SYNCAPP_ADMIN_USERS", "1,2")
	t.Setenv("SYNCAPP_MODERATOR_USERS", "3")
	t.Setenv("SYNCAPP_BANNED_TERMS", "x, y")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	g := cfg.GatewayConfig(42)
	if g.NodeID != "42" || g.SendRate != 5 || g.SendBurst != 10 || g.MaxConnsPerIP != 12 || g.AcceptRatePerIP != 2.5 {
		t.Fatalf("numeric settings not applied: %+v", g)
	}
	if !reflect.DeepEqual(g.TrustedProxies, []string{"10.0.0.0/8", "192.0.2.7"}) {
		t.Fatalf("trusted proxies: %q", g.TrustedProxies)
	}
	if len(g.AllowedOrigins) != 2 || len(g.AdminUsers) != 2 || len(g.ModeratorUsers) != 1 {
		t.Fatalf("lists not applied: %+v", g)
	}
	if !reflect.DeepEqual(cfg.BannedTerms, []string{"x", "y"}) {
		t.Fatalf("banned terms: %q", cfg.BannedTerms)
	}
}

// A malformed value used to be skipped in silence, leaving the default — for the
// per-IP cap, that default is "off".
func TestFromEnvRefusesMalformedValues(t *testing.T) {
	for name, value := range map[string]string{
		"SYNCAPP_MAX_CONNS_PER_IP":   "12x",
		"SYNCAPP_ACCEPT_RATE_PER_IP": "fast",
		"SYNCAPP_SEND_RATE":          "-1",
		"SYNCAPP_MEDIA_MAX_BYTES":    "4GB",
		"SYNCAPP_TRUSTED_PROXIES":    "10.0.0.0/8,ingress.local",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, value)
			if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("want an error naming %s, got %v", name, err)
			}
		})
	}
}

// Believing PROXY headers from anyone would let any client choose the address
// the per-IP guard charges.
func TestProxyProtocolNeedsTrustedProxies(t *testing.T) {
	t.Setenv("SYNCAPP_PROXY_PROTOCOL", "1")
	if _, err := FromEnv(); err == nil {
		t.Fatal("SYNCAPP_PROXY_PROTOCOL without SYNCAPP_TRUSTED_PROXIES must be refused")
	}
	t.Setenv("SYNCAPP_TRUSTED_PROXIES", "10.0.0.1")
	if _, err := FromEnv(); err != nil {
		t.Fatal(err)
	}
}

// authd once built its service without the second factor, so an account with
// TOTP enabled signed in on the password alone in the split deployment.
func TestNewAuthEnforcesTheSecondFactor(t *testing.T) {
	t.Setenv("SYNCAPP_TOTP_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	ctx := context.Background()
	b := testBackends(t)
	svc := NewAuth(b.Stores, b.IDs)

	_, u, err := svc.Register(ctx, "tfa", "secret123", "", "d1", "cli")
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := svc.BeginTOTP(ctx, u.ID, "SyncApp")
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.Code(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ConfirmTOTP(ctx, u.ID, code); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.LoginWithCode(ctx, "tfa", "secret123", "", "d2", "cli"); !errors.Is(err, auth.ErrTwoFactorRequired) {
		t.Fatalf("password alone must not sign in once TOTP is on, got %v", err)
	}
}

// startNode binds, starts and serves a node, returning the TCP and HTTP
// addresses.
func startNode(t *testing.T, node *Node, cfg Config, log *slog.Logger) (tcpAddr, httpAddr string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ln, err := Listen(cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	if err := node.Start(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Serve(ctx, cfg, node.Gateway, node.Edge, ln, log)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return ln.TCP.Addr().String(), ln.HTTP.Addr().String()
}

// TestMonolithServesTheProtocol boots cmd/server's assembly on random ports and
// runs the basic flow: register, send, deliver, and the HTTP surface.
func TestMonolithServesTheProtocol(t *testing.T) {
	b := testBackends(t)
	cfg := testConfig(t)
	node, err := Monolith(context.Background(), b, cfg)
	if err != nil {
		t.Fatal(err)
	}
	tcpAddr, httpAddr := startNode(t, node, cfg, b.Log)

	resp, err := http.Get("http://" + httpAddr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz: %d", resp.StatusCode)
	}

	alice := dial(t, tcpAddr, "monoalice", 0)
	bob := dial(t, tcpAddr, "monobob", 0)
	ack := alice.call(t, wire.MsgSend, wire.SendBody{ChatID: "@monobob", DedupKey: "m1", Text: "hi"}, wire.MsgSendAck)
	var ab wire.SendAckBody
	_ = wire.Unmarshal(ack.Body, &ab)
	if ab.MessageID == "" {
		t.Fatalf("no message id: %+v", ab)
	}
	nb := bob.until(t, wire.MsgNew)
	var got wire.NewMessageBody
	_ = wire.Unmarshal(nb.Body, &got)
	if got.Text != "hi" {
		t.Fatalf("delivered %+v", got)
	}
	exerciseEdgeFeatures(t, alice, ab)
}

// TestFleetServesEveryEdgeFeature boots the split deployment in-process: the
// domain daemons behind a gRPC server built from the same constructors they
// use, and gatewayd's assembly talking to them only through gRPC.
func TestFleetServesEveryEdgeFeature(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b := testBackends(t)
	cfg := testConfig(t)

	chatSvc := chat.New(b.Stores.Chats, b.IDs)
	msgSvc := message.New(b.MessageStore, b.Stores.Reads, chatSvc, b.Bus, b.IDs)
	for _, ob := range b.MsgOutbox {
		go outbox.New(ob, b.Bus, b.Log).Run(ctx)
	}
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	rpc.RegisterAuth(srv, NewAuth(b.Stores, b.IDs))
	rpc.RegisterChat(srv, chatSvc)
	rpc.RegisterMessage(srv, message.NewBroker(msgSvc, b.Log), msgSvc)
	rpc.RegisterPresence(srv, NewPresence(b))
	rpc.RegisterKeyDir(srv, NewKeyDir(b))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///fleet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	chats := rpc.NewChatClient(conn)
	fan := NewFanout(b, FanoutDeps{Chats: chats, Kinds: chats, Mute: chatSvc, Users: b.Stores.Users, Contacts: NewContacts(b.Stores)})
	if err := fan.Start(); err != nil {
		t.Fatal(err)
	}

	node, err := Fleet(ctx, b, cfg, FleetConns{Auth: conn, Chat: conn, Message: conn, Presence: conn, KeyDir: conn})
	if err != nil {
		t.Fatal(err)
	}
	tcpAddr, _ := startNode(t, node, cfg, b.Log)

	alice := dial(t, tcpAddr, "fleetalice", wire.CapSecretQueue)
	bob := dial(t, tcpAddr, "fleetbob", 0)
	ack := alice.call(t, wire.MsgSend, wire.SendBody{ChatID: "@fleetbob", DedupKey: "f1", Text: "hi"}, wire.MsgSendAck)
	var ab wire.SendAckBody
	_ = wire.Unmarshal(ack.Body, &ab)
	bob.until(t, wire.MsgNew)
	exerciseEdgeFeatures(t, alice, ab)

	// A secret message to a device that is not connected is held for it. Without
	// the queue store at the edge it was dropped, and the sender was told so only
	// by the absence of "queued".
	sack := alice.call(t, wire.MsgSecretSend, wire.SecretMsgBody{
		ToUserID: bob.userID, ToDeviceID: "bob-laptop",
		RatchetHeader: `{"dh":"x"}`, Ciphertext: base64.StdEncoding.EncodeToString([]byte("opaque")),
	}, wire.MsgSecretAck)
	var sb wire.SecretAckBody
	_ = wire.Unmarshal(sack.Body, &sb)
	if !sb.Queued {
		t.Fatalf("secret message to an offline device was not queued: %+v", sb)
	}
}

// exerciseEdgeFeatures runs the features served by edge-local services — the
// ones gatewayd used to leave unwired. Each must answer with its reply type,
// not an error.
func exerciseEdgeFeatures(t *testing.T, c *client, sent wire.SendAckBody) {
	t.Helper()
	pins := c.call(t, wire.MsgPin, wire.PinBody{ChatID: sent.ChatID, MessageID: sent.MessageID}, wire.MsgPinned)
	var pb wire.PinnedBody
	_ = wire.Unmarshal(pins.Body, &pb)
	if len(pb.Pins) != 1 {
		t.Fatalf("pin not recorded: %+v", pb)
	}

	sendAt := time.Now().Add(time.Hour).UnixMilli()
	c.call(t, wire.MsgSchedule, wire.ScheduleBody{ChatID: sent.ChatID, Text: "later", SendAt: sendAt}, wire.MsgScheduled)

	info := c.call(t, wire.MsgChatCreate, wire.ChatCreateBody{Type: "group", Title: "g"}, wire.MsgChatInfo)
	var ci wire.ChatInfoBody
	_ = wire.Unmarshal(info.Body, &ci)
	inv := c.call(t, wire.MsgInviteCreate, wire.InviteCreateBody{ChatID: ci.ChatID}, wire.MsgInvites)
	var ib wire.InvitesBody
	_ = wire.Unmarshal(inv.Body, &ib)
	if len(ib.Links) != 1 {
		t.Fatalf("invite link not created: %+v", ib)
	}
}

// client is a minimal protocol client for these tests.
type client struct {
	conn   *wire.Conn
	seq    uint64
	req    uint64
	userID string
}

const readTimeout = 15 * time.Second

func dial(t *testing.T, addr, user string, caps wire.Cap) *client {
	t.Helper()
	nc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nc.Close() })
	c := &client{conn: wire.NewConn(wire.NewTCPTransport(nc), false)}
	c.call(t, wire.MsgHello, wire.HelloBody{ClientVersion: "test", Platform: "cli", DeviceID: user + "-dev", Caps: caps}, wire.MsgWelcome)
	ok := c.call(t, wire.MsgAuth, wire.AuthBody{Username: user, Password: "secret123", Register: true}, wire.MsgAuthOK)
	var body wire.AuthOKBody
	_ = wire.Unmarshal(ok.Body, &body)
	c.userID = body.UserID
	return c
}

// call sends a request and reads until its reply, failing on an ERROR for it.
func (c *client) call(t *testing.T, typ wire.MsgType, body any, want wire.MsgType) wire.Envelope {
	t.Helper()
	c.seq++
	c.req++
	if err := c.conn.Send(typ, c.seq, 0, c.req, body); err != nil {
		t.Fatal(err)
	}
	for {
		e := c.read(t)
		if e.RequestID != c.req {
			continue
		}
		if e.Type == wire.MsgError {
			var eb wire.ErrorBody
			_ = wire.Unmarshal(e.Body, &eb)
			t.Fatalf("%s answered ERROR %v: %s", typ, eb.Code, eb.Message)
		}
		if e.Type == want {
			return e
		}
	}
}

func (c *client) until(t *testing.T, want wire.MsgType) wire.Envelope {
	t.Helper()
	for {
		if e := c.read(t); e.Type == want {
			return e
		}
	}
}

func (c *client) read(t *testing.T) wire.Envelope {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(readTimeout))
	e, err := c.conn.ReadEnvelope()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return e
}
