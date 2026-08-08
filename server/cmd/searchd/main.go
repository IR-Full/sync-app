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

	"github.com/synapse-chat/synapse/internal/platform"
	"github.com/synapse-chat/synapse/internal/rpc"
	"github.com/synapse-chat/synapse/internal/search"
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

	var backend search.Backend
	if dsn := os.Getenv("SYNAPSE_PG_DSN"); dsn != "" {
		backend, err = search.NewPostgresBackend(ctx, dsn)
		if err != nil {
			b.Log.Error("search backend", "err", err)
			os.Exit(1)
		}
		b.Log.Info("search: postgres tsvector")
	} else {
		backend = search.NewMemoryBackend()
		b.Log.Info("search: in-memory")
	}

	chatConn, err := platform.Dial(platform.Env("SYNAPSE_CHATD_ADDR", "localhost:9002"), "chatd", b.Log)
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
	platform.RunWorker(ctx, platform.Env("SYNAPSE_SEARCHD_METRICS", ":9109"), b.Log)
}
