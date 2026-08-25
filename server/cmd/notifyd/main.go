// Command notifyd runs the push-notification worker. It consumes offline push
// jobs from the bus and hands them to a provider (APNs/FCM in production; a log
// provider here). Pure bus consumer — no gRPC surface.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/SyncApp-chat/SyncApp/internal/notify"
	"github.com/SyncApp-chat/SyncApp/internal/platform"
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

	svc := notify.New(b.Bus,
		notify.ProviderFor(os.Getenv("SyncApp_PUSH_ENDPOINT"), os.Getenv("SyncApp_PUSH_KEY"), b.Log), b.Log).
		WithDevices(notify.StoreDevices{Users: b.Stores.Users})
	if err := svc.Start(); err != nil {
		b.Log.Error("start", "err", err)
		os.Exit(1)
	}
	platform.RunWorker(ctx, platform.Env("SyncApp_NOTIFYD_METRICS", ":9107"), b.Log)
}
