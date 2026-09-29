// Command moderationd runs the moderation/abuse worker. It observes message
// events off the bus, applies banned-term and spam-velocity rules, and records
// incidents. Advisory (observe-and-record) — it does not block delivery.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/IR-Full/sync-app/server/internal/platform"
	"github.com/IR-Full/sync-app/server/internal/wiring"
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

	if err := wiring.NewModeration(cfg, b.Bus, b.Log).Start(); err != nil {
		b.Log.Error("start", "err", err)
		os.Exit(1)
	}
	platform.RunWorker(ctx, platform.Env("SYNCAPP_MODERATIOND_METRICS", ":9108"), b.Log)
}
