package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/auth"
	"github.com/SyncApp-chat/SyncApp/internal/delivery"
	"github.com/SyncApp-chat/SyncApp/internal/metrics"
	"github.com/SyncApp-chat/SyncApp/internal/safego"
	"github.com/SyncApp-chat/SyncApp/pkg/ratelimit"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
)

// conn is one client connection. It implements delivery.Sink so fanout can push
// events to it. A connection has exactly one read goroutine (the state machine)
// and one write goroutine (drains outQueue); all socket writes go through the
// wire.Conn's internal mutex so the two never interleave a frame.
type conn struct {
	gw     *Gateway
	wc     *wire.Conn
	log    *slog.Logger
	remote string

	// identity (set after successful auth)
	userID    string
	deviceID  string
	sessionID string
	platform  string
	authed    bool
	peerCaps  wire.Cap
	inFlags   byte // compression flags the peer may send after auth (wire.Flag*)

	outSeq        atomic.Uint64 // server→client sequence
	lastClientSeq atomic.Uint64 // highest client Seq observed (piggyback ack)
	lastActivity  atomic.Int64  // unixnano of last inbound frame
	lastRefresh   atomic.Int64  // unixnano of last presence/router TTL refresh

	// QoS lanes: outbound frames are queued by priority and drained hi→mid→lo, so
	// control frames (pong/ack/error) never wait behind a fanout backlog. The lo
	// lane (typing/presence) is DROPPABLE under pressure — losing an ephemeral
	// "typing…" is fine; dropping an ack is not.
	outHi  chan delivery.Delivery // control: pong, error, send-ack, ping
	outMid chan delivery.Delivery // messages: new, read-receipt, secret, media, history
	outLo  chan delivery.Delivery // ephemeral: typing, presence (droppable)

	unregister func()
	done       chan struct{}
	closeOnce  sync.Once
	sendLimit  *ratelimit.Bucket // flood control on state-changing messages
	// readLimit meters the amplifying READS (history, chat list, the *_SYNC and
	// *_LIST families). A separate bucket rather than a share of sendLimit: writes
	// are rare and expensive to get wrong, reads are frequent and normal, and one
	// budget for both would either throttle scrolling or stop metering sends.
	readLimit *ratelimit.Bucket
	// Typing is throttled in two stages. The per-connection bucket is checked
	// FIRST, before the chat is resolved: it bounds the resolve work and, just as
	// importantly, bounds how many keys the per-chat limiter below can ever hold
	// (a client that could mint a bucket per made-up chat id would turn this
	// defense into a memory leak). The per-chat limiter then enforces the real
	// rule — one indicator per chat every couple of seconds.
	typingLimit     *ratelimit.Bucket
	typingChatLimit *ratelimit.Limiter
	signalLimit     *ratelimit.Bucket // call signaling relay (SDP/ICE)
}

// replayAppendTimeout bounds the resume-buffer write in writeLoop.
//
// Short on purpose. It sits on the single writer for a connection, so the value
// is not "how long may this reasonably take" but "how long may one connection's
// outbound path stall before the store's slowness becomes the user's problem".
const replayAppendTimeout = 250 * time.Millisecond

func newConn(g *Gateway, t wire.Transport, remote string) *conn {
	// Lane depths. MaxInflight is the backpressure window. The hi lane must also
	// hold MaxInflight: SendAcks ride it and correlate 1:1 with inbound sends, so a
	// legitimate send-burst produces an ack-burst that must not tear the connection
	// down. The lo lane is droppable (typing/presence), so a small fixed buffer is
	// enough — overflow just drops an ephemeral frame. (Buffered channels
	// pre-allocate their ring buffer, so lane depth is the dominant idle-connection
	// memory cost; the lo lane is the only one we can safely shrink here.)
	n := g.cfg.MaxInflight
	const ephemeralLane = 16
	wc := wire.NewConn(t, false) // compression enabled after Hello
	// Until the peer authenticates it gets small frames and no compression at all:
	// the compression flag is chosen by the sender, so otherwise a stranger's first
	// frame decides whether we run a decompressor on it.
	wc.SetInboundPolicy(preAuthMaxPayload, 0)
	return &conn{
		gw: g,
		wc: wc,
		// Truncated rather than verbatim: a log line outlives the connection, and a
		// file of addresses plus timestamps is a movement history whether or not
		// anyone set out to build one. See logaddr.go.
		log:             g.log.With("remote", logAddr(remote)),
		remote:          remote,
		outHi:           make(chan delivery.Delivery, n),
		outMid:          make(chan delivery.Delivery, n),
		outLo:           make(chan delivery.Delivery, ephemeralLane),
		done:            make(chan struct{}),
		sendLimit:       ratelimit.NewBucket(g.cfg.SendRate, g.cfg.SendBurst),
		readLimit:       ratelimit.NewBucket(g.cfg.ReadRate, g.cfg.ReadBurst),
		typingLimit:     ratelimit.NewBucket(g.cfg.TypingRate, g.cfg.TypingBurst),
		typingChatLimit: ratelimit.NewLimiter(g.cfg.TypingChatRate, g.cfg.TypingChatBurst),
		signalLimit:     ratelimit.NewBucket(g.cfg.SignalRate, g.cfg.SignalBurst),
	}
}

// --- delivery.Sink ---

// Send enqueues a push by QoS lane without blocking. A full hi/mid lane means the
// client cannot keep up with important frames → tear the connection down (it
// resyncs via history). A full lo lane just drops the ephemeral frame.
func (c *conn) Send(d delivery.Delivery) bool {
	lane, droppable := c.lane(d.Type)
	select {
	case lane <- d:
		return true
	default:
		if droppable {
			return true // ephemeral (typing/presence) — safe to drop under load
		}
		metrics.SlowConnDropped.Inc()
		c.log.Warn("outbound lane full; dropping slow connection", "user", logUser(c.userID))
		c.close()
		return false
	}
}

// lane classifies a message into its QoS lane.
func (c *conn) lane(t wire.MsgType) (ch chan delivery.Delivery, droppable bool) {
	switch t {
	case wire.MsgTyping, wire.MsgPresence:
		return c.outLo, true
	case wire.MsgPong, wire.MsgPing, wire.MsgError, wire.MsgSendAck, wire.MsgTransportAck:
		return c.outHi, false
	default:
		return c.outMid, false
	}
}

func (c *conn) DeviceID() string { return c.deviceID }

// run drives the full connection lifecycle.
func (c *conn) run(ctx context.Context) {
	defer c.close()
	c.touch()

	if err := c.handshake(ctx); err != nil {
		c.log.Info("handshake failed", "err", err)
		return
	}
	if err := c.authenticate(ctx); err != nil {
		c.log.Info("auth failed", "err", err)
		return
	}
	// Authenticated: full-size frames and the compression it negotiated.
	c.wc.SetInboundPolicy(wire.MaxPayloadSize, c.inFlags)

	c.log.Info("connection established", "user", logUser(c.userID), "device", logUser(c.deviceID))

	// Writer + liveness monitor run alongside the read loop.
	// Only two goroutines per connection: the read loop (below) and the write
	// loop. Liveness (idle-close + ping) is handled by ONE shared gateway reaper
	// instead of a timer goroutine per connection — at 1M connections that saves
	// 1M goroutines (~several GB of stack). Presence/router TTL refresh moved to
	// throttled on-activity in observe().
	// The writer is its own goroutine, so g.serve's guard does not cover it: a
	// panic while encoding an outbound frame would take the node down without one
	// of its own.
	safego.Go(c.log, "conn.writeLoop", c.writeLoop)

	c.readLoop(ctx)
}

// handshake reads Hello and replies Welcome, negotiating capabilities. A read
// deadline bounds how long an unauthenticated peer may hold the connection
// (slow-loris defense).
func (c *conn) handshake(ctx context.Context) error {
	_ = c.wc.SetReadDeadline(time.Now().Add(c.gw.cfg.HandshakeTimeout))
	e, err := c.wc.ReadEnvelope()
	if err != nil {
		return err
	}
	if e.Type != wire.MsgHello {
		return errors.New("expected HELLO")
	}
	var hello wire.HelloBody
	if err := wire.Unmarshal(e.Body, &hello); err != nil {
		return err
	}

	// Server-supported capabilities; the agreed set is the intersection.
	const serverCaps = wire.CapCompression | wire.CapZstd | wire.CapResume | wire.CapTypingSignals |
		wire.CapBatching | wire.CapSecretQueue
	agreed := hello.Caps & serverCaps
	c.peerCaps = agreed
	// Inbound compression the peer may use once authenticated: exactly what it
	// negotiated. Both, if it offered both — the choice of which to SEND is ours.
	if agreed&wire.CapZstd != 0 {
		c.inFlags |= wire.FlagZstd
	}
	if agreed&wire.CapCompression != 0 {
		c.inFlags |= wire.FlagCompressed
	}
	// Prefer zstd+dictionary when both sides support it; else gzip.
	if agreed&wire.CapZstd != 0 {
		c.wc.SetZstd(true)
	} else if agreed&wire.CapCompression != 0 {
		c.wc.SetCompression(true)
	}
	c.deviceID = hello.DeviceID
	c.platform = hello.Platform

	return c.wc.Send(wire.MsgWelcome, 0, 0, e.RequestID, wire.WelcomeBody{
		ServerVersion:   c.gw.cfg.ServerVersion,
		Caps:            agreed,
		HeartbeatMs:     int(c.gw.cfg.Heartbeat.Milliseconds()),
		MaxInflight:     c.gw.cfg.MaxInflight,
		ResumeSupported: agreed&wire.CapResume != 0,
	})
}

// authenticate reads AUTH (token or username/password) or RESUME and resolves
// the identity behind the connection.
func (c *conn) authenticate(ctx context.Context) error {
	_ = c.wc.SetReadDeadline(time.Now().Add(c.gw.cfg.HandshakeTimeout))
	e, err := c.wc.ReadEnvelope()
	if err != nil {
		return err
	}
	c.observe(e)

	switch e.Type {
	case wire.MsgAuth:
		var body wire.AuthBody
		if err := wire.Unmarshal(e.Body, &body); err != nil {
			return err
		}
		return c.doAuth(ctx, e.RequestID, body)
	case wire.MsgResume:
		var body wire.ResumeBody
		if err := wire.Unmarshal(e.Body, &body); err != nil {
			return err
		}
		return c.doResume(ctx, e.RequestID, body)
	default:
		_ = c.sendError(e.RequestID, wire.ErrUnauthenticated, "expected AUTH or RESUME")
		return errors.New("expected AUTH/RESUME")
	}
}

// register makes this connection reachable: the local hub for same-node
// delivery, the routing registry for cross-node, and presence for everyone else.
//
// It runs BEFORE the auth reply is written, and that order is the whole point. A
// client that has AUTH_OK believes it is connected and its peers may be told so
// immediately; if registration happened after, anything addressed to it in that
// window would find no route. For a chat message the loss is invisible (history
// backfills it) — but the E2E relay is fire-and-forget by design, so a ciphertext
// dropped there is gone for good, and the sender has no way to know.
func (c *conn) register(ctx context.Context) {
	c.unregister = c.gw.svc.Hub.Register(c.userID, c)
	c.gw.track(c)
	metrics.ConnActive.Inc()
	if c.gw.svc.Router != nil {
		_ = c.gw.svc.Router.Bind(ctx, c.userID, c.deviceID, c.gw.cfg.NodeID)
	}
	_ = c.gw.svc.Presence.Online(ctx, c.userID)
}

func (c *conn) doAuth(ctx context.Context, reqID uint64, body wire.AuthBody) error {
	var (
		ident *authIdentity
		err   error
	)
	switch {
	case body.Token != "":
		ident, err = c.authByToken(ctx, body.Token)
	case body.Username != "":
		ident, err = c.authByPasswordWithCode(ctx, body.Username, body.Password, body.TOTPCode, body.DisplayName, body.Register)
	default:
		err = errors.New("no credentials")
	}
	if err != nil {
		switch {
		case errors.Is(err, errLoginThrottled):
			_ = c.sendErrorRetry(reqID, wire.ErrRateLimited, "too many attempts", 2000)
		case errors.Is(err, errBadDisplayName):
			// A malformed field is the client's mistake, not a rejected identity:
			// reporting it as an auth failure would send the app to a login screen
			// it cannot get past by logging in again.
			_ = c.sendError(reqID, wire.ErrBadArg, "invalid display name")
		case errors.Is(err, auth.ErrBadUsername):
			// Same reasoning: "that handle has a character we cannot address" is
			// fixed by typing a different name, not by authenticating again. The
			// message carries the reason so the client can say which rule was
			// broken rather than a generic rejection.
			_ = c.sendError(reqID, wire.ErrBadArg, err.Error())
		case errors.Is(err, auth.ErrTwoFactorRequired):
			// The password was RIGHT and a code is needed. Its own code, because the
			// client behaviour is completely different from a rejected credential:
			// one asks for six digits on the screen the user is already on, the
			// other sends them back to a login form. Reporting this as
			// ErrUnauthenticated makes a working account look broken.
			_ = c.sendError(reqID, wire.ErrTwoFactorRequired, "two-factor code required")
		case errors.Is(err, auth.ErrBadTOTPCode):
			_ = c.sendError(reqID, wire.ErrTwoFactorInvalid, "that code did not match")
		default:
			_ = c.sendError(reqID, wire.ErrUnauthenticated, "authentication failed")
		}
		return err
	}

	c.userID = ident.userID
	c.sessionID = ident.sessionID
	if ident.deviceID != "" {
		c.deviceID = ident.deviceID
	}
	if c.deviceID == "" {
		c.deviceID = c.sessionID // fallback so multi-device keys stay unique
	}
	c.authed = true
	c.gw.audit(ctx, "auth.login", c.userID, c.deviceID, c.platform)
	c.register(ctx)

	return c.wc.Send(wire.MsgAuthOK, c.nextSeq(), c.ackSeq(), reqID, wire.AuthOKBody{
		UserID:      ident.userID,
		DeviceID:    c.deviceID,
		SessionID:   ident.sessionID,
		Token:       ident.token,
		ResumeToken: ident.resumeToken,
		Username:    ident.username,
		DisplayName: ident.displayName,
		AvatarRef:   ident.avatarRef,
	})
}

func (c *conn) doResume(ctx context.Context, reqID uint64, body wire.ResumeBody) error {
	ident, err := c.gw.svc.Auth.Resume(ctx, body.ResumeToken)
	if err != nil {
		_ = c.sendError(reqID, wire.ErrResumeExpired, "resume rejected; re-authenticate")
		return err
	}
	c.userID = ident.User.ID
	c.sessionID = ident.Session.ID
	c.deviceID = ident.Session.DeviceID
	c.authed = true
	c.register(ctx)

	// Replay the frames the client missed (Seq > LastAckSeq) from the session
	// buffer, then continue numbering from the session high-water mark so the
	// stream stays contiguous. Without a buffer, fall back to history backfill.
	//
	// LastAckSeq is unverified client input, so it is a floor to resume FROM, not
	// a value to adopt. Storing it directly let a client set this connection's
	// outbound counter to anything it liked; the replayed frames are the
	// authoritative high-water mark, and a claim beyond them has nothing behind
	// it. Clamping costs the client nothing it is entitled to: acking further
	// than the server ever sent is not a state it can legitimately be in.
	maxSeq := uint64(0)
	if c.gw.svc.Replay != nil {
		frames, _ := c.gw.svc.Replay.Since(ctx, c.sessionID, body.LastAckSeq)
		for _, f := range frames {
			if f.Seq > maxSeq {
				maxSeq = f.Seq
			}
			_ = c.wc.WriteRaw(f.Payload) // original seq preserved in the payload
		}
		// A client that acknowledged everything gets no frames back, and that is
		// not evidence that nothing was ever sent. Reading it that way reset this
		// connection's outbound counter to zero, so the next frame went out
		// numbered 1 to a client that had already acknowledged 100 — a sequence
		// that goes backwards, which is the one thing the resume contract promises
		// cannot happen. HighWater is the server's own record of how far it got,
		// so the floor is restored without taking the client's word for it.
		if hw, err := c.gw.svc.Replay.HighWater(ctx, c.sessionID); err == nil && hw > maxSeq {
			maxSeq = hw
		}
	}
	// With no replay store there is nothing to contradict the client, and history
	// backfill covers the gap either way — so its claim is honoured, bounded by
	// what it says it already has.
	if c.gw.svc.Replay == nil {
		maxSeq = body.LastAckSeq
	}
	c.outSeq.Store(maxSeq)
	// ResumeOK gets the next seq (maxSeq+1), so replayed frames + ResumeOK + live
	// traffic form one monotonic, gap-free sequence after LastAckSeq.
	return c.wc.Send(wire.MsgResumeOK, c.nextSeq(), c.ackSeq(), reqID, wire.ResumeOKBody{
		SessionID: ident.Session.ID,
		FromSeq:   body.LastAckSeq,
		// The token the client must use NEXT time. Resuming consumed the one it
		// just sent, so a client that keeps the old one will not merely fail its
		// next resume — it will look like a replay and end the session.
		ResumeToken: ident.Session.ResumeToken,
	})
}

// readLoop dispatches inbound envelopes until the peer disconnects. Each read
// carries an idle deadline: a connection that goes silent past IdleTimeout
// (missed heartbeats) is torn down, reclaiming server resources.
func (c *conn) readLoop(ctx context.Context) {
	for {
		_ = c.wc.SetReadDeadline(time.Now().Add(c.gw.cfg.IdleTimeout))
		e, err := c.wc.ReadEnvelope()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				c.log.Debug("read loop end", "err", err)
			}
			return
		}
		metrics.FramesIn.Inc()
		c.observe(e)
		if err := c.dispatch(ctx, e); err != nil {
			c.log.Debug("dispatch error", "type", e.Type, "err", err)
		}
	}
}

// writeLoop is the SINGLE writer for a connection once it is authenticated.
// Every post-auth outbound frame — responses, pushes, pings, errors — flows
// through here, so the Seq is assigned in the exact order frames hit the wire.
// This is what guarantees the client never sees Seq go backwards (which would
// trigger spurious gap detection / history refetch).
func (c *conn) writeLoop() {
	for {
		// Priority drain: always empty the hi lane first, then mid, then lo. The
		// nested selects with a default give hi strict precedence over mid over lo,
		// while the innermost blocking select parks when everything is empty.
		select {
		case <-c.done:
			return
		case d := <-c.outHi:
			if !c.writeOne(d) {
				return
			}
		default:
			select {
			case <-c.done:
				return
			case d := <-c.outHi:
				if !c.writeOne(d) {
					return
				}
			case d := <-c.outMid:
				if !c.writeOne(d) {
					return
				}
			default:
				select {
				case <-c.done:
					return
				case d := <-c.outHi:
					if !c.writeOne(d) {
						return
					}
				case d := <-c.outMid:
					if !c.writeOne(d) {
						return
					}
				case d := <-c.outLo:
					if !c.writeOne(d) {
						return
					}
				}
			}
		}
	}
}

// writeOne encodes and writes a single delivery, buffering it for resume replay.
// Returns false if the write failed (connection closed). Single writer → Seq
// stays monotonic on the wire.
func (c *conn) writeOne(d delivery.Delivery) bool {
	// A body that depends on the peer is resolved here, on the writer, because this
	// is the first and only place that knows which connection the frame is going to.
	body := d.Body
	if d.BodyFor != nil {
		body = d.BodyFor(c.peerCaps)
	}
	env := wire.Envelope{Type: d.Type, Seq: c.nextSeq(), Ack: c.ackSeq(), RequestID: d.RequestID, Body: wire.EncodeBody(body)}
	payload := env.Encode()
	_ = c.wc.SetWriteDeadline(time.Now().Add(c.gw.cfg.WriteTimeout))
	if err := c.wc.WriteRaw(payload); err != nil {
		c.log.Debug("write failed", "err", err)
		c.close()
		return false
	}
	metrics.FramesOut.Inc()
	// The frame is on the wire now, which is the only moment a delivery receipt
	// may honestly be claimed.
	if d.OnWritten != nil {
		d.OnWritten()
	}
	if c.gw.svc.Replay != nil && c.sessionID != "" {
		// In production the store is a replay.Async, so this only enqueues. The
		// timeout stays as the guard for a synchronous store: this runs inside
		// writeLoop — the SINGLE writer for the connection. An unbounded call against a hung replay store would block
		// that goroutine, back the outbound lanes up, and Send would then drop the
		// connection: an optimisation's dependency taking a user offline.
		//
		// Losing the buffer entry costs nothing that is not already recoverable.
		// A resume that finds no frames falls through to history backfill, which
		// is the documented fallback (see doResume).
		ctx, cancel := context.WithTimeout(context.Background(), replayAppendTimeout)
		_ = c.gw.svc.Replay.Append(ctx, c.sessionID, env.Seq, payload)
		cancel()
	}
	return true
}

// livenessLoop pings on the heartbeat interval and closes idle connections.
// tick is called by the shared gateway reaper on each heartbeat interval. It
// closes idle connections and pings live ones. Returns false if the connection
// was closed (so the reaper can drop it). Cheap and local — no Redis here.
func (c *conn) tick() bool {
	if time.Since(c.lastSeen()) > c.gw.cfg.IdleTimeout {
		c.log.Info("idle timeout; closing", "user", logUser(c.userID))
		c.close()
		return false
	}
	c.Send(delivery.Delivery{Type: wire.MsgPing, Body: nil})
	return true
}

// --- sequencing / liveness helpers ---

func (c *conn) nextSeq() uint64 { return c.outSeq.Add(1) }
func (c *conn) ackSeq() uint64  { return c.lastClientSeq.Load() }

// observe records liveness and the highest client sequence for piggyback acks.
func (c *conn) observe(e wire.Envelope) {
	c.touch()
	for {
		cur := c.lastClientSeq.Load()
		if e.Seq <= cur || c.lastClientSeq.CompareAndSwap(cur, e.Seq) {
			break
		}
	}
	c.maybeRefreshLiveness()
}

// maybeRefreshLiveness refreshes the presence + routing TTL on client activity,
// throttled to once per heartbeat interval per connection. Doing this on
// activity (instead of a per-connection timer) removes the timer goroutine while
// keeping "online" state fresh. Run async so a slow Redis never stalls the read
// loop; the throttle bounds the transient goroutine rate.
func (c *conn) maybeRefreshLiveness() {
	if !c.authed {
		return
	}
	now := time.Now().UnixNano()
	last := c.lastRefresh.Load()
	if now-last < int64(c.gw.cfg.Heartbeat) {
		return
	}
	if !c.lastRefresh.CompareAndSwap(last, now) {
		return
	}
	safego.Go(c.log, "conn.refreshLiveness", func() {
		ctx := context.Background()
		_ = c.gw.svc.Presence.Heartbeat(ctx, c.userID)
		if c.gw.svc.Router != nil {
			_ = c.gw.svc.Router.Refresh(ctx, c.userID, c.deviceID)
		}
	})
}

func (c *conn) touch()              { c.lastActivity.Store(time.Now().UnixNano()) }
func (c *conn) lastSeen() time.Time { return time.Unix(0, c.lastActivity.Load()) }

// reply enqueues a correlated response through the single writer (post-auth).
// Use this — never c.wc.Send directly — from dispatch and handlers, so Seq stays
// monotonic on the wire.
func (c *conn) reply(t wire.MsgType, reqID uint64, body any) error {
	c.Send(delivery.Delivery{Type: t, RequestID: reqID, Body: body})
	return nil
}

// replyThenClose enqueues a final frame and tears the connection down once it is
// actually on the wire.
//
// The close has to hang off the write, not follow the call: reply() only queues
// into the outbound lane, so closing straight afterwards would race the writer
// and drop the very frame that explains why the connection is ending. Delivery
// already carries OnWritten for delivery receipts, which fires at exactly the
// right moment — the frame has left the server.
//
// Used by the two handlers that destroy the thing the connection is standing on:
// revoking your own session, and deleting your account.
func (c *conn) replyThenClose(t wire.MsgType, reqID uint64, body any) error {
	c.Send(delivery.Delivery{Type: t, RequestID: reqID, Body: body, OnWritten: c.close})
	return nil
}

func (c *conn) replyError(reqID uint64, code wire.ErrorCode, msg string) error {
	metrics.Errors.WithLabelValues(strconv.FormatUint(uint64(code), 10)).Inc()
	return c.reply(wire.MsgError, reqID, wire.ErrorBody{Code: code, Message: msg})
}

func (c *conn) replyErrorRetry(reqID uint64, code wire.ErrorCode, msg string, retryAfterMs int) error {
	metrics.Errors.WithLabelValues(strconv.FormatUint(uint64(code), 10)).Inc()
	return c.reply(wire.MsgError, reqID, wire.ErrorBody{Code: code, Message: msg, RetryAfterMs: retryAfterMs})
}

// sendError / sendErrorRetry write DIRECTLY to the socket. They are only valid
// during the pre-auth handshake (single goroutine, writeLoop not yet running).
func (c *conn) sendError(reqID uint64, code wire.ErrorCode, msg string) error {
	return c.wc.Send(wire.MsgError, c.nextSeq(), c.ackSeq(), reqID, wire.ErrorBody{Code: code, Message: msg})
}

func (c *conn) sendErrorRetry(reqID uint64, code wire.ErrorCode, msg string, retryAfterMs int) error {
	return c.wc.Send(wire.MsgError, c.nextSeq(), c.ackSeq(), reqID,
		wire.ErrorBody{Code: code, Message: msg, RetryAfterMs: retryAfterMs})
}

// stillConnectedElsewhere reports whether this user keeps another live
// connection after the current one is gone. The local hub answers for free and
// covers the common case (a phone and a desktop on the same node); only if it
// says no do we pay for the cross-node registry lookup — a disconnect storm must
// not turn into a burst of Redis round trips.
//
// A registry error is treated as "still connected": a spurious offline is a
// user-visible lie, while a missing offline is corrected by the presence TTL
// within a heartbeat.
func (c *conn) stillConnectedElsewhere() bool {
	if c.gw.svc.Hub != nil && c.gw.svc.Hub.IsOnline(c.userID) {
		return true
	}
	if c.gw.svc.Router == nil {
		return false
	}
	nodes, err := c.gw.svc.Router.NodesFor(context.Background(), c.userID)
	return err != nil || len(nodes) > 0
}

func (c *conn) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.gw.untrack(c)
		if c.authed {
			metrics.ConnActive.Dec()
			if c.gw.svc.Router != nil {
				_ = c.gw.svc.Router.Unbind(context.Background(), c.userID, c.deviceID, c.gw.cfg.NodeID)
			}
		}
		if c.unregister != nil {
			c.unregister()
		}
		// Presence is per USER, not per connection: one device disconnecting must
		// not announce the user offline while another still holds a connection.
		// Both checks run after this connection is already gone — from the hub
		// (unregister above) and from the routing registry (Unbind above) — so
		// they see the state that will remain.
		if c.authed && !c.stillConnectedElsewhere() {
			_ = c.gw.svc.Presence.Offline(context.Background(), c.userID)
		}
		_ = c.wc.Close()
	})
}
