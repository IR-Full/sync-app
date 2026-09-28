package rpc_test

import (
	"context"
	"errors"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/SyncApp-chat/SyncApp/internal/chat"
	"github.com/SyncApp-chat/SyncApp/internal/fanout"
	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/rpc"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/internal/store/memory"
	"github.com/SyncApp-chat/SyncApp/pkg/id"
)

// fanoutd wires the chat client as fanout.ChatKinds; if the method ever stops
// matching, receipts in channels would quietly go back to being broadcast there.
var _ fanout.ChatKinds = (*rpc.ChatClient)(nil)

func startChatService(t *testing.T) (*rpc.ChatClient, *chat.Service) {
	t.Helper()
	ids, _ := id.NewGenerator(1)
	chatSvc := chat.New(memory.New().Stores().Chats, ids)

	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	rpc.RegisterChat(srv, chatSvc)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return rpc.NewChatClient(conn), chatSvc
}

// TestChatTypeOverRPC: fanoutd tells a channel from a group through this call,
// so the kind has to survive the trip for every chat shape.
func TestChatTypeOverRPC(t *testing.T) {
	cl, svc := startChatService(t)
	ctx := context.Background()

	channel, err := svc.CreateGroup(ctx, "1", "news", model.ChatChannel, []string{"2"})
	if err != nil {
		t.Fatal(err)
	}
	group, err := svc.CreateGroup(ctx, "1", "friends", model.ChatGroup, []string{"2"})
	if err != nil {
		t.Fatal(err)
	}
	direct, err := svc.EnsureDirect(ctx, "1", "2")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []*model.Chat{channel, group, direct} {
		got, err := cl.ChatType(ctx, want.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got != want.Type {
			t.Errorf("chat %s: type %q over RPC, want %q", want.ID, got, want.Type)
		}
	}

	if _, err := cl.ChatType(ctx, "999999"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing chat: err = %v, want store.ErrNotFound", err)
	}
}
