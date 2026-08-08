// Command server is the all-in-one runner for the Synapse MVP. It wires the
// domain services, the event bus, and the realtime gateway into one process
// (a "modular monolith" that is already split along the service boundaries from
// Section 6, so pieces can be peeled into separate deployables later).
//
// Backends are selected by environment. With none set it runs fully in-memory
// (great for `go run` and demos). Set the DSNs to use the Docker infra:
//
//	SYNAPSE_PG_DSN     postgres://... (enables durable storage)
//	SYNAPSE_REDIS_ADDR host:6379      (enables Redis presence)
//	SYNAPSE_NATS_URL   nats://...      (enables NATS event bus)
//	SYNAPSE_TCP_ADDR   default :7000   (raw-TCP binary protocol)
//	SYNAPSE_WS_ADDR    default :8080   (WebSocket + /healthz)
//	SYNAPSE_NODE_ID    default 1       (snowflake node id, 0..1023)
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/synapse-chat/synapse/internal/audit"
	"github.com/synapse-chat/synapse/internal/auth"
	"github.com/synapse-chat/synapse/internal/call"
	"github.com/synapse-chat/synapse/internal/chat"
	"github.com/synapse-chat/synapse/internal/contact"
	"github.com/synapse-chat/synapse/internal/delivery"
	"github.com/synapse-chat/synapse/internal/fanout"
	"github.com/synapse-chat/synapse/internal/gateway"
	"github.com/synapse-chat/synapse/internal/invite"
	"github.com/synapse-chat/synapse/internal/keydir"
	"github.com/synapse-chat/synapse/internal/media"
	"github.com/synapse-chat/synapse/internal/message"
	"github.com/synapse-chat/synapse/internal/moderation"
	"github.com/synapse-chat/synapse/internal/nodeid"
	"github.com/synapse-chat/synapse/internal/notify"
	"github.com/synapse-chat/synapse/internal/outbox"
	"github.com/synapse-chat/synapse/internal/pin"
	"github.com/synapse-chat/synapse/internal/platform"
	"github.com/synapse-chat/synapse/internal/poll"
	"github.com/synapse-chat/synapse/internal/presence"
	"github.com/synapse-chat/synapse/internal/reaction"
	"github.com/synapse-chat/synapse/internal/replay"
	"github.com/synapse-chat/synapse/internal/router"
	"github.com/synapse-chat/synapse/internal/schedule"
	"github.com/synapse-chat/synapse/internal/search"
	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/internal/store/memory"
	"github.com/synapse-chat/synapse/internal/store/postgres"
	"github.com/synapse-chat/synapse/internal/tracing"
	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/id"
	"github.com/synapse-chat/synapse/pkg/ratelimit"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
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

	// Distributed tracing (no-op unless SYNAPSE_TRACE=stdout / OTLP configured).
	shutdownTracing, err := tracing.Init(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = shutdownTracing(context.Background()) }()

	nodeID, releaseNode := resolveNodeID(ctx, log)
	defer releaseNode()
	ids, err := id.NewGenerator(nodeID)
	if err != nil {
		return err
	}

	// Region is a multi-region hook: in a multi-region deployment each region
	// runs its own gateway pool + data plane, users connect to the nearest, and
	// chats are home-region pinned (cross-region traffic flows over the event
	// bus). Here it is informational, stamped into logs for correlation.
	region := env("SYNAPSE_REGION", "local")
	log = log.With("region", region, "node", nodeID)

	// --- storage ---
	var stores store.Stores
	if dsn := os.Getenv("SYNAPSE_PG_DSN"); dsn != "" {
		pg, err := postgres.Connect(ctx, dsn)
		if err != nil {
			return err
		}
		defer pg.Close()
		if err := pg.Migrate(ctx); err != nil {
			return err
		}
		stores = pg.Stores()
		log.Info("storage: postgres")
	} else {
		stores = memory.New().Stores()
		log.Info("storage: in-memory (set SYNAPSE_PG_DSN for durable storage)")
	}

	// --- event bus ---
	var bus eventbus.Bus
	if url := os.Getenv("SYNAPSE_NATS_URL"); url != "" {
		bus, err = eventbus.NewNATS(url)
		if err != nil {
			return err
		}
		log.Info("eventbus: nats", "url", url)
	} else {
		bus = eventbus.NewMemory()
		log.Info("eventbus: in-memory")
	}
	defer bus.Close()

	// --- presence backend + cross-node router + resume buffer ---
	var pbackend presence.Backend
	var rtr router.Router
	var replayBuf replay.Buffer
	if addr := os.Getenv("SYNAPSE_REDIS_ADDR"); addr != "" {
		pbackend, err = presence.NewRedisBackend(addr, os.Getenv("SYNAPSE_REDIS_PASSWORD"), 0)
		if err != nil {
			return err
		}
		rdb := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("SYNAPSE_REDIS_PASSWORD")})
		defer rdb.Close()
		// Wrap in a circuit breaker + local fallback: a Redis outage degrades to
		// same-node delivery instead of a total routing failure.
		rtr = router.NewResilient(router.NewRedis(rdb, 60*time.Second), log)
		replayBuf = replay.NewRedis(rdb, 10*time.Minute)
		log.Info("presence+router+resume: redis", "addr", addr)
	} else {
		pbackend = presence.NewMemoryBackend()
		rtr = router.NewMemory()
		replayBuf = replay.NewMemory()
		log.Info("presence+router+resume: in-memory (single-node)")
	}

	// --- services ---
	hub := delivery.NewHub()
	authSvc := auth.New(stores.Users, stores.Sessions, ids)
	chatSvc := chat.New(stores.Chats, ids)
	msgSvc := message.New(stores.Messages, stores.Reads, chatSvc, bus, ids)
	msgBroker := message.NewBroker(msgSvc, log)
	presSvc := presence.New(pbackend, bus, 60*time.Second)
	reactSvc := reaction.New(stores.Reactions, chatSvc, bus)
	callSvc := call.New(stores.Calls, chatSvc, bus, ids)
	pollSvc := poll.New(stores.Polls, chatSvc, bus, ids)
	contactSvc := contact.New(stores.Contacts, stores.Users)
	pinSvc := pin.New(stores.Pins, stores.Drafts, chatSvc, bus)
	inviteSvc := invite.New(stores.Invites, stores.Chats.(store.MemberRoleStore), chatSvc)
	schedSvc := schedule.New(stores.Schedule, chatSvc, msgSvc, ids, log)
	go schedSvc.Run(ctx, 5*time.Second) // dispatches due sends + reaps self-destructed

	fan := fanout.New(bus, chatSvc, rtr, log)
	if err := fan.Start(); err != nil {
		return err
	}

	// Transactional-outbox relay: drains staged message events to the bus so a
	// crash between DB commit and publish cannot lose an event.
	go outbox.New(stores.Outbox, bus, log).Run(ctx)

	// Search indexer (consumes message events). Shared Postgres index when a DSN
	// is set (visible across nodes), else in-memory.
	var searchBackend search.Backend
	if dsn := os.Getenv("SYNAPSE_PG_DSN"); dsn != "" {
		searchBackend, err = search.NewPostgresBackend(ctx, dsn)
		if err != nil {
			return err
		}
		log.Info("search: postgres tsvector")
	} else {
		searchBackend = search.NewMemoryBackend()
		log.Info("search: in-memory")
	}
	searchSvc := search.New(searchBackend, chatSvc, log)
	if err := searchSvc.Start(bus); err != nil {
		return err
	}

	// Moderation/abuse (advisory; observes message events).
	modSvc := moderation.New(bus, []string{"spamword", "scamlink"}, log)
	if err := modSvc.Start(); err != nil {
		return err
	}

	// Push notifications (consumes offline push jobs).
	// Push: a real provider when an endpoint is configured, the logging stand-in
	// otherwise. WithDevices turns on the per-device fan-out and the removal of
	// tokens the provider reports as dead — without it, a user who uninstalls the
	// app costs a failed delivery on every message they are ever sent.
	notifySvc := notify.New(bus,
		notify.ProviderFor(os.Getenv("SYNAPSE_PUSH_ENDPOINT"), os.Getenv("SYNAPSE_PUSH_KEY"), log), log).
		WithDevices(notify.StoreDevices{Users: stores.Users})
	if err := notifySvc.Start(); err != nil {
		return err
	}

	// --- listeners ---
	tcpAddr := env("SYNAPSE_TCP_ADDR", ":7000")
	wsAddr := env("SYNAPSE_WS_ADDR", ":8080")

	// Media service (needs the public base URL for signed links).
	mediaDir := env("SYNAPSE_MEDIA_DIR", "./data/media")
	fsStore, err := media.NewFSStore(mediaDir)
	if err != nil {
		return err
	}
	publicBase := env("SYNAPSE_PUBLIC_URL", "http://localhost"+wsAddr)
	if os.Getenv("SYNAPSE_MEDIA_SECRET") == "" {
		log.Warn("SYNAPSE_MEDIA_SECRET not set — using an insecure dev default; set it (from a secrets manager) before production")
	}
	mediaSvc := media.New(fsStore, ids, platform.MediaSecret(), publicBase).WithLogger(log)
	// Blobs are collected, not leaked: the message log answers "is this still
	// referenced?", which is the only safe basis for deleting one — a forward
	// carries a copy of the original's ref. Deleting a message releases its bytes
	// at once; the sweep catches the rest (uploads never attached, and blobs freed
	// in bulk by the self-destruct reaper).
	if refs, ok := stores.Messages.(store.MediaReferencer); ok {
		mediaSvc.WithReferencer(refs)
		msgSvc.WithMedia(mediaSvc)
		go mediaSvc.RunGC(ctx, 0)
	}

	// E2E key directory (shared across nodes when Redis is configured).
	var keyDir keydir.Directory
	if addr := os.Getenv("SYNAPSE_REDIS_ADDR"); addr != "" {
		rdb := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("SYNAPSE_REDIS_PASSWORD")})
		defer rdb.Close()
		keyDir = keydir.NewRedis(rdb, log)
	} else {
		keyDir = keydir.NewMemory()
	}

	// Expensive per-user actions share one budget across a user's connections —
	// and across nodes when Redis is present, which is the only place a limit can
	// live if a user's second connection lands on a different pod.
	var userLimits ratelimit.Shared
	if addr := os.Getenv("SYNAPSE_REDIS_ADDR"); addr != "" {
		rdb := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("SYNAPSE_REDIS_PASSWORD")})
		defer rdb.Close()
		userLimits = ratelimit.NewRedisShared(rdb, "user", 2, 20)
		log.Info("per-user limits: redis (shared across nodes)")
	}

	gwCfg := gateway.DefaultConfig()
	gwCfg.NodeID = strconv.FormatInt(nodeID, 10)
	if v := os.Getenv("SYNAPSE_SEND_RATE"); v != "" {
		if f, e := strconv.ParseFloat(v, 64); e == nil {
			gwCfg.SendRate, gwCfg.SendBurst = f, f*2
		}
	}
	if origins := os.Getenv("SYNAPSE_ALLOWED_ORIGINS"); origins != "" {
		gwCfg.AllowedOrigins = strings.Split(origins, ",")
	} else {
		log.Warn("SYNAPSE_ALLOWED_ORIGINS not set — WebSocket accepts any origin (dev only); set your web origins before production")
	}
	// Per-IP accept guard (connection-flood / reconnect-storm defense). Off by
	// default so dev and tests are unaffected; set both in production.
	if v := os.Getenv("SYNAPSE_MAX_CONNS_PER_IP"); v != "" {
		if n, e := strconv.Atoi(v); e == nil {
			gwCfg.MaxConnsPerIP = n
		}
	}
	if v := os.Getenv("SYNAPSE_ACCEPT_RATE_PER_IP"); v != "" {
		if f, e := strconv.ParseFloat(v, 64); e == nil {
			gwCfg.AcceptRatePerIP = f
		}
	}
	if admins := os.Getenv("SYNAPSE_ADMIN_USERS"); admins != "" {
		gwCfg.AdminUsers = strings.Split(admins, ",")
	}
	if mods := os.Getenv("SYNAPSE_MODERATOR_USERS"); mods != "" {
		gwCfg.ModeratorUsers = strings.Split(mods, ",")
	}
	auditSink := audit.NewLogSink(log)

	gw := gateway.New(gateway.Services{
		Auth:       authSvc,
		Chat:       chatSvc,
		Msg:        msgSvc,
		Broker:     msgBroker,
		Presence:   presSvc,
		Reactor:    reactSvc,
		Calls:      callSvc,
		Polls:      pollSvc,
		Contacts:   contactSvc,
		Schedule:   schedSvc,
		Pins:       pinSvc,
		Invites:    inviteSvc,
		Users:      stores.Users,
		Hub:        hub,
		KeyDir:     keyDir,
		Media:      mediaSvc,
		Search:     searchSvc,
		Audit:      auditSink,
		Bus:        bus,
		Router:     rtr,
		Replay:     replayBuf,
		UserLimits: userLimits,
	}, gwCfg, log)
	if err := gw.StartDelivery(); err != nil {
		return err
	}

	// TLS terminates at the gateway edge; the custom protocol rides inside it.
	tlsConf, err := platform.BuildTLSConfig(log)
	if err != nil {
		return err
	}
	// Mandatory-TLS policy: with SYNAPSE_REQUIRE_TLS=1 the server refuses to start
	// in plaintext, so a misconfiguration can never silently expose cleartext
	// traffic in production. The plaintext path stays available for local dev.
	if os.Getenv("SYNAPSE_REQUIRE_TLS") == "1" && tlsConf == nil {
		return fmt.Errorf("SYNAPSE_REQUIRE_TLS=1 but TLS is not configured; set SYNAPSE_TLS_CERT/KEY (or SYNAPSE_TLS_SELFSIGNED=1 for dev)")
	}

	ln, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		return err
	}
	if tlsConf != nil {
		ln = tls.NewListener(ln, tlsConf)
	}
	go func() {
		log.Info("gateway listening (tcp)", "addr", tcpAddr, "tls", tlsConf != nil)
		if err := gw.ServeTCP(ctx, ln); err != nil {
			log.Error("tcp serve", "err", err)
		}
	}()

	// QUIC listens on the same address over UDP (requires TLS). Enable with
	// SYNAPSE_QUIC=1; gives mobile clients connection migration + no HOL blocking.
	if tlsConf != nil && os.Getenv("SYNAPSE_QUIC") == "1" {
		go func() {
			log.Info("gateway listening (quic)", "addr", tcpAddr)
			if err := gw.ServeQUIC(ctx, tcpAddr, tlsConf); err != nil {
				log.Error("quic serve", "err", err)
			}
		}()
	} else if os.Getenv("SYNAPSE_QUIC") == "1" {
		log.Warn("SYNAPSE_QUIC=1 ignored: QUIC requires TLS (set SYNAPSE_TLS_* or SYNAPSE_TLS_SELFSIGNED=1)")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", gw.ServeWS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/metrics", promhttp.Handler()) // Prometheus scrape target
	mediaSvc.RegisterHTTP(mux)                 // /media/upload/*, /media/download/*
	if os.Getenv("SYNAPSE_PPROF") == "1" {
		// Live profiling (CPU/heap/goroutine/block). Gated because it exposes
		// internals — bind to an internal port / behind auth in production.
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		log.Warn("pprof enabled at /debug/pprof/ — do not expose publicly")
	}
	httpSrv := &http.Server{
		Addr:      wsAddr,
		Handler:   mux,
		TLSConfig: tlsConf,
		// Slow-loris defense: cap how long a client may take to send request
		// headers (incl. the WebSocket upgrade), mirroring the raw-TCP handshake
		// deadline. Without it a stalled header write can hold a connection open.
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Info("gateway listening (ws)", "addr", wsAddr, "path", "/ws", "tls", tlsConf != nil)
		var serveErr error
		if tlsConf != nil {
			serveErr = httpSrv.ListenAndServeTLS("", "") // certs come from TLSConfig
		} else {
			serveErr = httpSrv.ListenAndServe()
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Error("http serve", "err", serveErr)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	gw.Shutdown() // drain live connections cleanly before stopping listeners
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	return nil
}

// resolveNodeID picks a unique Snowflake node id (0..1023). Precedence:
//  1. explicit SYNAPSE_NODE_ID (e.g. a Kubernetes StatefulSet ordinal);
//  2. a distributed lease from Redis (guarantees uniqueness across instances);
//  3. a hostname-derived id with a loud warning (collision possible).
//
// It returns the id and a release func (no-op unless a lease was taken).
func resolveNodeID(ctx context.Context, log *slog.Logger) (int64, func()) {
	if v := os.Getenv("SYNAPSE_NODE_ID"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 || n > 1023 {
			log.Warn("invalid SYNAPSE_NODE_ID; falling back to 0", "value", v)
			return 0, func() {}
		}
		return n, func() {}
	}
	if addr := os.Getenv("SYNAPSE_REDIS_ADDR"); addr != "" {
		rdb := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("SYNAPSE_REDIS_PASSWORD")})
		n, release, err := nodeid.Lease(ctx, rdb, 30*time.Second)
		if err == nil {
			log.Info("node id leased from Redis", "node_id", n)
			return n, func() { release(); _ = rdb.Close() }
		}
		log.Warn("node-id lease failed; falling back to hostname", "err", err)
		_ = rdb.Close()
	}
	host, _ := os.Hostname()
	var h uint32 = 2166136261
	for i := 0; i < len(host); i++ {
		h ^= uint32(host[i])
		h *= 16777619
	}
	n := int64(h % 1024)
	log.Warn("node id derived from hostname — set SYNAPSE_NODE_ID or SYNAPSE_REDIS_ADDR to guarantee uniqueness",
		"host", host, "node_id", n)
	return n, func() {}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
