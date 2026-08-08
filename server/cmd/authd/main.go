// Command authd runs the Auth/Session service as a standalone gRPC process.
// It owns the users/sessions/devices tables; the gateway calls it to
// register/login/authenticate/resume. See internal/rpc for the contract.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"

	"github.com/synapse-chat/synapse/internal/auth"
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

	svc := auth.New(b.Stores.Users, b.Stores.Sessions, b.IDs)
	addr := platform.Env("SYNAPSE_AUTHD_ADDR", ":9001")
	if err := platform.ServeGRPC(ctx, addr, platform.Env("SYNAPSE_AUTHD_METRICS", ":9101"), b.Log,
		func(s *grpc.Server) { rpc.RegisterAuth(s, svc) }); err != nil {
		b.Log.Error("serve", "err", err)
		os.Exit(1)
	}
}
