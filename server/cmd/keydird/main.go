// Command keydird runs the E2E Key Directory as a standalone gRPC process. It
// stores only PUBLIC prekeys (identity/signed/one-time) and serves per-device
// bundles for X3DH — it never sees a private key or plaintext.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"

	"github.com/synapse-chat/synapse/internal/keydir"
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

	// Shared Redis directory across nodes when available, else in-memory.
	var dir keydir.Directory
	if b.Redis != nil {
		dir = keydir.NewRedis(b.Redis, b.Log)
		b.Log.Info("keydir: redis")
	} else {
		dir = keydir.NewMemory()
		b.Log.Info("keydir: in-memory")
	}

	addr := platform.Env("SYNAPSE_KEYDIRD_ADDR", ":9005")
	if err := platform.ServeGRPC(ctx, addr, platform.Env("SYNAPSE_KEYDIRD_METRICS", ":9105"), b.Log,
		func(s *grpc.Server) { rpc.RegisterKeyDir(s, dir) }); err != nil {
		b.Log.Error("serve", "err", err)
		os.Exit(1)
	}
}
