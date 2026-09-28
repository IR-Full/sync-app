// Command fanoutd runs the Delivery/Fanout worker. It consumes message/read/
// typing events, resolves each chat's members (via chatd), looks up which gateway
// nodes hold those users (via the router), and publishes node-targeted deliveries
// on the bus. Recipients bound to no node are offline → a push job is emitted.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/SyncApp-chat/SyncApp/internal/fanout"
	"github.com/SyncApp-chat/SyncApp/internal/platform"
	"github.com/SyncApp-chat/SyncApp/internal/rpc"
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

	chatConn, err := platform.Dial(platform.Env("SYNCAPP_CHATD_ADDR", "localhost:9002"), "chatd", b.Log)
	if err != nil {
		b.Log.Error("dial chatd", "err", err)
		os.Exit(1)
	}
	defer func() { _ = chatConn.Close() }()

	chats := rpc.NewChatClient(chatConn)
	// Chat kinds keep read receipts in a channel with the reader, as in the
	// monolith; without them only the size rule applies (fanout.receiptsArePrivate).
	fan := fanout.New(b.Bus, chats, b.Router, b.Log).WithChatKinds(chats)
	if err := fan.Start(); err != nil {
		b.Log.Error("start", "err", err)
		os.Exit(1)
	}
	platform.RunWorker(ctx, platform.Env("SYNCAPP_FANOUTD_METRICS", ":9106"), b.Log)
}
