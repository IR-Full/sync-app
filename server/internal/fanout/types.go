package fanout

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/router"
	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
)

// Chats is the membership lookup fanout needs. An interface (not *chat.Service)
// so fanoutd can run against a gRPC chat client.
type Chats interface {
	MemberIDs(ctx context.Context, chatID string) ([]string, error)
	// MemberIDsPage walks membership by keyset so a hot chat can be streamed
	// rather than materialized.
	MemberIDsPage(ctx context.Context, chatID, afterUserID string, limit int) ([]string, error)
	// UserChats is how presence finds its audience: a summary names the peer of a
	// direct chat, which is exactly the set of people entitled to a user's
	// online state. Already implemented by both the service and its gRPC client.
	UserChats(ctx context.Context, userID, after string, limit int) ([]model.ChatSummary, error)
}

// MuteChecker reports whether a member has silenced a chat.
//
// An optional dependency, and optional for a reason worth stating: a deployment
// that does not wire one keeps the previous behaviour, which is that every offline
// recipient gets a notification. That is the behaviour the `muted` column was
// supposed to change from the first migration onwards and never did — nothing read
// it, so muting a chat was impossible while the schema implied otherwise.
//
// Failing OPEN is deliberate. If the lookup errors we send the notification: a
// missed message is worse than an unwanted buzz, and an outage in a settings read
// should not silence a conversation.
type MuteChecker interface {
	ChatFlags(ctx context.Context, chatID, userID string) (model.MemberFlags, error)
}

// PreviewPolicy decides whether a recipient allows message text in their push
// payload.
//
// A one-method interface for the same reason PresenceAudience is one: fanout
// routes, and it has no other business knowing that accounts have settings.
//
// The method returns a plain bool with no error, which is unusual here and is the
// point: the safe answer to "we could not check" is NO preview, and folding that
// into the return value removes any way for a caller to accidentally treat a
// failed lookup as consent.
type PreviewPolicy interface {
	WantsPushPreview(ctx context.Context, userID string) bool
}

// PresenceAudience decides who may learn a user's online state.
//
// An interface rather than a direct dependency on the user store: fanout's job
// is routing, and it has no other reason to know that accounts have settings. A
// deployment that does not wire one keeps the previous behaviour — presence
// reaches every direct peer — which is also what makes this safe to add without
// touching the microservice wiring.
type PresenceAudience interface {
	// MaySeePresence reports whether viewerID may see ownerID's online state.
	MaySeePresence(ctx context.Context, ownerID, viewerID string) (bool, error)
}

// ChatKinds tells fanout what kind of chat an event belongs to. Optional: without
// it, read receipts are kept private only in chats large enough to be sharded.
// Answers are cached for the life of the process (see Service.kindCache), so an
// implementation may be a remote call.
type ChatKinds interface {
	ChatType(ctx context.Context, chatID string) (model.ChatType, error)
}

// Service consumes domain events and routes deliveries to the owning nodes.
type Service struct {
	bus      eventbus.Bus
	chats    Chats
	router   router.Router
	log      *slog.Logger
	audience PresenceAudience
	mutes    MuteChecker
	previews PreviewPolicy
	kinds    ChatKinds

	mu        sync.RWMutex
	cache     map[string]memberEntry
	lastSweep time.Time

	// kindCache remembers each chat's kind. A chat never changes kind, so an
	// entry never goes stale and needs no TTL — only a size bound. It exists for
	// fanoutd, where ChatKinds is a gRPC call to chatd that ends in a database
	// read: without it every read receipt would cost that round trip.
	kindMu    sync.RWMutex
	kindCache map[string]model.ChatType
}

// memberEntry is a chat's cached delivery shape. ids is nil when the chat is
// HOT: the whole point is not to hold a huge channel's membership, so the cache
// records the verdict instead — which is the part worth remembering.
type memberEntry struct {
	ids     []string
	hot     bool
	expires time.Time
}
