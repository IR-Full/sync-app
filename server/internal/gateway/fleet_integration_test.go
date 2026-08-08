package gateway_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/synapse-chat/synapse/internal/auth"
	"github.com/synapse-chat/synapse/internal/chat"
	"github.com/synapse-chat/synapse/internal/delivery"
	"github.com/synapse-chat/synapse/internal/fanout"
	"github.com/synapse-chat/synapse/internal/gateway"
	"github.com/synapse-chat/synapse/internal/keydir"
	"github.com/synapse-chat/synapse/internal/message"
	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/outbox"
	"github.com/synapse-chat/synapse/internal/presence"
	"github.com/synapse-chat/synapse/internal/replay"
	"github.com/synapse-chat/synapse/internal/router"
	"github.com/synapse-chat/synapse/internal/rpc"
	"github.com/synapse-chat/synapse/internal/store/memory"
	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/id"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// The architecture's central claim is that the same handler code runs as one
// process or as a fleet, with gRPC clients standing in for the local services.
// Until now nothing checked it: the monolith was covered end to end and the
// split was verified by hand, which is how a field can be present in the domain
// type, delivered by the monolith, and dropped at the gRPC hop for weeks.
//
// This boots every domain service behind a real gRPC server (over an in-process
// listener, so the test needs no ports, no Docker and no fixed addresses) and
// gives the gateway ONLY rpc clients. Nothing in the gateway knows the
// difference; if anything is lost in translation, it is lost here too.

// startFleetGateway serves auth/chat/message/presence/keydir over gRPC and wires
// a gateway that talks to them exclusively through rpc clients.
func startFleetGateway(t *testing.T) string {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ids, _ := id.NewGenerator(3)
	st := memory.New().Stores()
	bus := eventbus.NewMemory()
	rtr := router.NewMemory()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go outbox.New(st.Outbox, bus, log).Run(ctx)

	// The services, as the daemons would construct them.
	authSvc := auth.New(st.Users, st.Sessions, ids)
	chatSvc := chat.New(st.Chats, ids)
	msgSvc := message.New(st.Messages, st.Reads, chatSvc, bus, ids)
	presSvc := presence.New(presence.NewMemoryBackend(), bus, time.Minute)
	keyDir := keydir.NewMemory()

	fan := fanout.New(bus, chatSvc, rtr, log)
	if err := fan.Start(); err != nil {
		t.Fatal(err)
	}

	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	rpc.RegisterAuth(srv, authSvc)
	rpc.RegisterChat(srv, chatSvc)
	rpc.RegisterMessage(srv, message.NewBroker(msgSvc, log), msgSvc)
	rpc.RegisterPresence(srv, presSvc)
	rpc.RegisterKeyDir(srv, keyDir)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///fleet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// Every domain dependency below is a REMOTE client. The two exceptions are
	// inherently per-node: the connection hub and the user store the gateway uses
	// to resolve @usernames (which chatd would own in a fuller split).
	msgClient := rpc.NewMessageClient(conn)
	cfg := gateway.DefaultConfig()
	cfg.Heartbeat = time.Hour
	cfg.NodeID = "fleet-1"
	gw := gateway.New(gateway.Services{
		Auth:     rpc.NewAuthClient(conn),
		Chat:     rpc.NewChatClient(conn),
		Msg:      msgClient,
		Broker:   msgClient,
		Presence: rpc.NewPresenceClient(conn),
		KeyDir:   rpc.NewKeyDirClient(conn, log),
		Users:    st.Users,
		Hub:      delivery.NewHub(),
		Bus:      bus,
		Router:   rtr,
		Replay:   replay.NewMemory(),
	}, cfg, log)
	if err := gw.StartDelivery(); err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go gw.ServeTCP(ctx, ln)
	return ln.Addr().String()
}

// TestFleetTopologyDeliversTheWholeMessage runs the ordinary client flow against
// a gateway whose services are all remote, and asserts on the fields most easily
// lost in a contract: the attachment, the forward provenance and the
// self-destruct deadline. Those are exactly what the split silently dropped
// before the contract carried them.
func TestFleetTopologyDeliversTheWholeMessage(t *testing.T) {
	addr := startFleetGateway(t)
	alice := connect(t, addr, "fleetalice", "secret123")
	bob := connect(t, addr, "fleetbob", "secret123")

	// Register + login went through authd; this send goes through messaged, which
	// authorizes against chatd.
	alice.send(t, wire.MsgSend, 1, wire.SendBody{
		ChatID: "@fleetbob", DedupKey: "f1", Text: "voice note", TTLSeconds: 30,
		Attachment: &wire.Attachment{
			Kind: string(model.AttachVoice), MediaRef: "m-1", DurationMs: 3200, Waveform: []int32{2, 7, 4},
		},
	})
	ack := alice.readUntil(t, wire.MsgSendAck)
	var ab wire.SendAckBody
	_ = wire.Unmarshal(ack.Body, &ab)
	if ab.MessageID == "" || ab.ChatSeq != 1 {
		t.Fatalf("bad ack over the fleet: %+v", ab)
	}

	live := bob.readUntil(t, wire.MsgNew)
	var nb wire.NewMessageBody
	_ = wire.Unmarshal(live.Body, &nb)
	if nb.Text != "voice note" {
		t.Fatalf("delivery lost the text: %+v", nb)
	}
	if nb.Attachment == nil || nb.Attachment.DurationMs != 3200 || len(nb.Attachment.Waveform) != 3 {
		t.Fatalf("delivery lost the attachment: %+v", nb.Attachment)
	}
	if nb.ExpiresAt == 0 {
		t.Fatal("delivery lost the self-destruct deadline")
	}

	// Forwarding runs entirely inside messaged and comes back through the same
	// reply mapping.
	alice.send(t, wire.MsgForward, 2, wire.ForwardBody{
		FromChatID: ab.ChatID, MessageID: ab.MessageID, ToChatID: ab.ChatID, DedupKey: "f2",
	})
	fwd := bob.readUntil(t, wire.MsgNew)
	var fb wire.NewMessageBody
	_ = wire.Unmarshal(fwd.Body, &fb)
	if fb.Forward == nil || fb.Forward.SenderID != alice.userID {
		t.Fatalf("forward provenance did not survive the hop: %+v", fb.Forward)
	}

	// History is the read path: a second gRPC surface with its own mapping.
	bob.send(t, wire.MsgHistory, 3, wire.HistoryBody{ChatID: ab.ChatID, Limit: 10})
	var sawAttachment, sawTTL bool
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
		sawAttachment = sawAttachment || hb.Attachment != nil
		sawTTL = sawTTL || hb.ExpiresAt > 0
	}
	if !sawAttachment || !sawTTL {
		t.Fatalf("history over gRPC dropped fields: attachment=%v ttl=%v", sawAttachment, sawTTL)
	}
}

// TestFleetTopologyCreatesAndUsesAGroup covers the chat service across the hop:
// creation, membership and posting into what was created.
func TestFleetTopologyCreatesAndUsesAGroup(t *testing.T) {
	addr := startFleetGateway(t)
	alice := connect(t, addr, "fleetowner", "secret123")
	bob := connect(t, addr, "fleetmember", "secret123")

	alice.send(t, wire.MsgChatCreate, 1, wire.ChatCreateBody{
		Type: "group", Title: "fleet room", Members: []string{"@fleetmember"},
	})
	info := alice.readUntil(t, wire.MsgChatInfo)
	var ci wire.ChatInfoBody
	_ = wire.Unmarshal(info.Body, &ci)
	if ci.ChatID == "" || ci.OwnerID != alice.userID {
		t.Fatalf("group creation did not survive the hop: %+v", ci)
	}

	alice.send(t, wire.MsgSend, 2, wire.SendBody{ChatID: ci.ChatID, DedupKey: "g1", Text: "quorum?"})
	nw := bob.readUntil(t, wire.MsgNew)
	var nb wire.NewMessageBody
	_ = wire.Unmarshal(nw.Body, &nb)
	if nb.Text != "quorum?" || nb.ChatID != ci.ChatID {
		t.Fatalf("member did not receive the group message: %+v", nb)
	}
}
