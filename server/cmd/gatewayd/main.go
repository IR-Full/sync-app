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

	"github.com/synapse-chat/synapse/internal/audit"
	"github.com/synapse-chat/synapse/internal/call"
	"github.com/synapse-chat/synapse/internal/contact"
	"github.com/synapse-chat/synapse/internal/delivery"
	"github.com/synapse-chat/synapse/internal/gateway"
	"github.com/synapse-chat/synapse/internal/media"
	"github.com/synapse-chat/synapse/internal/platform"
	"github.com/synapse-chat/synapse/internal/poll"
	"github.com/synapse-chat/synapse/internal/reaction"
	"github.com/synapse-chat/synapse/internal/rpc"
	"github.com/synapse-chat/synapse/internal/search"
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
		{"SYNAPSE_AUTHD_ADDR", "localhost:9001", "authd"},
		{"SYNAPSE_CHATD_ADDR", "localhost:9002", "chatd"},
		{"SYNAPSE_MESSAGED_ADDR", "localhost:9003", "messaged"},
		{"SYNAPSE_PRESENCED_ADDR", "localhost:9004", "presenced"},
		{"SYNAPSE_KEYDIRD_ADDR", "localhost:9005", "keydird"},
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
	wsAddr := platform.Env("SYNAPSE_WS_ADDR", ":8080")
	tcpAddr := platform.Env("SYNAPSE_TCP_ADDR", ":7000")

	mediaDir := platform.Env("SYNAPSE_MEDIA_DIR", "./data/media")
	fsStore, err := media.NewFSStore(mediaDir)
	if err != nil {
		return err
	}
	publicBase := platform.Env("SYNAPSE_PUBLIC_URL", "http://localhost"+wsAddr)
	mediaSvc := media.New(fsStore, b.IDs, platform.MediaSecret(), publicBase)

	// Search QUERY path reads the shared index; indexing runs in searchd.
	var searchBackend search.Backend
	if dsn := os.Getenv("SYNAPSE_PG_DSN"); dsn != "" {
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
		Users:    b.Stores.Users, // @username → user (read; a user-directory RPC is a follow-up)
		Hub:      hub,
		KeyDir:   rpc.NewKeyDirClient(conns["keydird"], log),
		Media:    mediaSvc,
		Search:   searchSvc,
		Audit:    audit.NewLogSink(log),
		Bus:      b.Bus,
		Router:   b.Router,
		Replay:   b.Replay,
	}, gwCfg, log)
	if err := gw.StartDelivery(); err != nil {
		return err
	}

	tlsConf, err := platform.BuildTLSConfig(log)
	if err != nil {
		return err
	}
	if os.Getenv("SYNAPSE_REQUIRE_TLS") == "1" && tlsConf == nil {
		return errors.New("SYNAPSE_REQUIRE_TLS=1 but TLS is not configured")
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
	if tlsConf != nil && os.Getenv("SYNAPSE_QUIC") == "1" {
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
	if os.Getenv("SYNAPSE_PPROF") == "1" {
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
	if v := os.Getenv("SYNAPSE_SEND_RATE"); v != "" {
		if f, e := strconv.ParseFloat(v, 64); e == nil {
			cfg.SendRate, cfg.SendBurst = f, f*2
		}
	}
	if origins := os.Getenv("SYNAPSE_ALLOWED_ORIGINS"); origins != "" {
		cfg.AllowedOrigins = strings.Split(origins, ",")
	}
	if v := os.Getenv("SYNAPSE_MAX_CONNS_PER_IP"); v != "" {
		if n, e := strconv.Atoi(v); e == nil {
			cfg.MaxConnsPerIP = n
		}
	}
	if v := os.Getenv("SYNAPSE_ACCEPT_RATE_PER_IP"); v != "" {
		if f, e := strconv.ParseFloat(v, 64); e == nil {
			cfg.AcceptRatePerIP = f
		}
	}
	if admins := os.Getenv("SYNAPSE_ADMIN_USERS"); admins != "" {
		cfg.AdminUsers = strings.Split(admins, ",")
	}
	if mods := os.Getenv("SYNAPSE_MODERATOR_USERS"); mods != "" {
		cfg.ModeratorUsers = strings.Split(mods, ",")
	}
	return cfg
}
