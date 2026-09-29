package wiring

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/IR-Full/sync-app/server/internal/billing"
	"github.com/IR-Full/sync-app/server/internal/gateway"
	"github.com/IR-Full/sync-app/server/internal/platform"
)

// shutdownGrace bounds how long in-flight HTTP requests get on shutdown.
const shutdownGrace = 5 * time.Second

// Listeners are the edge's bound sockets; HTTP stays raw and Serve adds TLS to
// it. Binding is separate from serving so a caller (a test, or a supervisor) can
// learn the real addresses of ":0" binds.
type Listeners struct {
	TCP  net.Listener
	HTTP net.Listener
	TLS  *tls.Config
}

// Listen binds the TCP protocol port and the HTTP port, with TLS when it is
// configured. SYNCAPP_REQUIRE_TLS without a TLS configuration is an error here as
// well as in the preflight: the preflight reads the environment, this checks what
// was actually built.
func Listen(cfg Config, log *slog.Logger) (*Listeners, error) {
	tlsConf, err := platform.BuildTLSConfig(log)
	if err != nil {
		return nil, err
	}
	if platform.RequireTLS() && tlsConf == nil {
		return nil, errors.New("SYNCAPP_REQUIRE_TLS is set but TLS is not configured; set SYNCAPP_TLS_CERT/KEY (or SYNCAPP_TLS_SELFSIGNED=1 for dev)")
	}
	tcp, err := net.Listen("tcp", cfg.TCPAddr)
	if err != nil {
		return nil, err
	}
	httpLn, err := net.Listen("tcp", cfg.WSAddr)
	if err != nil {
		_ = tcp.Close()
		return nil, err
	}
	if cfg.ProxyProtocol {
		// Below TLS: the header precedes the handshake on the wire.
		tcp = gateway.NewProxyProtocolListener(tcp, cfg.Gateway.TrustedProxies)
	}
	if tlsConf != nil {
		tcp = tls.NewListener(tcp, tlsConf)
		// The HTTP listener stays raw: http.Server.ServeTLS wraps it and adds the
		// ALPN protocols itself.
	}
	return &Listeners{TCP: tcp, HTTP: httpLn, TLS: tlsConf}, nil
}

// Serve runs the gateway on the bound listeners until ctx is cancelled, then
// drains live connections before closing the HTTP server.
func Serve(ctx context.Context, cfg Config, gw *gateway.Gateway, edge *Edge, ln *Listeners, log *slog.Logger) error {
	go func() {
		log.Info("gateway listening (tcp)", "addr", ln.TCP.Addr().String(), "tls", ln.TLS != nil)
		if err := gw.ServeTCP(ctx, ln.TCP); err != nil {
			log.Error("tcp serve", "err", err)
		}
	}()

	// QUIC shares the TCP port number over UDP and needs TLS.
	switch {
	case cfg.QUIC && ln.TLS != nil:
		go func() {
			log.Info("gateway listening (quic)", "addr", cfg.TCPAddr)
			if err := gw.ServeQUIC(ctx, cfg.TCPAddr, ln.TLS); err != nil {
				log.Error("quic serve", "err", err)
			}
		}()
	case cfg.QUIC:
		log.Warn("SYNCAPP_QUIC ignored: QUIC requires TLS (set SYNCAPP_TLS_* or SYNCAPP_TLS_SELFSIGNED=1)")
	}

	srv := &http.Server{
		Handler: Mux(cfg, gw, edge, log),
		// Slow-loris defense for the WebSocket upgrade, mirroring the raw-TCP
		// handshake deadline.
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Info("gateway listening (ws)", "addr", ln.HTTP.Addr().String(), "path", "/ws", "tls", ln.TLS != nil)
		var err error
		if ln.TLS != nil {
			srv.TLSConfig = ln.TLS
			err = srv.ServeTLS(ln.HTTP, "", "")
		} else {
			err = srv.Serve(ln.HTTP)
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http serve", "err", err)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	gw.Shutdown() // clients get a clean close and resume elsewhere
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// Mux is the edge's HTTP surface.
func Mux(cfg Config, gw *gateway.Gateway, edge *Edge, log *slog.Logger) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", gw.ServeWS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/metrics", promhttp.Handler())
	edge.Media.RegisterHTTP(mux) // /media/upload/*, /media/download/*
	if edge.Billing != nil {
		// Acquirer callbacks: unauthenticated by construction, so each one is
		// verified against the acquirer's API. The source address is resolved
		// through the trusted proxies, so the address check sees the acquirer and
		// not the ingress.
		billing.NewHandler(edge.Billing, log).WithClientIP(gw.ClientIP).Register(mux)
	}
	if cfg.PProf {
		// Exposes internals: bind to an internal port or put it behind auth.
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		log.Warn("pprof enabled at /debug/pprof/ — do not expose publicly")
	}
	return mux
}
