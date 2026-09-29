// Command server is the all-in-one runner: the domain services, the event bus
// workers and the realtime gateway in one process. The split deployment
// (cmd/gatewayd plus the service daemons) is assembled by the same package,
// internal/wiring, so the two cannot wire a service differently.
//
// Backends are selected by environment; with none set it runs fully in-memory.
//
//	SYNCAPP_PG_DSN     postgres://... (durable storage)
//	SYNCAPP_REDIS_ADDR host:6379      (presence, routing, resume, key directory)
//	SYNCAPP_NATS_URL   nats://...     (event bus)
//	SYNCAPP_TCP_ADDR   default :7000  (raw-TCP binary protocol)
//	SYNCAPP_WS_ADDR    default :8080  (WebSocket, /healthz, /metrics, media)
//	SYNCAPP_NODE_ID    0..1023        (snowflake node id; else a Redis lease)
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
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Before anything is built: a deployment that declares itself production and
	// is not safe to ship fails in the first second.
	if err := platform.EnforceProduction(func(msg string, args ...any) { log.Warn(msg, args...) }); err != nil {
		return err
	}
	cfg, err := wiring.FromEnv()
	if err != nil {
		return err
	}
	b, err := platform.Load(ctx, log)
	if err != nil {
		return err
	}
	defer b.Close()

	node, err := wiring.Monolith(ctx, b, cfg)
	if err != nil {
		return err
	}
	ln, err := wiring.Listen(cfg, b.Log)
	if err != nil {
		return err
	}
	if err := node.Start(ctx); err != nil {
		return err
	}
	return wiring.Serve(ctx, cfg, node.Gateway, node.Edge, ln, b.Log)
}
