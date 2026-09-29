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

	"github.com/IR-Full/sync-app/server/internal/chat"
	"github.com/IR-Full/sync-app/server/internal/platform"
	"github.com/IR-Full/sync-app/server/internal/rpc"
	"github.com/IR-Full/sync-app/server/internal/wiring"
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
	fan := wiring.NewFanout(b, wiring.FanoutDeps{
		Chats: chats,
		Kinds: chats,
		// Per-member flags are not on chatd's contract; they are read from the
		// shared store, as the edge reads them.
		Mute:     chat.New(b.Stores.Chats, b.IDs),
		Users:    b.Stores.Users,
		Contacts: wiring.NewContacts(b.Stores),
	})
	if err := fan.Start(); err != nil {
		b.Log.Error("start", "err", err)
		os.Exit(1)
	}
	platform.RunWorker(ctx, platform.Env("SYNCAPP_FANOUTD_METRICS", ":9106"), b.Log)
}
