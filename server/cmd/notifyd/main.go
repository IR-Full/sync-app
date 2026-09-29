// Command notifyd runs the push-notification worker. It consumes offline push
// jobs from the bus and hands them to a provider (APNs/FCM in production; a log
// provider here). Pure bus consumer — no gRPC surface.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/SyncApp-chat/SyncApp/internal/platform"
	"github.com/SyncApp-chat/SyncApp/internal/wiring"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := wiring.FromEnv()
	if err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
	b, err := platform.Load(ctx, log)
	if err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
	defer b.Close()

	if err := wiring.NewNotify(cfg, b.Stores, b.Bus, b.Log).Start(); err != nil {
		b.Log.Error("start", "err", err)
		os.Exit(1)
	}
	platform.RunWorker(ctx, platform.Env("SYNCAPP_NOTIFYD_METRICS", ":9107"), b.Log)
}
