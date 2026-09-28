// Command gatewayd is the realtime edge in the microservice topology. It owns
// client connections (TCP/WebSocket/QUIC) and the protocol/handshake, but holds
// no domain logic: it dials the auth/chat/message/presence/keydir services over
// gRPC and wires their clients into the same gateway.Services interfaces the
// monolith uses. Fanout, outbox, search indexing, moderation and push run as
// their own workers (cmd/fanoutd, …). Media URL signing and the search QUERY path
// stay local (stateless crypto / shared-index read); blob serving is mediad.
package main

import (
	"context"
	"crypto/tls"
	"errors"
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

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"

	"github.com/SyncApp-chat/SyncApp/internal/audit"
	"github.com/SyncApp-chat/SyncApp/internal/call"
	"github.com/SyncApp-chat/SyncApp/internal/contact"
	"github.com/SyncApp-chat/SyncApp/internal/delivery"
	"github.com/SyncApp-chat/SyncApp/internal/envcfg"
	"github.com/SyncApp-chat/SyncApp/internal/gateway"
	"github.com/SyncApp-chat/SyncApp/internal/media"
	"github.com/SyncApp-chat/SyncApp/internal/platform"
	"github.com/SyncApp-chat/SyncApp/internal/poll"
	"github.com/SyncApp-chat/SyncApp/internal/reaction"
	"github.com/SyncApp-chat/SyncApp/internal/rpc"
	"github.com/SyncApp-chat/SyncApp/internal/search"
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

	b, err := platform.Load(ctx, log)
	if err != nil {
		return err
	}
	defer b.Close()
	log = b.Log

	// Production preflight. gatewayd IS the client edge in the split topology —
	// it terminates the WebSocket upgrade and signs media URLs — so it is subject
	// to exactly the policy the monolith is, and for the same reasons. It ran only
	// in cmd/server for a long time, which meant the deployment people actually
	// roll out was the one with no preflight at all.
	//
	// It runs after Load and before anything is dialled or bound: a config that is
	// not safe to ship should fail in the first second, not after five gRPC
	// connections and two listeners.
	if err := platform.EnforceProduction(func(msg string, args ...any) { log.Warn(msg, args...) }); err != nil {
		return err
	}

	// Dial the domain services. Each client satisfies the gateway's interfaces.
	dial := func(envKey, def, name string) (*grpc.ClientConn, error) {
		addr := platform.Env(envKey, def)
		conn, err := platform.Dial(addr, name, log)
		if err == nil {
			log.Info("dialed service", "name", name, "addr", addr)
		}
		return conn, err
	}
	conns := map[string]*grpc.ClientConn{}
	for _, d := range []struct{ env, def, name string }{
		{"SYNCAPP_AUTHD_ADDR", "localhost:9001", "authd"},
		{"SYNCAPP_CHATD_ADDR", "localhost:9002", "chatd"},
		{"SYNCAPP_MESSAGED_ADDR", "localhost:9003", "messaged"},
		{"SYNCAPP_PRESENCED_ADDR", "localhost:9004", "presenced"},
		{"SYNCAPP_KEYDIRD_ADDR", "localhost:9005", "keydird"},
	} {
		conn, err := dial(d.env, d.def, d.name)
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()
		conns[d.name] = conn
	}

	chatClient := rpc.NewChatClient(conns["chatd"])
	msgClient := rpc.NewMessageClient(conns["messaged"])

	// Local edge pieces: connection hub, media signing, search query, audit.
	hub := delivery.NewHub()
	wsAddr := platform.Env("SYNCAPP_WS_ADDR", ":8080")
	tcpAddr := platform.Env("SYNCAPP_TCP_ADDR", ":7000")

	mediaDir := platform.Env("SYNCAPP_MEDIA_DIR", "./data/media")
	fsStore, err := media.NewFSStore(mediaDir)
	if err != nil {
		return err
	}
	publicBase := platform.Env("SYNCAPP_PUBLIC_URL", "http://localhost"+wsAddr)
	mediaSvc := media.New(fsStore, b.IDs, platform.MediaSecret(), publicBase).
		// See cmd/server: the deployment ceiling, above which no tier may go.
		WithMaxSize(int64(envcfg.Int("SYNCAPP_MEDIA_MAX_BYTES", 0)))

	// Search QUERY path reads the shared index; indexing runs in searchd.
	var searchBackend search.Backend
	if dsn := envcfg.Get("SYNCAPP_PG_DSN"); dsn != "" {
		searchBackend, err = search.NewPostgresBackend(ctx, dsn)
		if err != nil {
			return err
		}
	} else {
		searchBackend = search.NewMemoryBackend()
	}
	searchSvc := search.New(searchBackend, chatClient, log)

	gwCfg := buildGatewayConfig(b.NodeID, log)

	gw := gateway.New(gateway.Services{
		Auth:     rpc.NewAuthClient(conns["authd"]),
		Chat:     chatClient,
		Msg:      msgClient,
		Broker:   msgClient,
		Presence: rpc.NewPresenceClient(conns["presenced"]),
		// Reactions are a thin store+bus operation; the edge runs them directly
		// against the shared store rather than paying an extra RPC hop. Membership
		// is still authorized through chatd.
		Reactor:  reaction.New(b.Stores.Reactions, chatClient, b.Bus),
		Calls:    call.New(b.Stores.Calls, chatClient, b.Bus, b.IDs),
		Polls:    poll.New(b.Stores.Polls, chatClient, b.Bus, b.IDs),
		Contacts: contact.New(b.Stores.Contacts, b.Stores.Users),
		// @username → user, plus the profile read/write behind PROFILE_GET/SET.
		// A user-directory RPC on authd is the follow-up that would take the edge
		// out of the account table.
		Users:  b.Stores.Users,
		Hub:    hub,
		KeyDir: rpc.NewKeyDirClient(conns["keydird"], log),
		Media:  mediaSvc,
		Search: searchSvc,
		Audit:  audit.NewLogSink(log),
		Bus:    b.Bus,
		Router: b.Router,
		Replay: b.Replay,
	}, gwCfg, log)
	if err := gw.StartDelivery(); err != nil {
		return err
	}

	tlsConf, err := platform.BuildTLSConfig(log)
	if err != nil {
		return err
	}
	// Belt and braces behind the preflight: that one reads the environment, this
	// one checks what was actually built. platform.RequireTLS rather than a
	// literal comparison against "1" — the flag is also spelled "true"/"yes"
	// everywhere else, and a check that recognised only one of the three made
	// SYNCAPP_REQUIRE_TLS=true mean production for the node-id guard and
	// development for this one, inside a single process.
	if platform.RequireTLS() && tlsConf == nil {
		return errors.New("SYNCAPP_REQUIRE_TLS is set but TLS is not configured; set SYNCAPP_TLS_CERT/KEY")
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
	if tlsConf != nil && envcfg.Get("SYNCAPP_QUIC") == "1" {
		go func() {
			if err := gw.ServeQUIC(ctx, tcpAddr, tlsConf); err != nil {
				log.Error("quic serve", "err", err)
			}
		}()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", gw.ServeWS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.Handle("/metrics", promhttp.Handler())
	mediaSvc.RegisterHTTP(mux)
	if envcfg.Get("SYNCAPP_PPROF") == "1" {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	}
	httpSrv := &http.Server{Addr: wsAddr, Handler: mux, TLSConfig: tlsConf, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Info("gateway listening (ws)", "addr", wsAddr, "tls", tlsConf != nil)
		var serveErr error
		if tlsConf != nil {
			serveErr = httpSrv.ListenAndServeTLS("", "")
		} else {
			serveErr = httpSrv.ListenAndServe()
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Error("http serve", "err", serveErr)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	gw.Shutdown()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	return nil
}

// buildGatewayConfig assembles the gateway config from the environment (mirrors
// cmd/server so the edge behaves identically whether monolithic or split).
func buildGatewayConfig(nodeID int64, log *slog.Logger) gateway.Config {
	cfg := gateway.DefaultConfig()
	cfg.NodeID = strconv.FormatInt(nodeID, 10)
	if v := envcfg.Get("SYNCAPP_SEND_RATE"); v != "" {
		if f, e := strconv.ParseFloat(v, 64); e == nil {
			cfg.SendRate, cfg.SendBurst = f, f*2
		}
	}
	if origins := envcfg.Get("SYNCAPP_ALLOWED_ORIGINS"); origins != "" {
		cfg.AllowedOrigins = strings.Split(origins, ",")
	}
	if v := envcfg.Get("SYNCAPP_MAX_CONNS_PER_IP"); v != "" {
		if n, e := strconv.Atoi(v); e == nil {
			cfg.MaxConnsPerIP = n
		}
	}
	if v := envcfg.Get("SYNCAPP_ACCEPT_RATE_PER_IP"); v != "" {
		if f, e := strconv.ParseFloat(v, 64); e == nil {
			cfg.AcceptRatePerIP = f
		}
	}
	if admins := envcfg.Get("SYNCAPP_ADMIN_USERS"); admins != "" {
		cfg.AdminUsers = strings.Split(admins, ",")
	}
	if mods := envcfg.Get("SYNCAPP_MODERATOR_USERS"); mods != "" {
		cfg.ModeratorUsers = strings.Split(mods, ",")
	}
	return cfg
}
