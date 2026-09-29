// Package platform is the shared bootstrap for every SyncApp process — the
// gateway edge, each domain-service daemon, and the async workers. It builds the
// common backends from the environment (the same selection cmd/server uses) and
// provides gRPC serve/dial helpers with optional mTLS, so a service binary is
// just: Load backends → construct the one service → Serve. This keeps the
// microservice mains tiny and identical in their wiring.
package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/SyncApp-chat/SyncApp/internal/envcfg"
	"github.com/SyncApp-chat/SyncApp/internal/metrics"
	"github.com/SyncApp-chat/SyncApp/internal/nodeid"
	"github.com/SyncApp-chat/SyncApp/internal/presence"
	"github.com/SyncApp-chat/SyncApp/internal/replay"
	"github.com/SyncApp-chat/SyncApp/internal/router"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/internal/store/memory"
	"github.com/SyncApp-chat/SyncApp/internal/store/postgres"
	"github.com/SyncApp-chat/SyncApp/internal/store/sharded"
	"github.com/SyncApp-chat/SyncApp/internal/tracing"
	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
	"github.com/SyncApp-chat/SyncApp/pkg/id"
	"github.com/SyncApp-chat/SyncApp/pkg/mtls"
)

// Backends holds the shared infrastructure handles.
type Backends struct {
	Log      *slog.Logger
	Region   string
	NodeID   int64
	IDs      *id.Generator
	Stores   store.Stores
	Bus      eventbus.Bus
	Presence presence.Backend
	Router   router.Router
	Replay   replay.Buffer
	Redis    *redis.Client // nil unless SYNCAPP_REDIS_ADDR is set

	// MessageStore is the write path for messages: the primary store by default,
	// or a chat_id-sharded store across SYNCAPP_MESSAGE_SHARD_DSNS. MsgOutbox is
	// the set of outbox stores a relay must drain (one per shard, or the primary).
	MessageStore store.MessageStore
	MsgOutbox    []store.OutboxStore

	closers []func()
}

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

	b.NodeID, err = resolveNodeID(ctx, log, b)
	if err != nil {
		return nil, err
	}
	b.IDs, err = id.NewGenerator(b.NodeID)
	if err != nil {
		return nil, err
	}
	b.Region = Env("SYNCAPP_REGION", "local")
	b.Log = log.With("region", b.Region, "node", b.NodeID)

	// Storage.
	if dsn := envcfg.Get("SYNCAPP_PG_DSN"); dsn != "" {
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
	if url := envcfg.Get("SYNCAPP_NATS_URL"); url != "" {
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
	if addr := envcfg.Get("SYNCAPP_REDIS_ADDR"); addr != "" {
		b.Presence, err = presence.NewRedisBackend(addr, envcfg.Get("SYNCAPP_REDIS_PASSWORD"), 0)
		if err != nil {
			return nil, err
		}
		b.Redis = redis.NewClient(&redis.Options{Addr: addr, Password: envcfg.Get("SYNCAPP_REDIS_PASSWORD")})
		b.closers = append(b.closers, func() { _ = b.Redis.Close() })
		b.Router = router.NewResilient(router.NewRedis(b.Redis, 60*time.Second), b.Log)
		// Async: the gateway appends every outbound frame from the connection's
		// single writer, which must never wait on Redis (see replay.Async).
		// Closers run in reverse, so this drains before the client closes.
		replayBuf := replay.NewAsync(replay.NewRedis(b.Redis, 10*time.Minute), 0, b.Log)
		b.closers = append(b.closers, replayBuf.Close)
		b.Replay = replayBuf
		b.Log.Info("presence+router+resume: redis", "addr", addr)
	} else {
		b.Presence = presence.NewMemoryBackend()
		b.Router = router.NewMemory()
		b.Replay = replay.NewMemory()
		b.Log.Info("presence+router+resume: in-memory (single-node)")
	}

	// Message write path: sharded by chat_id across SYNCAPP_MESSAGE_SHARD_DSNS when
	// set (each shard is a full Postgres, but only its messages/chat_seq/outbox are
	// used — chat metadata stays in the primary store). Each shard allocates a
	// gap-free per-chat seq locally (chat_seq), and each has its own outbox that a
	// relay must drain. Default: the single primary store.
	b.MessageStore = b.Stores.Messages
	b.MsgOutbox = []store.OutboxStore{b.Stores.Outbox}
	if dsns := envcfg.Get("SYNCAPP_MESSAGE_SHARD_DSNS"); dsns != "" {
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

// resolveNodeID picks the Snowflake node id (0..1023). Precedence: explicit
// SYNCAPP_NODE_ID, then a Redis lease (unique by construction), then a
// hostname hash.
//
// The hash is a CONVENIENCE FOR SINGLE-NODE DEV ONLY, and the reason matters: the
// node id is 10 bits of every id this process mints, so two instances that hash
// to the same value mint colliding message and session ids — and with 1024 slots
// a collision is likely well before a thousand nodes (birthday bound: ~50% at 38).
// Duplicate ids are silent data corruption, not a degraded mode, so a deployment
// that declares itself production (SYNCAPP_REQUIRE_TLS=1) is refused rather than
// allowed to run on a guess.
func resolveNodeID(ctx context.Context, log *slog.Logger, b *Backends) (int64, error) {
	if v := envcfg.Get("SYNCAPP_NODE_ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 && n <= 1023 {
			return n, nil
		}
		return 0, fmt.Errorf("invalid SYNCAPP_NODE_ID %q: want an integer in 0..1023", v)
	}
	leaseAttempted := false
	if addr := envcfg.Get("SYNCAPP_REDIS_ADDR"); addr != "" {
		leaseAttempted = true
		rdb := redis.NewClient(&redis.Options{Addr: addr, Password: envcfg.Get("SYNCAPP_REDIS_PASSWORD")})
		n, release, err := nodeid.Lease(ctx, rdb, 30*time.Second)
		if err == nil {
			b.closers = append(b.closers, func() { release(); _ = rdb.Close() })
			log.Info("node id leased from Redis", "node_id", n)
			return n, nil
		}
		log.Error("node-id lease failed", "err", err)
		_ = rdb.Close()
	}
	if RequireTLS() {
		// Redis was configured, so this IS a multi-node deployment whose coordinator
		// is unreachable. Falling back here would mint ids that may already belong to
		// a peer.
		if leaseAttempted {
			return 0, errors.New("node-id lease failed and SYNCAPP_REQUIRE_TLS=1: refusing to guess a node id from the hostname (would risk colliding snowflake ids); fix Redis or set SYNCAPP_NODE_ID")
		}
		return 0, errors.New("SYNCAPP_REQUIRE_TLS=1 requires an explicit SYNCAPP_NODE_ID or SYNCAPP_REDIS_ADDR for a unique node id")
	}
	host, _ := os.Hostname()
	var h uint32 = 2166136261
	for i := 0; i < len(host); i++ {
		h ^= uint32(host[i])
		h *= 16777619
	}
	n := int64(h % 1024)
	log.Warn("node id derived from hostname — UNSAFE for multi-node: set SYNCAPP_NODE_ID or SYNCAPP_REDIS_ADDR",
		"host", host, "node_id", n)
	return n, nil
}

// serverCreds returns mTLS transport credentials when SYNCAPP_MTLS_* is set, or
// insecure credentials for local/dev.
func serverCreds(log *slog.Logger) credentials.TransportCredentials {
	ca, cert, key := envcfg.Get("SYNCAPP_MTLS_CA"), envcfg.Get("SYNCAPP_MTLS_CERT"), envcfg.Get("SYNCAPP_MTLS_KEY")
	if ca == "" || cert == "" || key == "" {
		log.Warn("mTLS disabled between services — set SYNCAPP_MTLS_CA/CERT/KEY in production")
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
	ca, cert, key := envcfg.Get("SYNCAPP_MTLS_CA"), envcfg.Get("SYNCAPP_MTLS_CERT"), envcfg.Get("SYNCAPP_MTLS_KEY")
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
	srv := grpc.NewServer(
		grpc.Creds(serverCreds(log)),
		grpc.UnaryInterceptor(recoverUnary(log)),
		grpc.StreamInterceptor(recoverStream(log)),
	)
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

// recoverUnary turns a panic in a handler into an INTERNAL error for that one
// call. Without it a panic in any RPC handler unwinds the serving goroutine and
// takes the whole daemon down — every other in-flight request with it — which
// turns one bad argument into an outage of authd, chatd or messaged.
func recoverUnary(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				metrics.PanicsRecovered.WithLabelValues("grpc.unary").Inc()
				log.Error("recovered panic in grpc handler",
					"method", info.FullMethod, "panic", r, "stack", string(debug.Stack()))
				// The panic text may quote request data, so it is logged, not returned.
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(ctx, req)
	}
}

// recoverStream is recoverUnary for streaming handlers.
func recoverStream(log *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if r := recover(); r != nil {
				metrics.PanicsRecovered.WithLabelValues("grpc.stream").Inc()
				log.Error("recovered panic in grpc stream handler",
					"method", info.FullMethod, "panic", r, "stack", string(debug.Stack()))
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(srv, ss)
	}
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
