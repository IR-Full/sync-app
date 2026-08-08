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

	"github.com/synapse-chat/synapse/internal/fanout"
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

	chatConn, err := platform.Dial(platform.Env("SYNAPSE_CHATD_ADDR", "localhost:9002"), "chatd", b.Log)
	if err != nil {
		b.Log.Error("dial chatd", "err", err)
		os.Exit(1)
	}
	defer func() { _ = chatConn.Close() }()

	fan := fanout.New(b.Bus, rpc.NewChatClient(chatConn), b.Router, b.Log)
	if err := fan.Start(); err != nil {
		b.Log.Error("start", "err", err)
		os.Exit(1)
	}
	platform.RunWorker(ctx, platform.Env("SYNAPSE_FANOUTD_METRICS", ":9106"), b.Log)
}
