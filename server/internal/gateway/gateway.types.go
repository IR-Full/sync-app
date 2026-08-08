package gateway

import (
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/synapse-chat/synapse/internal/audit"
	"github.com/synapse-chat/synapse/internal/delivery"
	"github.com/synapse-chat/synapse/internal/keydir"
	"github.com/synapse-chat/synapse/internal/replay"
	"github.com/synapse-chat/synapse/internal/router"
	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/ratelimit"
)

// Services bundles the domain dependencies the gateway routes to. Every domain
// field is an INTERFACE (see services.go), so cmd/server can wire in-process
// implementations and cmd/gatewayd can wire gRPC clients — the gateway code is
// identical either way. Hub is concrete because in-node connection delivery is
// inherently per-gateway-node, not a separable service.
type Services struct {
	Auth     AuthService
	Chat     ChatService
	Msg      MessageReader // read path (history, read receipts)
	Broker   MessageBroker // write path (create/edit/delete)
	Presence PresenceService
	Reactor  ReactionService // emoji reactions (optional)
	Calls    CallService     // voice/video call signaling (optional)
	Polls    PollService     // polls (optional)
	Contacts ContactService  // address book + block list (optional)
	Schedule ScheduleService // deferred sends (optional)
	Pins     PinService      // pinned messages + drafts (optional)
	Invites  InviteService   // public handles, invite links, admin rights (optional)
	Users    store.UserStore // for @username → user resolution
	Hub      *delivery.Hub
	KeyDir   keydir.Directory // E2E prekey directory (optional)
	Media    MediaService     // media upload/download tickets (optional)
	Search   SearchService    // full-text search (optional)
	Audit    audit.Sink       // audit log (optional)
	Bus      eventbus.Bus     // event bus (for cross-node delivery)
	Router   router.Router    // user→node routing registry
	Replay   replay.Buffer    // per-session resume replay buffer (optional)
	// UserLimits caps EXPENSIVE per-user actions (media tickets, search, export,
	// invite links, chat creation) across all of a user's connections — and, with
	// a Redis-backed implementation, across nodes. The per-connection flood bucket
	// answers "is this socket abusive?", which a client sidesteps by opening more
	// sockets; the cost these guard is paid once per request either way.
	// Optional: nil falls back to a node-local shared limiter.
	UserLimits ratelimit.Shared
}

// Config tunes connection behavior.
type Config struct {
	NodeID           string // this gateway's node id (for cross-node routing)
	ServerVersion    string
	Heartbeat        time.Duration // interval between server pings
	IdleTimeout      time.Duration // close if no client traffic for this long
	HandshakeTimeout time.Duration // max time to complete HELLO + AUTH
	WriteTimeout     time.Duration // max time a single frame write may block
	MaxInflight      int           // outbound queue depth (backpressure window)
	SendRate         float64       // allowed state-changing msgs/sec per connection
	SendBurst        float64       // burst capacity for the above
	// Typing indicators are cheap to send and expensive to deliver: one frame in
	// becomes one delivery per chat member, through the bus, on every node. They
	// are exempt from the send bucket (they are not state-changing), so they get
	// their own ceiling: TypingRate/Burst bound a single connection overall, and
	// TypingChatRate/Burst bound it per chat. A real client refreshes "typing…"
	// every few seconds, so this is invisible in normal use.
	TypingRate      float64
	TypingBurst     float64
	TypingChatRate  float64
	TypingChatBurst float64
	// Call signaling is relayed between participants without passing the send
	// bucket (SDP/ICE is chatty and must not compete with messages for it), so it
	// carries its own generous ceiling — enough for ICE gathering, not enough to
	// use the relay as an unmetered message channel.
	SignalRate  float64
	SignalBurst float64
	AcceptLoops int // concurrent TCP accept goroutines (0 = 1)
	// MaxConnsPerIP caps concurrent connections from one source IP (0 = unlimited).
	// AcceptRatePerIP caps new connections/sec from one source IP (0 = unlimited).
	// Together they blunt connection floods / reconnect storms at the accept edge.
	MaxConnsPerIP   int
	AcceptRatePerIP float64
	// AllowedOrigins restricts WebSocket upgrades (CSWSH defense). Empty = allow
	// any origin (dev only). In production set your web app origins.
	AllowedOrigins []string
	// AdminUsers / ModeratorUsers assign platform roles (RBAC). Admins may perform
	// any privileged action; moderators may export/inspect for support but not
	// change platform config.
	AdminUsers     []string
	ModeratorUsers []string
}

// Role is a platform-level RBAC role.
type Role string

// Gateway accepts and serves client connections.
type Gateway struct {
	svc Services
	cfg Config
	log *slog.Logger
	up  websocket.Upgrader
	// loginLimiter throttles password auth attempts per username across ALL
	// connections, so an attacker cannot brute-force by opening a new connection
	// per guess. A token bucket (not a hard lockout) means a legitimate user is
	// never permanently locked out by someone spamming their username.
	loginLimiter *ratelimit.Limiter
	// userLimits caps expensive per-user actions across connections (see Services).
	userLimits ratelimit.Shared
	// newChatLimiter throttles how fast one user may start NEW direct chats,
	// capping mass-DM spam without affecting messaging in existing chats.
	newChatLimiter *ratelimit.Limiter
	// conns tracks live connections for graceful drain on shutdown.
	conns sync.Map // *conn -> struct{}
	// roles maps user ids to their platform role (RBAC).
	roles map[string]Role
	// ipg limits per-source-IP accept rate and concurrent connections (may be nil).
	ipg *ipGuard
	// reaper: one shared liveness goroutine per node (idle-close + ping).
	reaperOnce sync.Once
	reaperDone chan struct{}
}
