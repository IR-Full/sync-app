// Command searchd runs the search INDEXER worker: it consumes message events and
// writes to the shared Postgres tsvector index (in-memory when no DSN). The query
// path stays in the gateway, reading the same shared index — so index writes and
// reads are decoupled but consistent through Postgres.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/IR-Full/sync-app/server/internal/platform"
	"github.com/IR-Full/sync-app/server/internal/rpc"
	"github.com/IR-Full/sync-app/server/internal/search"
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

	backend, err := wiring.NewSearchBackend(ctx, cfg)
	if err != nil {
		b.Log.Error("search backend", "err", err)
		os.Exit(1)
	}
	chatConn, err := platform.Dial(platform.Env("SYNCAPP_CHATD_ADDR", "localhost:9002"), "chatd", b.Log)
	if err != nil {
		b.Log.Error("dial chatd", "err", err)
		os.Exit(1)
	}
	defer func() { _ = chatConn.Close() }()

	svc := search.New(backend, rpc.NewChatClient(chatConn), b.Log)
	if err := svc.Start(b.Bus); err != nil {
		b.Log.Error("start", "err", err)
		os.Exit(1)
	}
	platform.RunWorker(ctx, platform.Env("SYNCAPP_SEARCHD_METRICS", ":9109"), b.Log)
}
