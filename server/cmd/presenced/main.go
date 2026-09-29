// Command presenced runs the Presence service as a standalone gRPC process
// (online/last-seen/typing), backed by Redis TTL keys in production.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"

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

	svc := wiring.NewPresence(b)
	addr := platform.Env("SYNCAPP_PRESENCED_ADDR", ":9004")
	if err := platform.ServeGRPC(ctx, addr, platform.Env("SYNCAPP_PRESENCED_METRICS", ":9104"), b.Log,
		func(s *grpc.Server) { rpc.RegisterPresence(s, svc) }); err != nil {
		b.Log.Error("serve", "err", err)
		os.Exit(1)
	}
}
