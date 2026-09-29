package wiring

import (
	"context"

	"google.golang.org/grpc"

	"github.com/SyncApp-chat/SyncApp/internal/chat"
	"github.com/SyncApp-chat/SyncApp/internal/gateway"
	"github.com/SyncApp-chat/SyncApp/internal/message"
	"github.com/SyncApp-chat/SyncApp/internal/outbox"
	"github.com/SyncApp-chat/SyncApp/internal/platform"
	"github.com/SyncApp-chat/SyncApp/internal/rpc"
)

// Node is an assembled gateway process, ready to Serve.
type Node struct {
	Gateway *gateway.Gateway
	Edge    *Edge
}

// Start begins cross-node delivery and the edge's background work.
func (n *Node) Start(ctx context.Context) error {
	if err := n.Gateway.StartDelivery(); err != nil {
		return err
	}
	n.Edge.RunBackground(ctx, n.Gateway)
	return nil
}

// Monolith assembles every service in one process: the domain services, the
// edge, and the workers the split runs as separate daemons (fanout, the outbox
// relay, search indexing, moderation, push). The workers are started here;
// call Node.Start and then Serve.
func Monolith(ctx context.Context, b *platform.Backends, cfg Config) (*Node, error) {
	chatSvc := chat.New(b.Stores.Chats, b.IDs)
	msgSvc := message.New(b.MessageStore, b.Stores.Reads, chatSvc, b.Bus, b.IDs)
	contacts := NewContacts(b.Stores)

	edge, err := NewEdge(ctx, b, cfg, EdgeDeps{
		Core: Core{
			Auth:     NewAuth(b.Stores, b.IDs),
			Chat:     chatSvc,
			Msg:      msgSvc,
			Broker:   message.NewBroker(msgSvc, b.Log),
			Presence: NewPresence(b),
			KeyDir:   NewKeyDir(b),
		},
		Membership: chatSvc,
		ChatAdmin:  chatSvc,
		Contacts:   contacts,
		Sender:     msgSvc,
	})
	if err != nil {
		return nil, err
	}
	// Deleting a message releases its blob at once. Only possible in-process:
	// in the split the blob store lives at the edge and the periodic sweep does it.
	msgSvc.WithMedia(edge.Media)

	fan := NewFanout(b, FanoutDeps{
		Chats: chatSvc, Kinds: chatSvc, Mute: chatSvc,
		Users: b.Stores.Users, Contacts: contacts,
	})
	if err := fan.Start(); err != nil {
		return nil, err
	}
	for _, ob := range b.MsgOutbox {
		go outbox.New(ob, b.Bus, b.Log).Run(ctx)
	}
	if err := edge.Search.Start(b.Bus); err != nil {
		return nil, err
	}
	if err := NewModeration(cfg, b.Bus, b.Log).Start(); err != nil {
		return nil, err
	}
	if err := NewNotify(cfg, b.Stores, b.Bus, b.Log).Start(); err != nil {
		return nil, err
	}

	gw := gateway.New(edge.Services, cfg.GatewayConfig(b.NodeID), b.Log)
	return &Node{Gateway: gw, Edge: edge}, nil
}

// FleetConns are gatewayd's connections to the domain services.
type FleetConns struct {
	Auth, Chat, Message, Presence, KeyDir *grpc.ClientConn
}

// DialFleet dials the domain services at the addresses in the environment.
// The returned close func closes every connection that was opened.
func DialFleet(b *platform.Backends) (FleetConns, func(), error) {
	var fc FleetConns
	var opened []*grpc.ClientConn
	closeAll := func() {
		for _, c := range opened {
			_ = c.Close()
		}
	}
	for _, d := range []struct {
		env, def, name string
		dst            **grpc.ClientConn
	}{
		{"SYNCAPP_AUTHD_ADDR", "localhost:9001", "authd", &fc.Auth},
		{"SYNCAPP_CHATD_ADDR", "localhost:9002", "chatd", &fc.Chat},
		{"SYNCAPP_MESSAGED_ADDR", "localhost:9003", "messaged", &fc.Message},
		{"SYNCAPP_PRESENCED_ADDR", "localhost:9004", "presenced", &fc.Presence},
		{"SYNCAPP_KEYDIRD_ADDR", "localhost:9005", "keydird", &fc.KeyDir},
	} {
		addr := platform.Env(d.env, d.def)
		conn, err := platform.Dial(addr, d.name, b.Log)
		if err != nil {
			closeAll()
			return FleetConns{}, nil, err
		}
		b.Log.Info("dialed service", "name", d.name, "addr", addr)
		opened = append(opened, conn)
		*d.dst = conn
	}
	return fc, closeAll, nil
}

// Fleet assembles the edge of the split deployment: the domain services are
// gRPC clients, everything else is built exactly as in Monolith. Fanout, the
// outbox relay, search indexing, moderation, push and the scheduled-send
// dispatcher run in their own daemons.
func Fleet(ctx context.Context, b *platform.Backends, cfg Config, fc FleetConns) (*Node, error) {
	chats := rpc.NewChatClient(fc.Chat)
	msgs := rpc.NewMessageClient(fc.Message)

	edge, err := NewEdge(ctx, b, cfg, EdgeDeps{
		Core: Core{
			Auth:     rpc.NewAuthClient(fc.Auth),
			Chat:     chats,
			Msg:      msgs,
			Broker:   msgs,
			Presence: rpc.NewPresenceClient(fc.Presence),
			KeyDir:   rpc.NewKeyDirClient(fc.KeyDir, b.Log),
		},
		Membership: chats,
		ChatAdmin:  chat.New(b.Stores.Chats, b.IDs),
		Contacts:   NewContacts(b.Stores),
	})
	if err != nil {
		return nil, err
	}
	gw := gateway.New(edge.Services, cfg.GatewayConfig(b.NodeID), b.Log)
	return &Node{Gateway: gw, Edge: edge}, nil
}
