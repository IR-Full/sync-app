// Package gateway is the Realtime Gateway (Sections 2, 6, 10). It terminates
// client connections over raw TCP and over WebSocket, speaks the custom binary
// protocol, runs the handshake/auth/resume state machine, enforces per-connection
// sequencing and backpressure, and bridges to the domain services. It holds no
// durable state of its own: everything authoritative lives in the services/stores,
// so gateway pods are disposable and horizontally scalable.
package gateway

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/synapse-chat/synapse/internal/audit"
	"github.com/synapse-chat/synapse/internal/delivery"
	"github.com/synapse-chat/synapse/internal/metrics"
	"github.com/synapse-chat/synapse/internal/router"
	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/ratelimit"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// pickUserLimits falls back to a node-local shared limiter. Node-local is still
// a real limit: it charges every connection of a user to one budget, which is
// the abuse the per-connection bucket cannot see.
func pickUserLimits(s ratelimit.Shared) ratelimit.Shared {
	if s != nil {
		return s
	}
	return ratelimit.NewLocalShared(2, 20)
}

// roleOf returns a user's platform role ("" if none).
func (g *Gateway) roleOf(userID string) Role { return g.roles[userID] }

// isAdmin reports whether a user holds the platform-admin role.
func (g *Gateway) isAdmin(userID string) bool { return g.roles[userID] == RoleAdmin }

// canExportAny reports whether a user may export ANY chat (admin or moderator).
// Chat owners can always export their own chat regardless of platform role.
func (g *Gateway) canExportAny(userID string) bool {
	r := g.roles[userID]
	return r == RoleAdmin || r == RoleModerator
}

// StartDelivery subscribes this node to its delivery subject on the bus, so
// events routed to it by fanout (or another node) reach local connections. Must
// be called once after New if Bus and Router are set.
func (g *Gateway) StartDelivery() error {
	if g.svc.Bus == nil || g.cfg.NodeID == "" {
		return nil // single-process without cross-node bus wiring
	}
	return g.svc.Bus.Subscribe(router.DeliverSubject(g.cfg.NodeID), "", func(_ context.Context, e eventbus.Event) error {
		nd, err := router.DecodeNodeDelivery(e.Data)
		if err != nil {
			return err
		}
		d := delivery.Delivery{Type: wire.MsgType(nd.Type), Body: nd.Body}
		if nd.DeviceID != "" {
			g.svc.Hub.RouteDevice(nd.UserID, nd.DeviceID, d)
		} else {
			g.svc.Hub.Route(nd.UserID, d)
		}
		return nil
	})
}

// routeToUser publishes a node-targeted delivery to every node holding userID's
// connections (used by the secret-chat relay). Returns nodes reached.
func (g *Gateway) routeToUser(ctx context.Context, userID, deviceID string, typ wire.MsgType, body []byte) int {
	if g.svc.Router == nil || g.svc.Bus == nil {
		return 0
	}
	nodes, err := g.svc.Router.NodesFor(ctx, userID)
	if err != nil {
		return 0
	}
	nd := router.NodeDelivery{UserID: userID, DeviceID: deviceID, Type: uint16(typ), Body: body}
	data := nd.Encode()
	for _, node := range nodes {
		_ = g.svc.Bus.Publish(ctx, eventbus.Event{Subject: router.DeliverSubject(node), Key: userID, Data: data})
	}
	return len(nodes)
}

// audit records a security-relevant event if an audit sink is configured.
func (g *Gateway) audit(ctx context.Context, action, actor, target, detail string) {
	if g.svc.Audit != nil {
		g.svc.Audit.Record(ctx, audit.Event{Action: action, Actor: actor, Target: target, Detail: detail})
	}
}

// New builds a gateway.
func New(svc Services, cfg Config, log *slog.Logger) *Gateway {
	g := &Gateway{
		svc: svc,
		cfg: cfg,
		log: log,
		up: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			// Restrict WebSocket upgrades to configured origins (cross-site
			// WebSocket hijacking defense). Empty list = allow any (dev only).
			CheckOrigin: func(r *http.Request) bool {
				if len(cfg.AllowedOrigins) == 0 {
					return true
				}
				origin := r.Header.Get("Origin")
				for _, o := range cfg.AllowedOrigins {
					if o == origin {
						return true
					}
				}
				return false
			},
		},
		// ~1 attempt/sec sustained, burst of 5 per username.
		loginLimiter: ratelimit.NewLimiter(1, 5),
		// ~1 new chat/sec sustained, burst of 10 per user.
		newChatLimiter: ratelimit.NewLimiter(1, 10),
		// Node-local by default so a single-process run still charges limits to the
		// user rather than the socket; cmd/server swaps in the Redis-backed one when
		// there is more than one node to share the budget across.
		userLimits: pickUserLimits(svc.UserLimits),
		roles:      make(map[string]Role),
		reaperDone: make(chan struct{}),
	}
	if cfg.MaxConnsPerIP > 0 || cfg.AcceptRatePerIP > 0 {
		g.ipg = newIPGuard(cfg.AcceptRatePerIP, cfg.MaxConnsPerIP)
	}
	for _, u := range cfg.ModeratorUsers {
		g.roles[u] = RoleModerator
	}
	for _, u := range cfg.AdminUsers { // admin wins if listed in both
		g.roles[u] = RoleAdmin
	}
	return g
}

// Shutdown closes all live connections for a graceful drain. Call before
// stopping the HTTP/TCP listeners so clients get a clean close (and reconnect
// via resume elsewhere) instead of a truncated stream.
func (g *Gateway) Shutdown() {
	g.reaperOnce.Do(func() {}) // prevent a late reaper start after shutdown
	close(g.reaperDone)
	g.conns.Range(func(k, _ any) bool {
		k.(*conn).close()
		return true
	})
}

func (g *Gateway) track(c *conn) {
	g.conns.Store(c, struct{}{})
	g.reaperOnce.Do(func() { go g.reaper() })
}
func (g *Gateway) untrack(c *conn) { g.conns.Delete(c) }

// reaper is the single per-node liveness goroutine: every heartbeat interval it
// pings live connections and closes idle ones. It replaces a per-connection
// timer goroutine — one goroutine for the whole node instead of one per
// connection. The work is cheap and local (map walk + time compare + a
// non-blocking ping enqueue); Redis-bound presence/router refresh happens on
// client activity (conn.observe), not here, so this scales to millions of conns.
func (g *Gateway) reaper() {
	t := time.NewTicker(g.cfg.Heartbeat)
	defer t.Stop()
	for {
		select {
		case <-g.reaperDone:
			return
		case <-t.C:
			g.conns.Range(func(k, _ any) bool {
				k.(*conn).tick()
				return true
			})
		}
	}
}

// ServeTCP accepts raw-TCP clients on ln until the context is cancelled.
func (g *Gateway) ServeTCP(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	// Run several accept goroutines so accepting new connections is not a single
	// serialized bottleneck under connection storms (the OS load-balances Accept
	// across them). For multi-PROCESS scaling, bind the listener with SO_REUSEPORT
	// (see cmd/server) so each process gets its own accept queue on the same port.
	n := g.cfg.AcceptLoops
	if n < 1 {
		n = 1
	}
	errc := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() { errc <- g.acceptLoop(ctx, ln) }()
	}
	return <-errc
}

func (g *Gateway) acceptLoop(ctx context.Context, ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				g.log.Warn("tcp accept", "err", err)
				continue
			}
		}
		remote := c.RemoteAddr().String()
		host, ok := g.ipg.acquire(remote)
		if !ok {
			// Over the per-IP rate or concurrency cap: drop before handshaking so a
			// flood costs almost nothing (no goroutine, no read deadline held).
			metrics.ConnRejected.Inc()
			_ = c.Close()
			continue
		}
		go func() {
			defer g.ipg.release(host)
			g.serve(ctx, wire.NewTCPTransport(c), remote)
		}()
	}
}

// ServeWS is an http.Handler that upgrades to WebSocket and serves the client.
func (g *Gateway) ServeWS(w http.ResponseWriter, r *http.Request) {
	host, ok := g.ipg.acquire(r.RemoteAddr)
	if !ok {
		metrics.ConnRejected.Inc()
		http.Error(w, "too many connections", http.StatusTooManyRequests)
		return
	}
	c, err := g.up.Upgrade(w, r, nil)
	if err != nil {
		g.ipg.release(host)
		g.log.Warn("ws upgrade", "err", err)
		return
	}
	defer g.ipg.release(host)
	g.serve(r.Context(), wire.NewWSTransport(c), r.RemoteAddr)
}

// serve runs one connection lifecycle on a transport.
func (g *Gateway) serve(ctx context.Context, t wire.Transport, remote string) {
	cn := newConn(g, t, remote)
	cn.run(ctx)
}
