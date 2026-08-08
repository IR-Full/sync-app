// Command messaged runs the Message service as a standalone gRPC process. It owns
// the durable write path (create/edit/delete via the broker) and the read path
// (history, read receipts). It authorizes writes by calling chatd (CanPost/
// IsMember) and co-locates the transactional-outbox relay, which drains staged
// events to the bus — keeping "commit + publish" on the process that owns the DB.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"

	"github.com/synapse-chat/synapse/internal/message"
	"github.com/synapse-chat/synapse/internal/outbox"
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

	// Authorize writes/reads against the remote chat service.
	chatConn, err := platform.Dial(platform.Env("SYNAPSE_CHATD_ADDR", "localhost:9002"), "chatd", b.Log)
	if err != nil {
		b.Log.Error("dial chatd", "err", err)
		os.Exit(1)
	}
	defer func() { _ = chatConn.Close() }()
	chats := rpc.NewChatClient(chatConn)

	// b.MessageStore is chat_id-sharded when SYNAPSE_MESSAGE_SHARD_DSNS is set,
	// else the single primary. Read-state stays central (small, not the bottleneck).
	svc := message.New(b.MessageStore, b.Stores.Reads, chats, b.Bus, b.IDs)
	broker := message.NewBroker(svc, b.Log)

	// One transactional-outbox relay per shard (each shard stages its own events
	// atomically with the message write).
	for _, ob := range b.MsgOutbox {
		go outbox.New(ob, b.Bus, b.Log).Run(ctx)
	}

	addr := platform.Env("SYNAPSE_MESSAGED_ADDR", ":9003")
	if err := platform.ServeGRPC(ctx, addr, platform.Env("SYNAPSE_MESSAGED_METRICS", ":9103"), b.Log,
		func(s *grpc.Server) { rpc.RegisterMessage(s, broker, svc) }); err != nil {
		b.Log.Error("serve", "err", err)
		os.Exit(1)
	}
}
