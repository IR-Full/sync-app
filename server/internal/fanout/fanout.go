// Package fanout is the Delivery Service (Sections 6, 9). It subscribes to
// message/read/typing events and, for each recipient, looks up which gateway
// NODES hold that user's live connections (via the router) and publishes a
// node-targeted delivery on the bus. The owning gateway node consumes it and
// pushes to its local connections. This decouples the delivery decision (fanout,
// any node) from the actual socket write (the node holding the connection), so
// the system scales horizontally instead of only reaching same-node recipients.
// Recipients bound to no node are offline → a push job is emitted.
package fanout

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/SyncApp-chat/SyncApp/internal/metrics"
	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/router"
	"github.com/SyncApp-chat/SyncApp/internal/tracing"
	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
)

// New builds the fanout service.
func New(bus eventbus.Bus, chats Chats, rtr router.Router, log *slog.Logger) *Service {
	return &Service{bus: bus, chats: chats, router: rtr, log: log,
		cache: map[string]memberEntry{}, lastSweep: time.Now()}
}

// WithPresenceAudience gates presence on the sender's privacy setting.
//
// Optional: without it presence reaches every direct peer, which is what this
// service did before settings existed. Denying wholesale on a missing dependency
// would turn "we cannot check" into "everyone is invisible".
func (s *Service) WithPresenceAudience(a PresenceAudience) *Service {
	s.audience = a
	return s
}

// WithPreviewPolicy decides, per recipient, whether message text may travel to
// the push provider.
//
// Optional — and with no policy wired, NO preview is sent. That is the opposite
// default from the other optional dependencies here, deliberately: for presence
// and mute, an unwired dependency preserves the old behaviour because the old
// behaviour was a reasonable one. Here the old behaviour was handing every
// message to a third party, so an unconfigured deployment should not inherit it.
func (s *Service) WithPreviewPolicy(p PreviewPolicy) *Service {
	s.previews = p
	return s
}

// WithMuteChecker makes the notification path honour a muted chat.
//
// Optional in the same sense as the presence audience: a deployment without one
// notifies every offline recipient, which is what happened before this existed.
func (s *Service) WithMuteChecker(m MuteChecker) *Service {
	s.mutes = m
	return s
}

// muted reports whether userID has silenced chatID right now.
//
// Fails OPEN on an error: a missed message is worse than an unwanted notification,
// and a settings lookup that is briefly unavailable must not silence a chat.
func (s *Service) muted(ctx context.Context, chatID, userID string) bool {
	if s.mutes == nil {
		return false
	}
	f, err := s.mutes.ChatFlags(ctx, chatID, userID)
	if err != nil {
		return false
	}
	return f.MutedAt(time.Now().UnixMilli())
}

// members returns a chat's delivery shape from a short-TTL cache: either its
// member ids (normal chat) or "this one is hot, stream it".
//
// The cache is consulted FIRST and covers both answers. Deciding hotness with a
// fresh page read on every message would reintroduce, per message, exactly the
// lookup this cache exists to remove — and would do it for every ordinary chat
// in the system to answer a question only huge ones ever answer differently.
func (s *Service) members(ctx context.Context, chatID string) (ids []string, hot bool, err error) {
	s.mu.RLock()
	e, ok := s.cache[chatID]
	s.mu.RUnlock()
	if ok && time.Now().Before(e.expires) {
		return e.ids, e.hot, nil
	}

	// One page past the threshold answers "is this hot?" without loading a
	// million-member channel to find out.
	first, err := s.chats.MemberIDsPage(ctx, chatID, "", fanoutShardThreshold+1)
	if err != nil {
		return nil, false, err
	}
	entry := memberEntry{expires: time.Now().Add(memberCacheTTL)}
	if len(first) > fanoutShardThreshold {
		entry.hot = true
	} else {
		entry.ids = first
	}
	s.mu.Lock()
	s.sweepLocked()
	s.cache[chatID] = entry
	s.mu.Unlock()
	return entry.ids, entry.hot, nil
}

// sweepLocked collects expired entries, then enforces the ceiling by dropping
// arbitrary survivors. Caller holds the write lock. A dropped entry costs one
// membership lookup on its next delivery — the same cost its expiry would have.
func (s *Service) sweepLocked() {
	now := time.Now()
	if now.Sub(s.lastSweep) < memberSweepEvery && len(s.cache) < memberCacheMax {
		return
	}
	s.lastSweep = now
	for k, e := range s.cache {
		if now.After(e.expires) {
			delete(s.cache, k)
		}
	}
	for k := range s.cache {
		if len(s.cache) < memberCacheMax {
			break
		}
		delete(s.cache, k)
	}
	metrics.CacheEntries.WithLabelValues("fanout_members").Set(float64(len(s.cache)))
}

// Start registers the bus subscriptions in the "fanout" queue group (each event
// processed once across fanout workers).
func (s *Service) Start() error {
	for _, subj := range []string{eventbus.SubjMessageCreated, eventbus.SubjMessageEdited, eventbus.SubjMessageDeleted} {
		if err := s.bus.Subscribe(subj, "fanout", s.onMessage); err != nil {
			return err
		}
	}
	if err := s.bus.Subscribe(eventbus.SubjMessageRead, "fanout", s.onRead); err != nil {
		return err
	}
	// Hot-chat shard jobs: competing workers each deliver one member chunk.
	if err := s.bus.Subscribe(subjFanoutShard, "fanout", s.onShard); err != nil {
		return err
	}
	if err := s.bus.Subscribe(eventbus.SubjReaction, "fanout", s.onReaction); err != nil {
		return err
	}
	if err := s.bus.Subscribe(eventbus.SubjCallState, "fanout", s.onCallState); err != nil {
		return err
	}
	if err := s.bus.Subscribe(eventbus.SubjPollState, "fanout", s.onPollState); err != nil {
		return err
	}
	if err := s.bus.Subscribe(eventbus.SubjPinned, "fanout", s.onPinned); err != nil {
		return err
	}
	if err := s.bus.Subscribe(eventbus.SubjPresence, "fanout", s.onPresence); err != nil {
		return err
	}
	return s.bus.Subscribe(eventbus.SubjTyping, "fanout", s.onTyping)
}

// route publishes a node-targeted delivery to every node holding the user's
// connections. Returns how many nodes were targeted (0 = user offline).
//
// Kept for the single-recipient callers (presence, call signaling, anything
// addressed at one person). Everything that addresses a CHAT goes through
// routeMany instead — see the comment there for why that distinction is the
// whole performance story of this file.
func (s *Service) route(ctx context.Context, userID, deviceID string, typ wire.MsgType, body []byte) int {
	nodes, err := s.router.NodesFor(ctx, userID)
	if err != nil {
		s.log.Warn("router lookup failed", "user", userID, "err", err)
		return 0
	}
	s.publish(ctx, nodes, []string{userID}, deviceID, typ, body)
	return len(nodes)
}

// routeMany delivers one payload to a page of recipients, and returns those that
// reached no node — the ones a caller may want to push to.
//
// This replaced a loop that called route per recipient, and the two costs it
// removes are both multiplicative in the size of the chat:
//
//   - One router round trip instead of N. A group of 200 cost 200 SEQUENTIAL
//     Redis lookups per event, and "per event" includes every typing indicator.
//   - One publish per NODE instead of one per recipient, each of which carried a
//     full copy of the body. A 4 KB message to a thousand members spread over ten
//     nodes put 4 MB on the bus to deliver what ten frames' worth of addressing
//     could have carried.
//
// The offline list is returned rather than pushed from here so the caller decides
// what an absence means: a new message earns a notification, a reaction does not.
func (s *Service) routeMany(ctx context.Context, userIDs []string, typ wire.MsgType, body []byte) (offline []string) {
	if len(userIDs) == 0 {
		return nil
	}
	byUser, err := s.router.NodesForMany(ctx, userIDs)
	if err != nil {
		s.log.Warn("router batch lookup failed", "users", len(userIDs), "err", err)
		// Every recipient is unresolved, not offline. Reporting them offline would
		// turn a Redis outage into a push storm addressed at people who are
		// connected — so the caller is told nothing reached anyone and nothing is
		// claimed about why.
		return nil
	}
	// Invert to node → recipients on it, so the body is encoded and published once
	// per node no matter how many of its members are there.
	perNode := make(map[string][]string, len(byUser))
	for _, uid := range userIDs {
		nodes := byUser[uid]
		if len(nodes) == 0 {
			offline = append(offline, uid)
			continue
		}
		for _, n := range nodes {
			perNode[n] = append(perNode[n], uid)
		}
	}
	for node, users := range perNode {
		s.publish(ctx, []string{node}, users, "", typ, body)
	}
	return offline
}

// publish encodes one delivery and puts it on each node's subject.
//
// Key is the first recipient rather than something chat-scoped: the bus uses it
// for partitioning, and a delivery destined for one node has no ordering
// relationship with any other. Per-chat ordering is carried by chat_seq, not by
// bus arrival order.
func (s *Service) publish(ctx context.Context, nodes, users []string, deviceID string, typ wire.MsgType, body []byte) {
	nd := router.NodeDelivery{Users: users, DeviceID: deviceID, Type: uint16(typ), Body: body}
	data := nd.Encode()
	key := ""
	if len(users) > 0 {
		key = users[0]
	}
	for _, node := range nodes {
		_ = s.bus.Publish(ctx, eventbus.Event{Subject: router.DeliverSubject(node), Key: key, Data: data})
	}
}

func (s *Service) onMessage(ctx context.Context, e eventbus.Event) error {
	ctx, span := tracing.Start(tracing.Extract(ctx, e.Headers), "fanout.onMessage")
	defer span.End()
	var body wire.NewMessageBody
	if err := wire.Unmarshal(e.Data, &body); err != nil {
		return err
	}
	if body.Timestamp > 0 {
		metrics.FanoutLagSeconds.Observe(time.Since(time.UnixMilli(body.Timestamp)).Seconds())
	}
	members, hot, err := s.members(ctx, body.ChatID)
	if err != nil {
		return err
	}
	// Normal chat: deliver inline. Hot chat: stream it into shard jobs that
	// competing workers deliver in parallel.
	if hot {
		return s.shardHotChat(ctx, body)
	}
	s.deliverNew(ctx, members, body)
	return nil
}

// shardHotChat walks a huge chat's membership by keyset and publishes one job per
// page. Only a single page is ever resident here: the previous design loaded
// every member id before splitting them up, which put the whole channel in the
// coordinator's heap — and in the member cache behind it — precisely for the
// chats where that is least affordable.
func (s *Service) shardHotChat(ctx context.Context, body wire.NewMessageBody) error {
	headers := tracing.Inject(ctx)
	after := ""
	for {
		page, err := s.chats.MemberIDsPage(ctx, body.ChatID, after, fanoutShardSize)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		after = page[len(page)-1]
		if err := s.bus.Publish(ctx, eventbus.Event{
			Subject: subjFanoutShard, Key: body.ChatID, Headers: headers,
			Data: wire.Marshal(wire.FanoutShardBody{Body: body, Members: page}),
		}); err != nil {
			return err
		}
		metrics.FanoutShardJobs.Inc()
		if len(page) < fanoutShardSize {
			return nil
		}
	}
}

// eachMember calls fn for every member of a chat. For a normal chat that walks
// the cached ids; for a hot one it STREAMS pages, so the caller never holds a
// channel's membership and never has to know which case it is in. Read
// receipts, reactions, poll tallies and pins all reach the same audience a
// message does — they just do not deserve their own shard machinery.
func (s *Service) eachMember(ctx context.Context, chatID string, fn func(userID string)) error {
	ids, hot, err := s.members(ctx, chatID)
	if err != nil {
		return err
	}
	if !hot {
		for _, uid := range ids {
			fn(uid)
		}
		return nil
	}
	after := ""
	for {
		page, err := s.chats.MemberIDsPage(ctx, chatID, after, fanoutShardSize)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		for _, uid := range page {
			fn(uid)
		}
		after = page[len(page)-1]
		if len(page) < fanoutShardSize {
			return nil
		}
	}
}

// deliverNew routes a NEW message to a set of members (offline → push). Shared by
// the inline path and the sharded (onShard) path.
//
// Recipients are resolved in PAGES rather than one batch, even though the inline
// path is already bounded by fanoutShardThreshold. The bound on a Redis pipeline
// and on a map of node→recipients should come from this function, not from a
// constant somewhere else that happens to be small today.
func (s *Service) deliverNew(ctx context.Context, members []string, body wire.NewMessageBody) {
	payload := wire.Marshal(body)
	for start := 0; start < len(members); start += routeBatchSize {
		end := min(start+routeBatchSize, len(members))
		for _, uid := range s.routeMany(ctx, members[start:end], wire.MsgNew, payload) {
			// The sender's own absence is not news: they know they sent it, and a
			// notification for your own message is a bug users report as one.
			if uid == body.SenderID {
				continue
			}
			// And a muted chat gets no notification. The message is still delivered
			// and still waits in history — muting silences the buzz, not the chat.
			if s.muted(ctx, body.ChatID, uid) {
				metrics.PushSuppressedMuted.Inc()
				continue
			}
			s.enqueuePush(ctx, uid, body)
		}
	}
}

// broadcast delivers a chat-wide event to every member, optionally skipping one
// (normally the person who caused it).
//
// Every chat-wide event used to walk members one at a time through route, which
// made a read receipt or a typing indicator in a 200-member group cost 200
// sequential router round trips. They all reach the same audience a message does
// and none of them deserves its own machinery, so they share this.
func (s *Service) broadcast(ctx context.Context, chatID string, typ wire.MsgType, payload []byte, skipUser string) error {
	batch := make([]string, 0, routeBatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		s.routeMany(ctx, batch, typ, payload)
		batch = batch[:0]
	}
	if err := s.eachMember(ctx, chatID, func(uid string) {
		if uid == skipUser {
			return
		}
		batch = append(batch, uid)
		if len(batch) == routeBatchSize {
			flush()
		}
	}); err != nil {
		return err
	}
	flush()
	return nil
}

// onShard delivers one chunk of a hot chat's recipients. Many workers run this
// concurrently for the same message, one chunk each.
func (s *Service) onShard(ctx context.Context, e eventbus.Event) error {
	ctx, span := tracing.Start(tracing.Extract(ctx, e.Headers), "fanout.onShard")
	defer span.End()
	var job wire.FanoutShardBody
	if err := wire.Unmarshal(e.Data, &job); err != nil {
		return err
	}
	s.deliverNew(ctx, job.Members, job.Body)
	return nil
}

func (s *Service) onRead(ctx context.Context, e eventbus.Event) error {
	ctx, span := tracing.Start(tracing.Extract(ctx, e.Headers), "fanout.onRead")
	defer span.End()
	var body wire.ReadUpdateBody
	if err := wire.Unmarshal(e.Data, &body); err != nil {
		return err
	}
	// The reader does not need their own receipt back.
	return s.broadcast(ctx, body.ChatID, wire.MsgReadUpd, wire.Marshal(body), body.UserID)
}

// onReaction delivers a reaction change to every chat member. Unlike a new
// message it is NOT pushed when offline (a reaction is not worth a notification)
// — offline devices pick it up with the message on next sync. The reactor's own
// other devices DO receive it, so multi-device stays consistent.
func (s *Service) onReaction(ctx context.Context, e eventbus.Event) error {
	ctx, span := tracing.Start(tracing.Extract(ctx, e.Headers), "fanout.onReaction")
	defer span.End()
	var body wire.ReactUpdateBody
	if err := wire.Unmarshal(e.Data, &body); err != nil {
		return err
	}
	// No skip: the reactor's OWN other devices need this, or multi-device
	// disagrees about what is on the message.
	return s.broadcast(ctx, body.ChatID, wire.MsgReactUpd, wire.Marshal(body), "")
}

func (s *Service) onTyping(ctx context.Context, e eventbus.Event) error {
	ctx, span := tracing.Start(tracing.Extract(ctx, e.Headers), "fanout.onTyping")
	defer span.End()
	var body wire.TypingBody
	if err := wire.Unmarshal(e.Data, &body); err != nil {
		return err
	}
	// The typist does not need their own indicator back. This is the
	// highest-frequency chat-wide event there is, which is why it mattered most
	// that it stopped costing one router round trip per member.
	return s.broadcast(ctx, body.ChatID, wire.MsgTyping, wire.Marshal(body), body.UserID)
}

// onPresence delivers a user's online/last-seen transition to the people who are
// entitled to it: the peers of their DIRECT chats.
//
// The audience is the whole design question, and "everyone who shares any chat"
// is the wrong answer. Presence flips on every connect and disconnect, so that
// rule would make one flaky mobile connection fan out to every member of every
// group the user belongs to — a reconnect storm turning into a membership-sized
// multiplication of frames, for a decoration. A 1:1 chat is where "last seen" is
// actually shown, and its audience is exactly one person.
//
// Delivery is best-effort in the same sense as typing: the frame rides the
// droppable QoS lane, and presence has a TTL behind it, so a lost transition
// corrects itself.
func (s *Service) onPresence(ctx context.Context, e eventbus.Event) error {
	var body wire.PresenceBody
	if err := wire.Unmarshal(e.Data, &body); err != nil {
		return err
	}
	if body.UserID == "" {
		return nil
	}
	peers, err := s.directPeers(ctx, body.UserID)
	if err != nil {
		// A registry read failing must not make the bus redeliver forever: presence is
		// ephemeral, and the next transition (or the TTL) supersedes this one.
		s.log.Warn("presence audience lookup failed", "user", body.UserID, "err", err)
		return nil
	}
	payload := wire.Marshal(body)
	for _, uid := range peers {
		// The audience is filtered here rather than at the socket, because this is
		// where it is known: by the time a frame reaches a connection the only
		// thing left to do is drop it, and the routing work is already paid for.
		if !s.mayAnnounce(ctx, body.UserID, uid) {
			continue
		}
		s.route(ctx, uid, "", wire.MsgPresence, payload)
	}
	return nil
}

// mayAnnounce reports whether owner's presence may be delivered to viewer.
//
// A lookup failure hides the transition. Presence is ephemeral and the next one
// supersedes it, so the cost of a false negative is a stale dot for a few
// seconds; the cost of failing open is announcing someone who asked to be
// invisible, which is not recoverable by waiting.
func (s *Service) mayAnnounce(ctx context.Context, ownerID, viewerID string) bool {
	if s.audience == nil {
		return true
	}
	ok, err := s.audience.MaySeePresence(ctx, ownerID, viewerID)
	if err != nil {
		s.log.Warn("presence audience check failed", "user", ownerID, "err", err)
		return false
	}
	return ok
}

// directPeers lists the other side of every direct chat a user is in, de-duplicated
// (the same person can only be in one direct chat with them, but a defensive set
// costs nothing and keeps a duplicated row from doubling the fanout).
func (s *Service) directPeers(ctx context.Context, userID string) ([]string, error) {
	seen := make(map[string]struct{})
	out := make([]string, 0, 8)
	after := ""
	for pages := 0; pages < maxPresencePages; pages++ {
		page, err := s.chats.UserChats(ctx, userID, after, presencePageSize)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return out, nil
		}
		for _, sum := range page {
			if sum.Chat == nil {
				continue
			}
			after = sum.Chat.ID
			if sum.Chat.Type != model.ChatDirect || sum.PeerID == "" || sum.PeerID == userID {
				continue
			}
			if _, dup := seen[sum.PeerID]; dup {
				continue
			}
			seen[sum.PeerID] = struct{}{}
			out = append(out, sum.PeerID)
		}
		if len(page) < presencePageSize {
			return out, nil
		}
	}
	return out, nil
}

// RouteSecret relays an opaque E2E ciphertext to a specific device on whatever
// node holds it (multi-node secret delivery). Called by the gateway handler.
func (s *Service) RouteSecret(ctx context.Context, toUser, toDevice string, body wire.SecretMsgBody) {
	s.route(ctx, toUser, toDevice, wire.MsgSecretRecv, wire.Marshal(body))
}

// enqueuePush asks for a notification for one offline recipient.
//
// The preview is opt-IN, per recipient, and defaults to absent. It used to be
// included unconditionally: every notification carried up to 120 runes of the
// message, and the provider that receives it is Apple or Google. So a third party
// saw the contents of every conversation on the system — a wider disclosure than
// anything end-to-end encryption was protecting against, since E2E guards against
// the server and this was the server handing the text over.
//
// It is the RECIPIENT's setting, not the sender's. The person whose device shows
// the notification, and whose provider account receives it, is the one making the
// trade — and they are the only party who can.
func (s *Service) enqueuePush(ctx context.Context, userID string, msg wire.NewMessageBody) {
	job := pushJob{
		UserID: userID, ChatID: msg.ChatID, MessageID: msg.MessageID,
		SenderID: msg.SenderID,
	}
	if s.previews != nil && s.previews.WantsPushPreview(ctx, userID) {
		job.Preview = preview(msg.Text)
	}
	s.publishPush(ctx, userID, job)
}

// preview trims a notification body. The cut is by RUNE, not by byte: slicing
// mid-character would hand the push provider invalid UTF-8, which is a JSON
// encoding error at best and a mangled notification at worst — and the first
// users to hit it would be everyone who does not type in ASCII.
func preview(text string) string {
	const max = 120
	if len(text) <= max {
		return text
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

// onCallState delivers a call room's lifecycle/roster change to its
// participants. Only participants receive it — a call is not chat-wide news, and
// the roster reveals who is talking to whom. An invited-but-offline user gets a
// push so their phone can ring.
func (s *Service) onCallState(ctx context.Context, e eventbus.Event) error {
	ctx, span := tracing.Start(tracing.Extract(ctx, e.Headers), "fanout.onCallState")
	defer span.End()
	var body wire.CallStateBody
	if err := wire.Unmarshal(e.Data, &body); err != nil {
		return err
	}
	payload := wire.Marshal(body)
	for _, p := range body.Participants {
		if p.State == "left" || p.State == "declined" {
			continue // they are out of the room; no need to keep ringing them
		}
		delivered := s.route(ctx, p.UserID, "", wire.MsgCallState, payload)
		// An offline invitee still needs their device to ring.
		if delivered == 0 && p.State == "invited" && body.State == "ringing" {
			s.enqueueCallPush(ctx, p.UserID, body)
		}
	}
	return nil
}

// enqueueCallPush asks the notification worker to wake an offline invitee.
func (s *Service) enqueueCallPush(ctx context.Context, userID string, c wire.CallStateBody) {
	job := pushJob{
		UserID: userID, ChatID: c.ChatID, CallID: c.CallID,
		SenderID: c.InitiatorID, Preview: "Incoming " + c.Kind + " call",
	}
	s.publishPush(ctx, userID, job)
}

/*
 * pushJob mirrors notify.PushJob, which the worker decodes with encoding/json.
 *
 * It is JSON and not the wire codec on purpose, and the type is explicit rather
 * than a map for the same reason: wire.Marshal is protobuf-backed and has no
 * mapping for an ad-hoc map, so it returned an ERROR that Marshal's `b, _ :=`
 * discarded — publishing zero bytes. The worker then failed to decode an empty
 * payload and no notification was ever delivered, with nothing in the path
 * reporting a problem. A named struct keeps the contract checkable at compile
 * time; duplicated rather than imported so fanout does not depend on notify.
 */
type pushJob struct {
	UserID    string `json:"user_id"`
	ChatID    string `json:"chat_id"`
	MessageID string `json:"message_id,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	SenderID  string `json:"sender_id"`
	Preview   string `json:"preview"`
}

func (s *Service) publishPush(ctx context.Context, userID string, job pushJob) {
	data, err := json.Marshal(job)
	if err != nil {
		s.log.Warn("encode push job failed", "user", userID, "err", err)
		return
	}
	if err := s.bus.Publish(ctx, eventbus.Event{
		Subject: eventbus.SubjNotifyPush, Key: userID, Data: data,
	}); err != nil {
		s.log.Warn("enqueue push failed", "user", userID, "err", err)
	}
}

// onPollState delivers a poll's tally to every chat member. Like a reaction it
// is not push-worthy on its own — the poll's QUESTION was a normal message and
// already generated a notification.
func (s *Service) onPollState(ctx context.Context, e eventbus.Event) error {
	ctx, span := tracing.Start(tracing.Extract(ctx, e.Headers), "fanout.onPollState")
	defer span.End()
	var body wire.PollStateBody
	if err := wire.Unmarshal(e.Data, &body); err != nil {
		return err
	}
	// Defensive: a broadcast must never leak one member's selections to others.
	body.MyVotes = nil
	return s.broadcast(ctx, body.ChatID, wire.MsgPollState, wire.Marshal(body), "")
}

// onPinned delivers a chat's updated pin set to every member. Pins are chat-wide
// state, so unlike drafts this goes to the whole room.
func (s *Service) onPinned(ctx context.Context, e eventbus.Event) error {
	ctx, span := tracing.Start(tracing.Extract(ctx, e.Headers), "fanout.onPinned")
	defer span.End()
	var body wire.PinnedBody
	if err := wire.Unmarshal(e.Data, &body); err != nil {
		return err
	}
	return s.broadcast(ctx, body.ChatID, wire.MsgPinned, wire.Marshal(body), "")
}
