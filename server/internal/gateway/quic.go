package gateway

import (
	"context"
	"crypto/tls"

	"github.com/quic-go/quic-go"
	"github.com/synapse-chat/synapse/internal/metrics"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// ServeQUIC accepts clients over QUIC/HTTP-3-style transport. QUIC is a better
// fit for mobile than TCP: the connection survives an IP change (WiFi↔LTE) via
// connection migration, so a phone changing networks does not reconnect or lose
// its session; there is no TCP head-of-line blocking; and the handshake (TLS 1.3
// built in) is faster. Each client opens one bidirectional stream carrying the
// same binary frame protocol as TCP/WS — only the stream source differs.
//
// ListenQUIC opens a QUIC listener (UDP). QUIC mandates TLS, so tlsConf must be
// non-nil (self-signed is fine for dev). The caller may read ln.Addr() before
// ServeQUIC (useful for ephemeral ports in tests).
func ListenQUIC(addr string, tlsConf *tls.Config, cfg Config) (*quic.Listener, error) {
	qtls := tlsConf.Clone()
	qtls.NextProtos = []string{QUICALPN}
	return quic.ListenAddr(addr, qtls, &quic.Config{
		MaxIdleTimeout:  cfg.IdleTimeout * 2,
		KeepAlivePeriod: cfg.Heartbeat,
	})
}

// ServeQUIC opens a listener at addr and serves it until ctx is cancelled.
func (g *Gateway) ServeQUIC(ctx context.Context, addr string, tlsConf *tls.Config) error {
	ln, err := ListenQUIC(addr, tlsConf, g.cfg)
	if err != nil {
		return err
	}
	return g.ServeQUICListener(ctx, ln)
}

// ServeQUICListener serves an already-opened QUIC listener.
func (g *Gateway) ServeQUICListener(ctx context.Context, ln *quic.Listener) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				g.log.Warn("quic accept", "err", err)
				continue
			}
		}
		remote := conn.RemoteAddr().String()
		host, ok := g.ipg.acquire(remote)
		if !ok {
			metrics.ConnRejected.Inc()
			_ = conn.CloseWithError(0, "rate limited")
			continue
		}
		go func() {
			defer g.ipg.release(host)
			g.serveQUICConn(ctx, conn)
		}()
	}
}

// serveQUICConn accepts the client's single protocol stream and serves it.
func (g *Gateway) serveQUICConn(ctx context.Context, conn quic.Connection) {
	stream, err := conn.AcceptStream(ctx)
	if err != nil {
		return
	}
	// quic.Stream satisfies wire.StreamConn (Read/Write/SetDeadline/Close).
	g.serve(ctx, wire.NewStreamTransport(stream), conn.RemoteAddr().String())
}
