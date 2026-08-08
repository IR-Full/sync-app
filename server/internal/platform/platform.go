// Package platform is the shared bootstrap for every Synapse process — the
// gateway edge, each domain-service daemon, and the async workers. It builds the
// common backends from the environment (the same selection cmd/server uses) and
// provides gRPC serve/dial helpers with optional mTLS, so a service binary is
// just: Load backends → construct the one service → Serve. This keeps the
// microservice mains tiny and identical in their wiring.
package platform

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/synapse-chat/synapse/internal/nodeid"
	"github.com/synapse-chat/synapse/internal/presence"
	"github.com/synapse-chat/synapse/internal/replay"
	"github.com/synapse-chat/synapse/internal/router"
	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/internal/store/memory"
	"github.com/synapse-chat/synapse/internal/store/postgres"
	"github.com/synapse-chat/synapse/internal/store/sharded"
	"github.com/synapse-chat/synapse/internal/tracing"
	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/id"
	"github.com/synapse-chat/synapse/pkg/mtls"
)

// Env reads an env var with a default.
func Env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Close releases every backend in reverse order.
func (b *Backends) Close() {
	for i := len(b.closers) - 1; i >= 0; i-- {
		b.closers[i]()
	}
}

// Load builds the shared backends, selecting real infra when the env DSNs are
// set and falling back to in-memory otherwise (so any daemon runs with zero
// setup). The returned Backends.Close must be deferred by the caller.
func Load(ctx context.Context, log *slog.Logger) (*Backends, error) {
	b := &Backends{Log: log}

	shutdownTracing, err := tracing.Init(ctx)
	if err != nil {
		return nil, err
	}
	b.closers = append(b.closers, func() { _ = shutdownTracing(context.Background()) })

	b.NodeID = resolveNodeID(ctx, log, b)
	b.IDs, err = id.NewGenerator(b.NodeID)
	if err != nil {
		return nil, err
	}
	b.Region = Env("SYNAPSE_REGION", "local")
	b.Log = log.With("region", b.Region, "node", b.NodeID)

	// Storage.
	if dsn := os.Getenv("SYNAPSE_PG_DSN"); dsn != "" {
		pg, err := postgres.Connect(ctx, dsn)
		if err != nil {
			return nil, err
		}
		if err := pg.Migrate(ctx); err != nil {
			return nil, err
		}
		b.Stores = pg.Stores()
		b.closers = append(b.closers, pg.Close)
		b.Log.Info("storage: postgres")
	} else {
		b.Stores = memory.New().Stores()
		b.Log.Info("storage: in-memory")
	}

	// Event bus.
	if url := os.Getenv("SYNAPSE_NATS_URL"); url != "" {
		b.Bus, err = eventbus.NewNATS(url)
		if err != nil {
			return nil, err
		}
		b.Log.Info("eventbus: nats", "url", url)
	} else {
		b.Bus = eventbus.NewMemory()
		b.Log.Info("eventbus: in-memory")
	}
	b.closers = append(b.closers, func() { _ = b.Bus.Close() })

	// Redis-backed presence / router / resume.
	if addr := os.Getenv("SYNAPSE_REDIS_ADDR"); addr != "" {
		b.Presence, err = presence.NewRedisBackend(addr, os.Getenv("SYNAPSE_REDIS_PASSWORD"), 0)
		if err != nil {
			return nil, err
		}
		b.Redis = redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("SYNAPSE_REDIS_PASSWORD")})
		b.closers = append(b.closers, func() { _ = b.Redis.Close() })
		b.Router = router.NewResilient(router.NewRedis(b.Redis, 60*time.Second), b.Log)
		b.Replay = replay.NewRedis(b.Redis, 10*time.Minute)
		b.Log.Info("presence+router+resume: redis", "addr", addr)
	} else {
		b.Presence = presence.NewMemoryBackend()
		b.Router = router.NewMemory()
		b.Replay = replay.NewMemory()
		b.Log.Info("presence+router+resume: in-memory (single-node)")
	}

	// Message write path: sharded by chat_id across SYNAPSE_MESSAGE_SHARD_DSNS when
	// set (each shard is a full Postgres, but only its messages/chat_seq/outbox are
	// used — chat metadata stays in the primary store). Each shard allocates a
	// gap-free per-chat seq locally (chat_seq), and each has its own outbox that a
	// relay must drain. Default: the single primary store.
	b.MessageStore = b.Stores.Messages
	b.MsgOutbox = []store.OutboxStore{b.Stores.Outbox}
	if dsns := os.Getenv("SYNAPSE_MESSAGE_SHARD_DSNS"); dsns != "" {
		var msgShards []store.MessageStore
		var obShards []store.OutboxStore
		for _, d := range strings.Split(dsns, ",") {
			d = strings.TrimSpace(d)
			if d == "" {
				continue
			}
			ps, err := postgres.Connect(ctx, d)
			if err != nil {
				return nil, err
			}
			if err := ps.Migrate(ctx); err != nil {
				return nil, err
			}
			b.closers = append(b.closers, ps.Close)
			msgShards = append(msgShards, ps)
			obShards = append(obShards, ps)
		}
		b.MessageStore = sharded.New(msgShards...)
		b.MsgOutbox = obShards
		b.Log.Info("message store: chat_id-sharded", "shards", len(msgShards))
	}
	return b, nil
}

func resolveNodeID(ctx context.Context, log *slog.Logger, b *Backends) int64 {
	if v := os.Getenv("SYNAPSE_NODE_ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 && n <= 1023 {
			return n
		}
		log.Warn("invalid SYNAPSE_NODE_ID; using 0", "value", v)
		return 0
	}
	if addr := os.Getenv("SYNAPSE_REDIS_ADDR"); addr != "" {
		rdb := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("SYNAPSE_REDIS_PASSWORD")})
		if n, release, err := nodeid.Lease(ctx, rdb, 30*time.Second); err == nil {
			b.closers = append(b.closers, func() { release(); _ = rdb.Close() })
			return n
		}
		_ = rdb.Close()
	}
	host, _ := os.Hostname()
	var h uint32 = 2166136261
	for i := 0; i < len(host); i++ {
		h ^= uint32(host[i])
		h *= 16777619
	}
	return int64(h % 1024)
}

// serverCreds returns mTLS transport credentials when SYNAPSE_MTLS_* is set, or
// insecure credentials for local/dev.
func serverCreds(log *slog.Logger) credentials.TransportCredentials {
	ca, cert, key := os.Getenv("SYNAPSE_MTLS_CA"), os.Getenv("SYNAPSE_MTLS_CERT"), os.Getenv("SYNAPSE_MTLS_KEY")
	if ca == "" || cert == "" || key == "" {
		log.Warn("mTLS disabled between services — set SYNAPSE_MTLS_CA/CERT/KEY in production")
		return insecure.NewCredentials()
	}
	tc, err := mtls.ServerConfig(ca, cert, key)
	if err != nil {
		log.Error("mTLS server config failed; falling back to insecure", "err", err)
		return insecure.NewCredentials()
	}
	return credentials.NewTLS(tc)
}

// clientCreds mirrors serverCreds for dialing a peer service.
func clientCreds(serverName string, log *slog.Logger) credentials.TransportCredentials {
	ca, cert, key := os.Getenv("SYNAPSE_MTLS_CA"), os.Getenv("SYNAPSE_MTLS_CERT"), os.Getenv("SYNAPSE_MTLS_KEY")
	if ca == "" || cert == "" || key == "" {
		return insecure.NewCredentials()
	}
	tc, err := mtls.ClientConfig(ca, cert, key, serverName)
	if err != nil {
		log.Error("mTLS client config failed; falling back to insecure", "err", err)
		return insecure.NewCredentials()
	}
	return credentials.NewTLS(tc)
}

// ServeGRPC starts a gRPC server on addr, invokes register to attach services,
// and blocks until ctx is cancelled, then gracefully stops. It also serves
// /healthz and /metrics on metricsAddr (if non-empty) for observability parity
// with the monolith.
func ServeGRPC(ctx context.Context, addr, metricsAddr string, log *slog.Logger, register func(*grpc.Server)) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := grpc.NewServer(grpc.Creds(serverCreds(log)))
	register(srv)

	if metricsAddr != "" {
		go serveMetrics(metricsAddr, log)
	}

	go func() {
		<-ctx.Done()
		srv.GracefulStop()
	}()
	log.Info("grpc listening", "addr", addr)
	return srv.Serve(lis)
}

// RunWorker serves /metrics + /healthz on metricsAddr (if set) and blocks until
// ctx is cancelled. Async workers (fanoutd, notifyd, …) subscribe to the bus in
// their constructors and then call this to stay alive and observable.
func RunWorker(ctx context.Context, metricsAddr string, log *slog.Logger) {
	if metricsAddr != "" {
		go serveMetrics(metricsAddr, log)
	}
	log.Info("worker running", "metrics", metricsAddr)
	<-ctx.Done()
	log.Info("worker shutting down")
}

func serveMetrics(addr string, log *slog.Logger) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Warn("metrics http", "err", err)
	}
}

// Dial connects to a peer gRPC service at target, using mTLS when configured.
// serverName is the certificate name to verify (mTLS) — ignore for insecure.
func Dial(target, serverName string, log *slog.Logger) (*grpc.ClientConn, error) {
	return grpc.NewClient(target, grpc.WithTransportCredentials(clientCreds(serverName, log)))
}
