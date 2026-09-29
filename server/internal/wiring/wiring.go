package wiring

import (
	"context"
	"log/slog"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/audit"
	"github.com/SyncApp-chat/SyncApp/internal/auth"
	"github.com/SyncApp-chat/SyncApp/internal/billing"
	"github.com/SyncApp-chat/SyncApp/internal/call"
	"github.com/SyncApp-chat/SyncApp/internal/chat"
	"github.com/SyncApp-chat/SyncApp/internal/contact"
	"github.com/SyncApp-chat/SyncApp/internal/delivery"
	"github.com/SyncApp-chat/SyncApp/internal/fanout"
	"github.com/SyncApp-chat/SyncApp/internal/gateway"
	"github.com/SyncApp-chat/SyncApp/internal/invite"
	"github.com/SyncApp-chat/SyncApp/internal/keydir"
	"github.com/SyncApp-chat/SyncApp/internal/media"
	"github.com/SyncApp-chat/SyncApp/internal/moderation"
	"github.com/SyncApp-chat/SyncApp/internal/notify"
	"github.com/SyncApp-chat/SyncApp/internal/pin"
	"github.com/SyncApp-chat/SyncApp/internal/platform"
	"github.com/SyncApp-chat/SyncApp/internal/poll"
	"github.com/SyncApp-chat/SyncApp/internal/presence"
	"github.com/SyncApp-chat/SyncApp/internal/reaction"
	"github.com/SyncApp-chat/SyncApp/internal/schedule"
	"github.com/SyncApp-chat/SyncApp/internal/search"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
	"github.com/SyncApp-chat/SyncApp/pkg/id"
	"github.com/SyncApp-chat/SyncApp/pkg/ratelimit"
)

// presenceTTL is how long a presence entry lives without a refresh.
const presenceTTL = 60 * time.Second

// scheduleTick is how often due scheduled sends and self-destruct deadlines are
// checked.
const scheduleTick = 5 * time.Second

// Core is the part of the gateway's dependencies that differs between the
// topologies: in-process services in the monolith, gRPC clients in gatewayd.
type Core struct {
	Auth     gateway.AuthService
	Chat     gateway.ChatService
	Msg      gateway.MessageReader
	Broker   gateway.MessageBroker
	Presence gateway.PresenceService
	KeyDir   keydir.Directory
}

// Membership authorizes the edge-local services (reactions, calls, polls,
// scheduled sends). chatd's client satisfies it in the split, the chat service
// in the monolith.
type Membership interface {
	reaction.Chats
	call.Chats
	poll.Chats
	schedule.Chats
	search.Chats
}

// EdgeDeps is what NewEdge needs beyond the shared backends.
type EdgeDeps struct {
	Core
	Membership Membership
	// ChatAdmin serves the chat capabilities chatd does not expose over RPC —
	// pin rights, invite links and roles, the direct-chat peer for block checks.
	// In the split it is a chat service over the shared store; its authorization
	// cache lives 5 s, so a membership written here reaches chatd's view within
	// that window.
	ChatAdmin *chat.Service
	Contacts  *contact.Service
	// Sender, when set, lets the edge's scheduler dispatch due sends (the
	// monolith). gatewayd leaves it nil; messaged runs the dispatcher.
	Sender schedule.Sender
}

// Edge is everything the gateway process owns besides the gateway itself.
type Edge struct {
	Services gateway.Services
	Media    *media.Service
	Billing  *billing.Service
	Search   *search.Service
	Schedule *schedule.Service
	// sendsScheduled records whether Schedule has a sender, i.e. whether this
	// process is the one that dispatches.
	sendsScheduled bool
}

// NewEdge builds the gateway's services. It is the single definition of
// gateway.Services for both topologies; TestEdgeServicesAreComplete fails if a
// field is left unset in either.
func NewEdge(ctx context.Context, b *platform.Backends, cfg Config, d EdgeDeps) (*Edge, error) {
	st := b.Stores
	mediaSvc, err := NewMedia(cfg, b.IDs, b.MessageStore, b.Log)
	if err != nil {
		return nil, err
	}
	searchBackend, err := NewSearchBackend(ctx, cfg)
	if err != nil {
		return nil, err
	}
	searchSvc := search.New(searchBackend, d.Membership, b.Log)
	sched := NewScheduler(st.Schedule, d.Membership, d.Sender, d.ChatAdmin, d.Contacts, b.IDs, b.Log)
	billingSvc := NewBilling(cfg, st.Billing, b.Bus, b.IDs, b.Log)

	svc := gateway.Services{
		Auth:     d.Auth,
		Chat:     d.Chat,
		Msg:      d.Msg,
		Broker:   d.Broker,
		Presence: d.Presence,
		KeyDir:   d.KeyDir,

		Reactor:  reaction.New(st.Reactions, d.Membership, b.Bus),
		Calls:    call.New(st.Calls, d.Membership, b.Bus, b.IDs),
		Polls:    poll.New(st.Polls, d.Membership, b.Bus, b.IDs),
		Contacts: d.Contacts,
		Schedule: sched,
		Pins:     pin.New(st.Pins, st.Drafts, d.ChatAdmin, b.Bus),
		Invites:  invite.New(st.Invites, memberRoles(st.Chats), d.ChatAdmin),
		Users:    st.Users,
		// Downloads are gated on membership of a chat the blob was posted in; without
		// a resolver the gateway falls back to the signed ref alone.
		MediaChats: mediaChats(b.MessageStore),
		Hub:        delivery.NewHub(),
		SecretQ:    st.SecretQ,
		IDs:        b.IDs,
		Media:      mediaSvc,
		Search:     searchSvc,
		Audit:      audit.NewLogSink(b.Log),
		Roles:      st.Roles,
		Bus:        b.Bus,
		Router:     b.Router,
		Replay:     b.Replay,
		UserLimits: NewUserLimits(b),
	}
	// Assigned only when present: a nil *billing.Service in the interface field
	// would read as configured and panic on first use.
	if billingSvc != nil {
		svc.Billing = billingSvc
	}
	return &Edge{
		Services:       svc,
		Media:          mediaSvc,
		Billing:        billingSvc,
		Search:         searchSvc,
		Schedule:       sched,
		sendsScheduled: d.Sender != nil,
	}, nil
}

// RunBackground starts the edge's periodic work. Every job is safe to run on
// several replicas at once: each claims or deletes rows atomically.
func (e *Edge) RunBackground(ctx context.Context, gw *gateway.Gateway) {
	// Undelivered secret envelopes past their TTL are a record of who messaged
	// whom, so collecting them is part of the privacy promise.
	go gw.RunSecretQueueCollector(ctx)
	// A no-op sweep when the message store cannot say whether a ref is used.
	go e.Media.RunGC(ctx, 0)
	if e.Billing != nil {
		go e.Billing.RunExpiry(ctx)
	}
	if e.sendsScheduled {
		go e.Schedule.Run(ctx, scheduleTick)
	}
}

// NewAuth builds the identity service. The second factor is always attached:
// without it an account that enabled TOTP signs in with the password alone.
func NewAuth(st store.Stores, ids *id.Generator) *auth.Service {
	return auth.New(st.Users, st.Sessions, ids).WithTwoFactor(st.TwoFactor)
}

// NewPresence builds the presence service over the shared backend.
func NewPresence(b *platform.Backends) *presence.Service {
	return presence.New(b.Presence, b.Bus, presenceTTL)
}

// NewContacts builds the address book and block list.
func NewContacts(st store.Stores) *contact.Service {
	return contact.New(st.Contacts, st.Users)
}

// NewKeyDir builds the E2E prekey directory: shared through Redis when it is
// configured, process-local otherwise.
func NewKeyDir(b *platform.Backends) keydir.Directory {
	if b.Redis != nil {
		return keydir.NewRedis(b.Redis, b.Log)
	}
	return keydir.NewMemory()
}

// NewUserLimits returns the cross-node per-user budget when Redis is available,
// and nil otherwise, which the gateway replaces with a node-local one.
func NewUserLimits(b *platform.Backends) ratelimit.Shared {
	if b.Redis == nil {
		return nil
	}
	return ratelimit.NewRedisShared(b.Redis, "user", 2, 20)
}

// FanoutDeps is what fanout needs to decide who hears about an event.
type FanoutDeps struct {
	Chats fanout.Chats
	Kinds fanout.ChatKinds
	// Mute answers per-member chat flags, so a muted chat sends no push.
	Mute     fanout.MuteChecker
	Users    store.UserStore
	Contacts *contact.Service
}

// NewFanout builds the delivery fanout with every policy it enforces. The
// policies are privacy decisions, so both fanout hosts (the monolith and
// fanoutd) get all of them from here:
//   - presence reaches only the audience the owner's privacy setting allows;
//   - a muted chat produces no push;
//   - channel read receipts stay with the reader;
//   - message text reaches the push provider only for accounts that opted in.
func NewFanout(b *platform.Backends, d FanoutDeps) *fanout.Service {
	return fanout.New(b.Bus, d.Chats, b.Router, b.Log).
		WithPresenceAudience(gateway.NewPresenceAudience(gateway.Services{Users: d.Users, Contacts: d.Contacts})).
		WithMuteChecker(d.Mute).
		WithChatKinds(d.Kinds).
		WithPreviewPolicy(gateway.NewPreviewPolicy(d.Users))
}

// NewScheduler builds the scheduled-send service. sender may be nil for a
// process that only accepts schedules and never dispatches them.
func NewScheduler(st store.ScheduleStore, chats schedule.Chats, sender schedule.Sender, peers DirectPeers, blocks Blocks, ids *id.Generator, log *slog.Logger) *schedule.Service {
	return schedule.New(st, chats, sender, ids, log).
		WithBlockGate(BlockGate{Peers: peers, Blocks: blocks})
}

// NewBilling builds the billing service with the configured acquirers, or
// returns nil when the store has no billing tables. With no acquirer configured
// the service still runs and every account is on the free tier.
func NewBilling(cfg Config, st store.BillingStore, bus eventbus.Bus, ids *id.Generator, log *slog.Logger) *billing.Service {
	if st == nil {
		return nil
	}
	svc := billing.New(st, bus, ids, log)
	if cfg.YooKassa != nil {
		if cfg.YooKassa.AllowedSources == nil {
			log.Warn("billing: yookassa notifications accepted from any address (SYNCAPP_YOOKASSA_ALLOWED_IPS=off)")
		}
		svc = svc.WithProvider(cfg.YooKassa)
		log.Info("billing: yookassa enabled (card + sbp)")
	}
	if cfg.Stripe != nil {
		svc = svc.WithProvider(cfg.Stripe)
		log.Info("billing: stripe enabled (card)")
	}
	return svc
}

// NewMedia builds the blob store and signer. When the message log can answer
// "is this ref still used", unreferenced blobs are collected (see RunBackground).
func NewMedia(cfg Config, ids *id.Generator, msgs store.MessageStore, log *slog.Logger) (*media.Service, error) {
	fs, err := media.NewFSStore(cfg.MediaDir)
	if err != nil {
		return nil, err
	}
	svc := media.New(fs, ids, platform.MediaSecret(), cfg.PublicURL).
		WithLogger(log).
		// The deployment ceiling; per-account limits come from entitlements.
		WithMaxSize(cfg.MediaMaxBytes)
	if refs, ok := msgs.(store.MediaReferencer); ok {
		svc.WithReferencer(refs)
	}
	return svc, nil
}

// NewSearchBackend selects the shared Postgres index or the in-memory one.
func NewSearchBackend(ctx context.Context, cfg Config) (search.Backend, error) {
	if cfg.SearchDSN != "" {
		return search.NewPostgresBackend(ctx, cfg.SearchDSN)
	}
	return search.NewMemoryBackend(), nil
}

// NewModeration builds the advisory abuse filter.
func NewModeration(cfg Config, bus eventbus.Bus, log *slog.Logger) *moderation.Service {
	return moderation.New(bus, cfg.BannedTerms, log)
}

// NewNotify builds the push worker. The device fan-out also removes tokens the
// provider reports as dead.
func NewNotify(cfg Config, st store.Stores, bus eventbus.Bus, log *slog.Logger) *notify.Service {
	return notify.New(bus, notify.ProviderFor(cfg.PushEndpoint, cfg.PushKey, log), log).
		WithDevices(notify.StoreDevices{Users: st.Users})
}

// DirectPeers resolves the other party of a 1:1 chat.
type DirectPeers interface {
	DirectPeer(ctx context.Context, chatID, userID string) (peer string, ok bool, err error)
}

// Blocks reports whether either of two users blocks the other.
type Blocks interface {
	BlocksBetween(ctx context.Context, a, b string) (bool, error)
}

// BlockGate answers the scheduler's "is this 1:1 send refused by a block?". It
// lives here because neither chat nor contact should import the other.
type BlockGate struct {
	Peers  DirectPeers
	Blocks Blocks
}

// Blocked implements schedule.BlockGate.
func (g BlockGate) Blocked(ctx context.Context, chatID, senderID string) (bool, error) {
	peer, ok, err := g.Peers.DirectPeer(ctx, chatID, senderID)
	if err != nil || !ok {
		return false, err
	}
	return g.Blocks.BlocksBetween(ctx, senderID, peer)
}

func memberRoles(chats store.ChatStore) store.MemberRoleStore {
	roles, _ := chats.(store.MemberRoleStore)
	return roles
}

func mediaChats(msgs store.MessageStore) store.MediaChatResolver {
	r, _ := msgs.(store.MediaChatResolver)
	return r
}
