// Command moderationd runs the moderation/abuse worker. It observes message
// events off the bus, applies banned-term and spam-velocity rules, and records
// incidents. Advisory (observe-and-record) — it does not block delivery.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/synapse-chat/synapse/internal/moderation"
	"github.com/synapse-chat/synapse/internal/platform"
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

	banned := strings.Split(platform.Env("SYNAPSE_BANNED_TERMS", "spamword,scamlink"), ",")
	svc := moderation.New(b.Bus, banned, b.Log)
	if err := svc.Start(); err != nil {
		b.Log.Error("start", "err", err)
		os.Exit(1)
	}
	platform.RunWorker(ctx, platform.Env("SYNAPSE_MODERATIOND_METRICS", ":9108"), b.Log)
}
