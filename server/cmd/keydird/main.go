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

	svc := wiring.NewKeyDir(b)
	addr := platform.Env("SYNCAPP_KEYDIRD_ADDR", ":9005")
	if err := platform.ServeGRPC(ctx, addr, platform.Env("SYNCAPP_KEYDIRD_METRICS", ":9105"), b.Log,
		func(s *grpc.Server) { rpc.RegisterKeyDir(s, svc) }); err != nil {
		b.Log.Error("serve", "err", err)
		os.Exit(1)
	}
}
