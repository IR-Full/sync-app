// Command server is the all-in-one runner for the SyncApp MVP. It wires the
// domain services, the event bus, and the realtime gateway into one process
// (a "modular monolith" that is already split along the service boundaries from
// Section 6, so pieces can be peeled into separate deployables later).
//
// Backends are selected by environment. With none set it runs fully in-memory
// (great for `go run` and demos). Set the DSNs to use the Docker infra:
//
//	SYNCAPP_PG_DSN     postgres://... (enables durable storage)
//	SYNCAPP_REDIS_ADDR host:6379      (enables Redis presence)
//	SYNCAPP_NATS_URL   nats://...      (enables NATS event bus)
//	SYNCAPP_TCP_ADDR   default :7000   (raw-TCP binary protocol)
//	SYNCAPP_WS_ADDR    default :8080   (WebSocket + /healthz)
//	SYNCAPP_NODE_ID    default 1       (snowflake node id, 0..1023)
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

	"github.com/SyncApp-chat/SyncApp/internal/audit"
	"github.com/SyncApp-chat/SyncApp/internal/auth"
	"github.com/SyncApp-chat/SyncApp/internal/billing"
	"github.com/SyncApp-chat/SyncApp/internal/call"
	"github.com/SyncApp-chat/SyncApp/internal/chat"
	"github.com/SyncApp-chat/SyncApp/internal/contact"
	"github.com/SyncApp-chat/SyncApp/internal/delivery"
	"github.com/SyncApp-chat/SyncApp/internal/envcfg"
	"github.com/SyncApp-chat/SyncApp/internal/fanout"
	"github.com/SyncApp-chat/SyncApp/internal/gateway"
	"github.com/SyncApp-chat/SyncApp/internal/invite"
	"github.com/SyncApp-chat/SyncApp/internal/keydir"
	"github.com/SyncApp-chat/SyncApp/internal/media"
	"github.com/SyncApp-chat/SyncApp/internal/message"
	"github.com/SyncApp-chat/SyncApp/internal/moderation"
	"github.com/SyncApp-chat/SyncApp/internal/nodeid"
	"github.com/SyncApp-chat/SyncApp/internal/notify"
	"github.com/SyncApp-chat/SyncApp/internal/outbox"
	"github.com/SyncApp-chat/SyncApp/internal/pin"
	"github.com/SyncApp-chat/SyncApp/internal/platform"
	"github.com/SyncApp-chat/SyncApp/internal/poll"
	"github.com/SyncApp-chat/SyncApp/internal/presence"
	"github.com/SyncApp-chat/SyncApp/internal/reaction"
	"github.com/SyncApp-chat/SyncApp/internal/replay"
	"github.com/SyncApp-chat/SyncApp/internal/router"
	"github.com/SyncApp-chat/SyncApp/internal/schedule"
	"github.com/SyncApp-chat/SyncApp/internal/search"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/internal/store/memory"
	"github.com/SyncApp-chat/SyncApp/internal/store/postgres"
	"github.com/SyncApp-chat/SyncApp/internal/tracing"
	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
	"github.com/SyncApp-chat/SyncApp/pkg/id"
	"github.com/SyncApp-chat/SyncApp/pkg/ratelimit"

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

	// Distributed tracing (no-op unless SYNCAPP_TRACE=stdout / OTLP configured).
	shutdownTracing, err := tracing.Init(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = shutdownTracing(context.Background()) }()

	nodeID, releaseNode, err := resolveNodeID(ctx, log)
	if err != nil {
		return err
	}
	defer releaseNode()
	ids, err := id.NewGenerator(nodeID)
	if err != nil {
		return err
	}

	// Region is a multi-region hook: in a multi-region deployment each region
	// runs its own gateway pool + data plane, users connect to the nearest, and
	// chats are home-region pinned (cross-region traffic flows over the event
	// bus). Here it is informational, stamped into logs for correlation.
	region := env("SYNCAPP_REGION", "local")
	log = log.With("region", region, "node", nodeID)

	// --- storage ---
	var stores store.Stores
	if dsn := envcfg.Get("SYNCAPP_PG_DSN"); dsn != "" {
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
		log.Info("storage: in-memory (set SYNCAPP_PG_DSN for durable storage)")
	}

	// --- event bus ---
	var bus eventbus.Bus
	if url := envcfg.Get("SYNCAPP_NATS_URL"); url != "" {
		bus, err = eventbus.NewNATS(url)
		if err != nil {
			return err
		}
		log.Info("eventbus: nats", "url", url)
	} else {
		bus = eventbus.NewMemory()
		log.Info("eventbus: in-memory")
	}
	defer func() { _ = bus.Close() }() // process is exiting; nothing to recover

	// --- presence backend + cross-node router + resume buffer ---
	var pbackend presence.Backend
	var rtr router.Router
	var replayBuf replay.Buffer
	if addr := envcfg.Get("SYNCAPP_REDIS_ADDR"); addr != "" {
		pbackend, err = presence.NewRedisBackend(addr, envcfg.Get("SYNCAPP_REDIS_PASSWORD"), 0)
		if err != nil {
			return err
		}
		rdb := redis.NewClient(&redis.Options{Addr: addr, Password: envcfg.Get("SYNCAPP_REDIS_PASSWORD")})
		defer func() { _ = rdb.Close() }()
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
	authSvc := auth.New(stores.Users, stores.Sessions, ids).
		// The second factor. Optional at the service level so a deployment without
		// the store keeps single-factor login rather than a half-built one.
		WithTwoFactor(stores.TwoFactor)
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

	// Presence is gated on the sender's privacy setting. The gate is built from
	// the user directory and the address book, which fanout has no other reason
	// to know about — so it asks through a one-method interface and the gateway
	// answers (internal/gateway/presence_audience.go).
	fan := fanout.New(bus, chatSvc, rtr, log).
		WithPresenceAudience(gateway.NewPresenceAudience(gateway.Services{
			Users:    stores.Users,
			Contacts: contactSvc,
		})).
		// And notifications are gated on the recipient's mute setting. The column
		// has existed since the first migration with nothing reading it, so until
		// this line muting a chat did nothing at all.
		WithMuteChecker(chatSvc).
		// Message text reaches the push provider only for accounts that asked for
		// it. Without this the payload carried a preview of every message to
		// Apple/Google — the server volunteering the plaintext that E2E exists to
		// keep from it.
		WithPreviewPolicy(gateway.NewPreviewPolicy(stores.Users))
	if err := fan.Start(); err != nil {
		return err
	}

	// Transactional-outbox relay: drains staged message events to the bus so a
	// crash between DB commit and publish cannot lose an event.
	go outbox.New(stores.Outbox, bus, log).Run(ctx)

	// Search indexer (consumes message events). Shared Postgres index when a DSN
	// is set (visible across nodes), else in-memory.
	var searchBackend search.Backend
	if dsn := envcfg.Get("SYNCAPP_PG_DSN"); dsn != "" {
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
		notify.ProviderFor(envcfg.Get("SYNCAPP_PUSH_ENDPOINT"), envcfg.Get("SYNCAPP_PUSH_KEY"), log), log).
		WithDevices(notify.StoreDevices{Users: stores.Users})
	if err := notifySvc.Start(); err != nil {
		return err
	}

	// --- listeners ---
	tcpAddr := env("SYNCAPP_TCP_ADDR", ":7000")
	wsAddr := env("SYNCAPP_WS_ADDR", ":8080")

	// Production preflight. Runs before anything is built: a deployment that
	// declares itself production and is not safe to ship should fail in the first
	// second, not after opening a database pool and binding three listeners.
	if err := platform.EnforceProduction(func(msg string, args ...any) { log.Warn(msg, args...) }); err != nil {
		return err
	}

	// Media service (needs the public base URL for signed links).
	mediaDir := env("SYNCAPP_MEDIA_DIR", "./data/media")
	fsStore, err := media.NewFSStore(mediaDir)
	if err != nil {
		return err
	}
	publicBase := env("SYNCAPP_PUBLIC_URL", "http://localhost"+wsAddr)
	mediaSvc := media.New(fsStore, ids, platform.MediaSecret(), publicBase).
		WithLogger(log).
		// The deployment ceiling, above which no TIER may go. Default 4 GiB, which
		// is the largest tier — per-account limits come from entitlements now, so
		// this only has to be big enough not to contradict them. Lower it when the
		// disk behind the object store says so.
		WithMaxSize(int64(envcfg.Int("SYNCAPP_MEDIA_MAX_BYTES", 0)))
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
	if addr := envcfg.Get("SYNCAPP_REDIS_ADDR"); addr != "" {
		rdb := redis.NewClient(&redis.Options{Addr: addr, Password: envcfg.Get("SYNCAPP_REDIS_PASSWORD")})
		defer func() { _ = rdb.Close() }()
		keyDir = keydir.NewRedis(rdb, log)
	} else {
		keyDir = keydir.NewMemory()
	}

	// Expensive per-user actions share one budget across a user's connections —
	// and across nodes when Redis is present, which is the only place a limit can
	// live if a user's second connection lands on a different pod.
	var userLimits ratelimit.Shared
	if addr := envcfg.Get("SYNCAPP_REDIS_ADDR"); addr != "" {
		rdb := redis.NewClient(&redis.Options{Addr: addr, Password: envcfg.Get("SYNCAPP_REDIS_PASSWORD")})
		defer func() { _ = rdb.Close() }()
		userLimits = ratelimit.NewRedisShared(rdb, "user", 2, 20)
		log.Info("per-user limits: redis (shared across nodes)")
	}

	gwCfg := gateway.DefaultConfig()
	gwCfg.NodeID = strconv.FormatInt(nodeID, 10)
	if v := envcfg.Get("SYNCAPP_SEND_RATE"); v != "" {
		if f, e := strconv.ParseFloat(v, 64); e == nil {
			gwCfg.SendRate, gwCfg.SendBurst = f, f*2
		}
	}
	if origins := envcfg.Get("SYNCAPP_ALLOWED_ORIGINS"); origins != "" {
		gwCfg.AllowedOrigins = strings.Split(origins, ",")
	}
	// Per-IP accept guard (connection-flood / reconnect-storm defense). Off by
	// default so dev and tests are unaffected; set both in production.
	if v := envcfg.Get("SYNCAPP_MAX_CONNS_PER_IP"); v != "" {
		if n, e := strconv.Atoi(v); e == nil {
			gwCfg.MaxConnsPerIP = n
		}
	}
	if v := envcfg.Get("SYNCAPP_ACCEPT_RATE_PER_IP"); v != "" {
		if f, e := strconv.ParseFloat(v, 64); e == nil {
			gwCfg.AcceptRatePerIP = f
		}
	}
	// Hops allowed to speak for a client through X-Forwarded-For. Unset means the
	// header is ignored, which is right for a direct deployment and wrong for one
	// behind an ingress — where, unset, the per-IP guard sees one address for the
	// whole internet.
	if v := envcfg.Get("SYNCAPP_TRUSTED_PROXIES"); v != "" {
		gwCfg.TrustedProxies = strings.Split(v, ",")
	}
	if admins := envcfg.Get("SYNCAPP_ADMIN_USERS"); admins != "" {
		gwCfg.AdminUsers = strings.Split(admins, ",")
	}
	if mods := envcfg.Get("SYNCAPP_MODERATOR_USERS"); mods != "" {
		gwCfg.ModeratorUsers = strings.Split(mods, ",")
	}
	// --- billing ---
	//
	// Optional by construction: with no acquirer credentials configured the service
	// still runs and every account is on the free tier. That is a coherent
	// deployment — a self-hosted instance with no payments — rather than a broken
	// one, which is why the gateway reads ENTITLEMENTS rather than asking whether
	// billing exists.
	var billingSvc *billing.Service
	if stores.Billing != nil {
		billingSvc = billing.New(stores.Billing, bus, ids, log)
		if shop := envcfg.Get("SYNCAPP_YOOKASSA_SHOP_ID"); shop != "" {
			billingSvc = billingSvc.WithProvider(&billing.YooKassa{
				Endpoint:      envcfg.GetDefault("SYNCAPP_YOOKASSA_ENDPOINT", "https://api.yookassa.ru/v3/payments"),
				ShopID:        shop,
				SecretKey:     envcfg.Get("SYNCAPP_YOOKASSA_SECRET"),
				WebhookSecret: envcfg.Get("SYNCAPP_YOOKASSA_WEBHOOK_SECRET"),
			})
			log.Info("billing: yookassa enabled (card + sbp)")
		}
		if key := envcfg.Get("SYNCAPP_STRIPE_SECRET"); key != "" {
			billingSvc = billingSvc.WithProvider(&billing.Stripe{
				Endpoint:      envcfg.GetDefault("SYNCAPP_STRIPE_ENDPOINT", "https://api.stripe.com/v1/payment_intents"),
				SecretKey:     key,
				WebhookSecret: envcfg.Get("SYNCAPP_STRIPE_WEBHOOK_SECRET"),
			})
			log.Info("billing: stripe enabled (card)")
		}
		// The expiry sweep closes lapsed subscriptions and re-announces entitlements.
		// Without it a cancelled plan keeps granting access until somebody notices.
		go billingSvc.RunExpiry(ctx)
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
		SecretQ:    stores.SecretQ,
		IDs:        ids,
		Media:      mediaSvc,
		Billing:    billingSvc,
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
	// Collect undelivered secret envelopes past their TTL. Every queued row is a
	// record of who messaged whom and when, so the collector is part of the
	// privacy promise rather than storage hygiene — it runs whether or not the
	// queue is under pressure.
	go gw.RunSecretQueueCollector(ctx)

	// TLS terminates at the gateway edge; the custom protocol rides inside it.
	tlsConf, err := platform.BuildTLSConfig(log)
	if err != nil {
		return err
	}
	// Belt and braces behind the preflight: that one reads the environment, this
	// one checks what was actually built. They can only disagree if BuildTLSConfig
	// grows a path the preflight does not know about, which is exactly when a
	// second check earns its keep.
	if platform.RequireTLS() && tlsConf == nil {
		return fmt.Errorf("SYNCAPP_REQUIRE_TLS=1 but TLS is not configured; set SYNCAPP_TLS_CERT/KEY (or SYNCAPP_TLS_SELFSIGNED=1 for dev)")
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
	// SYNCAPP_QUIC=1; gives mobile clients connection migration + no HOL blocking.
	if tlsConf != nil && envcfg.Get("SYNCAPP_QUIC") == "1" {
		go func() {
			log.Info("gateway listening (quic)", "addr", tcpAddr)
			if err := gw.ServeQUIC(ctx, tcpAddr, tlsConf); err != nil {
				log.Error("quic serve", "err", err)
			}
		}()
	} else if envcfg.Get("SYNCAPP_QUIC") == "1" {
		log.Warn("SYNCAPP_QUIC=1 ignored: QUIC requires TLS (set SYNCAPP_TLS_* or SYNCAPP_TLS_SELFSIGNED=1)")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", gw.ServeWS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/metrics", promhttp.Handler()) // Prometheus scrape target
	mediaSvc.RegisterHTTP(mux)                 // /media/upload/*, /media/download/*
	if billingSvc != nil {
		// Provider callbacks. The one HTTP surface here that is unauthenticated by
		// construction — an acquirer has no credential of ours to present — so the
		// only thing between a stranger and a free subscription is the signature on
		// the body. See internal/billing/http.go.
		billing.NewHandler(billingSvc, log).Register(mux)
	}
	if envcfg.Get("SYNCAPP_PPROF") == "1" {
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
//  1. explicit SYNCAPP_NODE_ID (e.g. a Kubernetes StatefulSet ordinal);
//  2. a distributed lease from Redis (guarantees uniqueness across instances);
//  3. a hostname-derived id — DEV ONLY (see below).
//
// The node id is 10 bits of every snowflake this process mints, so two instances
// that hash to the same value mint colliding message and session ids. With 1024
// slots that is likely long before a thousand nodes (~50% at 38), and duplicate
// ids are silent corruption rather than a degraded mode — so a deployment that
// declares itself production (SYNCAPP_REQUIRE_TLS=1) gets an error instead of a
// guess.
//
// It returns the id and a release func (no-op unless a lease was taken).
func resolveNodeID(ctx context.Context, log *slog.Logger) (int64, func(), error) {
	if v := envcfg.Get("SYNCAPP_NODE_ID"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 || n > 1023 {
			return 0, nil, fmt.Errorf("invalid SYNCAPP_NODE_ID %q: want an integer in 0..1023", v)
		}
		return n, func() {}, nil
	}
	leaseAttempted := false
	if addr := envcfg.Get("SYNCAPP_REDIS_ADDR"); addr != "" {
		leaseAttempted = true
		rdb := redis.NewClient(&redis.Options{Addr: addr, Password: envcfg.Get("SYNCAPP_REDIS_PASSWORD")})
		n, release, err := nodeid.Lease(ctx, rdb, 30*time.Second)
		if err == nil {
			log.Info("node id leased from Redis", "node_id", n)
			return n, func() { release(); _ = rdb.Close() }, nil
		}
		log.Error("node-id lease failed", "err", err)
		_ = rdb.Close()
	}
	if platform.RequireTLS() {
		if leaseAttempted {
			return 0, nil, errors.New("node-id lease failed and SYNCAPP_REQUIRE_TLS=1: refusing to guess a node id from the hostname (would risk colliding snowflake ids); fix Redis or set SYNCAPP_NODE_ID")
		}
		return 0, nil, errors.New("SYNCAPP_REQUIRE_TLS=1 requires an explicit SYNCAPP_NODE_ID or SYNCAPP_REDIS_ADDR for a unique node id")
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
	return n, func() {}, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
