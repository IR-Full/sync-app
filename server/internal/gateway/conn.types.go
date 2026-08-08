package gateway

import (
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/synapse-chat/synapse/internal/delivery"
	"github.com/synapse-chat/synapse/pkg/ratelimit"
	"github.com/synapse-chat/synapse/pkg/wire"
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
