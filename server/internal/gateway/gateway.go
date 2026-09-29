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

	"github.com/SyncApp-chat/SyncApp/internal/audit"
	"github.com/SyncApp-chat/SyncApp/internal/billing"
	"github.com/SyncApp-chat/SyncApp/internal/delivery"
	"github.com/SyncApp-chat/SyncApp/internal/metrics"
	"github.com/SyncApp-chat/SyncApp/internal/router"
	"github.com/SyncApp-chat/SyncApp/internal/safego"
	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
	"github.com/SyncApp-chat/SyncApp/pkg/ratelimit"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
	"github.com/gorilla/websocket"
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
	// One goroutine per node turns written frames into delivery receipts.
	safego.Loop(g.reaperDone, g.log, "gateway.deliveryReporter", g.runDeliveryReporter)
	// Subscription changes are PUSHED to the account's devices.
	//
	// Not polled, because the change is not the client's doing: a payment settles or
	// a period lapses, and until the client hears about it it goes on offering
	// features the server has started refusing — which the user experiences as the
	// app breaking rather than as a plan ending.
	//
	// No queue group, so EVERY node receives it: the account may be connected on
	// several, and a queue group would deliver the news to exactly one of them.
	if err := g.svc.Bus.Subscribe(eventbus.SubjSubscription, "", g.onSubscriptionChange); err != nil {
		return err
	}
	return g.svc.Bus.Subscribe(router.DeliverSubject(g.cfg.NodeID), "", func(_ context.Context, e eventbus.Event) error {
		nd, err := router.DecodeNodeDelivery(e.Data)
		if err != nil {
			return err
		}
		// One delivery now names every recipient that lives on this node, so the
		// body crossed the bus once for all of them. The per-recipient Delivery is
		// still built per user: OnWritten reports a receipt on behalf of ONE
		// recipient, and sharing one callback across a group would credit the
		// whole page to whoever's frame happened to land first.
		// A secret frame is decoded ONCE per bus message, not once per recipient: it
		// is addressed to a single device, so there is one recipient anyway, and the
		// decode has to happen before the per-connection re-encode can.
		var secretBody *wire.SecretMsgBody
		if wire.MsgType(nd.Type) == wire.MsgSecretRecv {
			var sb wire.SecretMsgBody
			if err := wire.Unmarshal(nd.Body, &sb); err == nil {
				secretBody = &sb
			}
		}
		for _, userID := range nd.Users {
			d := delivery.Delivery{Type: wire.MsgType(nd.Type), Body: nd.Body}
			if secretBody != nil {
				// Re-encode for whichever socket this lands on. The relay normalised
				// the payload to bytes between nodes; a peer that did not negotiate
				// CapSecretQueue reads only the base64 fields, and handing it the
				// binary ones would deliver a message with no content in it.
				sb := *secretBody
				header, cipher, _ := wire.SecretPayload(sb)
				d.BodyFor = func(caps wire.Cap) any {
					out := sb
					wire.SetSecretPayloadFor(&out, caps, header, cipher)
					return out
				}
			}
			// A message is the one frame whose arrival the sender is entitled to
			// hear about, and this node is the only place that can witness it.
			if d.Type == wire.MsgNew {
				d.OnWritten = g.deliveryReporterFor(userID, nd.Body)
			}
			if nd.DeviceID != "" {
				g.svc.Hub.RouteDevice(userID, nd.DeviceID, d)
			} else {
				g.svc.Hub.Route(userID, d)
			}
		}
		return nil
	})
}

// routeToDevice publishes a delivery addressed at ONE device, and returns the
// number of nodes that actually hold a connection for it.
//
// Separate from routeToUser because the count is the point. The secret relay
// uses the answer to decide whether to queue the ciphertext, and the user-level
// lookup cannot support that decision: it reports a node whenever ANY of the
// account's devices is connected, so a message addressed to an offline laptop
// looked delivered because a phone was online, and was dropped.
//
// An empty deviceID means "every device of that user" and falls back to the
// user-level index, which is the correct answer for that address.
func (g *Gateway) routeToDevice(ctx context.Context, userID, deviceID string, typ wire.MsgType, body []byte) int {
	if g.svc.Router == nil || g.svc.Bus == nil {
		return 0
	}
	nodes, err := g.svc.Router.NodesForDevice(ctx, userID, deviceID)
	if err != nil {
		return 0
	}
	return g.publishToNodes(ctx, nodes, userID, deviceID, typ, body)
}

// routeToUser publishes a node-targeted delivery to every node holding userID's
// connections. Returns nodes reached.
func (g *Gateway) routeToUser(ctx context.Context, userID, deviceID string, typ wire.MsgType, body []byte) int {
	if g.svc.Router == nil || g.svc.Bus == nil {
		return 0
	}
	nodes, err := g.svc.Router.NodesFor(ctx, userID)
	if err != nil {
		return 0
	}
	return g.publishToNodes(ctx, nodes, userID, deviceID, typ, body)
}

// publishToNodes is the shared tail of both routes.
func (g *Gateway) publishToNodes(ctx context.Context, nodes []string, userID, deviceID string, typ wire.MsgType, body []byte) int {
	nd := router.NodeDelivery{Users: []string{userID}, DeviceID: deviceID, Type: uint16(typ), Body: body}
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
	// Gate media downloads on chat membership where the media service can accept
	// a gate. Done here because the answer is assembled from the chat service,
	// the user directory and the message log — all of which the gateway already
	// holds and the media service deliberately does not.
	//
	// A media service that does not accept an authorizer, or a deployment with no
	// way to resolve a blob to its chats, keeps the previous behaviour: the
	// unguessable ref plus the signed URL. Denying wholesale on a missing
	// capability would take media away rather than secure it.
	if gated, ok := svc.Media.(fetchGatedMedia); ok {
		gated.WithFetchAuthorizer(mediaAuthorizer{svc: &svc})
	}

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
		// user rather than the socket; internal/wiring passes the Redis-backed one
		// when there is more than one node to share the budget across.
		userLimits: pickUserLimits(svc.UserLimits),
		roles:      make(map[string]Role),
		reaperDone: make(chan struct{}),
		delivered:  make(chan deliveryReport, deliveredQueueDepth),
	}
	if cfg.MaxConnsPerIP > 0 || cfg.AcceptRatePerIP > 0 {
		g.ipg = newIPGuard(cfg.AcceptRatePerIP, cfg.MaxConnsPerIP)
	}
	trusted, bad := parseTrustedProxies(cfg.TrustedProxies)
	g.trustedProxies = trusted
	for _, entry := range bad {
		// Loud, because the failure mode is silent trust: an operator who meant to
		// trust their ingress and typed the CIDR wrong would otherwise see the
		// guard keep working — on the wrong address — with nothing to say so.
		log.Error("ignoring an unparseable trusted proxy entry", "entry", entry)
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
	g.reaperOnce.Do(func() {
		safego.Loop(g.reaperDone, g.log, "gateway.reaper", g.reaper)
	})
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
	// across them). For multi-process scaling on one host, bind the listener with
	// SO_REUSEPORT so each process gets its own accept queue on the same port.
	n := g.cfg.AcceptLoops
	if n < 1 {
		n = 1
	}
	errc := make(chan error, n)
	for i := 0; i < n; i++ {
		safego.Go(g.log, "gateway.acceptLoop", func() { errc <- g.acceptLoop(ctx, ln) })
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
		if behindProxy(c) {
			// The client address is in a PROXY header that has not been read yet;
			// reading it here would let one slow proxy connection stall accepts.
			safego.Go(g.log, "gateway.serveTCP", func() { g.guardAndServeTCP(ctx, c) })
			continue
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
		safego.Go(g.log, "gateway.serveTCP", func() {
			defer g.ipg.release(host)
			g.serve(ctx, wire.NewTCPTransport(c), remote)
		})
	}
}

// guardAndServeTCP applies the per-IP guard to a connection from a trusted
// proxy. RemoteAddr reads the PROXY header, so it runs before any handshake
// deadline is set.
func (g *Gateway) guardAndServeTCP(ctx context.Context, c net.Conn) {
	if err := proxyHeaderErr(c); err != nil {
		// Debug: a balancer health check connects without a header every few seconds.
		g.log.Debug("closing a proxied connection", "err", err)
		_ = c.Close()
		return
	}
	remote := c.RemoteAddr().String()
	host, ok := g.ipg.acquire(remote)
	if !ok {
		metrics.ConnRejected.Inc()
		_ = c.Close()
		return
	}
	defer g.ipg.release(host)
	g.serve(ctx, wire.NewTCPTransport(c), remote)
}

// ServeWS is an http.Handler that upgrades to WebSocket and serves the client.
func (g *Gateway) ServeWS(w http.ResponseWriter, r *http.Request) {
	// Behind a trusted proxy the peer address is the proxy's, so the guard would
	// charge every client on earth to one bucket. clientIP resolves the forwarded
	// address when — and only when — the hop it came from is one we configured.
	host, ok := g.ipg.acquire(clientIP(r.RemoteAddr, r.Header, g.trustedProxies))
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
	g.serve(r.Context(), wire.NewWSTransport(c), host)
}

// serve runs one connection lifecycle on a transport.
//
// This is the single funnel for all three transports (TCP, WebSocket, QUIC), and
// therefore the one place a per-connection panic guard belongs: a malformed frame
// that trips a nil dereference in a handler must cost that one connection, not
// every connection on the node. The client reconnects and resyncs via history.
func (g *Gateway) serve(ctx context.Context, t wire.Transport, remote string) {
	defer safego.Recover(g.log, "gateway.serve")
	cn := newConn(g, t, remote)
	cn.run(ctx)
}

// onSubscriptionChange pushes new entitlements to an account's local connections.
//
// It routes through the Hub directly rather than through the router and the bus: the
// event is already on every node, so re-publishing per node would multiply one
// change into one message per node per node.
func (g *Gateway) onSubscriptionChange(_ context.Context, e eventbus.Event) error {
	userID, ent, ok := billing.DecodeSubscriptionEvent(e.Data)
	if !ok {
		return nil
	}
	if g.svc.Hub == nil {
		return nil
	}
	g.svc.Hub.Route(userID, delivery.Delivery{
		Type: wire.MsgSubscription,
		Body: subscriptionToWire(nil, ent),
	})
	return nil
}
