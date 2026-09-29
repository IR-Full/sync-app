// Command gatewayd is the realtime edge of the split deployment. It owns client
// connections (TCP, WebSocket, QUIC) and the protocol, and reaches the domain
// services over gRPC. Its services are assembled by internal/wiring exactly as
// the monolith's are; only the domain half is swapped for gRPC clients.
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
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	conns, closeConns, err := wiring.DialFleet(b)
	if err != nil {
		return err
	}
	defer closeConns()

	node, err := wiring.Fleet(ctx, b, cfg, conns)
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
