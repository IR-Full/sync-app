// Command chatd runs the Chat service as a standalone gRPC process. It owns the
// chats/members/direct_index tables and answers membership/authorization queries
// (CanPost/IsMember/MemberIDs) used by the gateway, messaged, searchd, and
// fanoutd.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"

	"github.com/synapse-chat/synapse/internal/chat"
	"github.com/synapse-chat/synapse/internal/platform"
	"github.com/synapse-chat/synapse/internal/rpc"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	b, err := platform.Load(ctx, log)
	if err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
	defer b.Close()

	svc := chat.New(b.Stores.Chats, b.IDs)
	addr := platform.Env("SYNAPSE_CHATD_ADDR", ":9002")
	if err := platform.ServeGRPC(ctx, addr, platform.Env("SYNAPSE_CHATD_METRICS", ":9102"), b.Log,
		func(s *grpc.Server) { rpc.RegisterChat(s, svc) }); err != nil {
		b.Log.Error("serve", "err", err)
		os.Exit(1)
	}
}
