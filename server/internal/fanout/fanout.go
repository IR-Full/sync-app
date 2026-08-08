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
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/synapse-chat/synapse/internal/metrics"
	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/router"
	"github.com/synapse-chat/synapse/internal/tracing"
	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// New builds the fanout service.
func New(bus eventbus.Bus, chats Chats, rtr router.Router, log *slog.Logger) *Service {
	return &Service{bus: bus, chats: chats, router: rtr, log: log,
		cache: map[string]memberEntry{}, lastSweep: time.Now()}
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
func (s *Service) route(ctx context.Context, userID, deviceID string, typ wire.MsgType, body []byte) int {
	nodes, err := s.router.NodesFor(ctx, userID)
	if err != nil {
		s.log.Warn("router lookup failed", "user", userID, "err", err)
		return 0
	}
	nd := router.NodeDelivery{UserID: userID, DeviceID: deviceID, Type: uint16(typ), Body: body}
	data := nd.Encode()
	for _, node := range nodes {
		_ = s.bus.Publish(ctx, eventbus.Event{Subject: router.DeliverSubject(node), Key: userID, Data: data})
	}
	return len(nodes)
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
func (s *Service) deliverNew(ctx context.Context, members []string, body wire.NewMessageBody) {
	payload := wire.Marshal(body)
	for _, uid := range members {
		delivered := s.route(ctx, uid, "", wire.MsgNew, payload)
		if delivered == 0 && uid != body.SenderID {
			s.enqueuePush(ctx, uid, body)
		}
	}
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
	payload := wire.Marshal(body)
	if err := s.eachMember(ctx, body.ChatID, func(uid string) {
		if uid == body.UserID {
			return // the reader/typist does not need their own event back
		}
		s.route(ctx, uid, "", wire.MsgReadUpd, payload)
	}); err != nil {
		return err
	}
	return nil
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
	payload := wire.Marshal(body)
	if err := s.eachMember(ctx, body.ChatID, func(uid string) {
		s.route(ctx, uid, "", wire.MsgReactUpd, payload)
	}); err != nil {
		return err
	}
	return nil
}

func (s *Service) onTyping(ctx context.Context, e eventbus.Event) error {
	ctx, span := tracing.Start(tracing.Extract(ctx, e.Headers), "fanout.onTyping")
	defer span.End()
	var body wire.TypingBody
	if err := wire.Unmarshal(e.Data, &body); err != nil {
		return err
	}
	payload := wire.Marshal(body)
	if err := s.eachMember(ctx, body.ChatID, func(uid string) {
		if uid == body.UserID {
			return // the reader/typist does not need their own event back
		}
		s.route(ctx, uid, "", wire.MsgTyping, payload)
	}); err != nil {
		return err
	}
	return nil
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
		s.route(ctx, uid, "", wire.MsgPresence, payload)
	}
	return nil
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

func (s *Service) enqueuePush(ctx context.Context, userID string, msg wire.NewMessageBody) {
	job := map[string]any{
		"user_id": userID, "chat_id": msg.ChatID, "message_id": msg.MessageID,
		"sender_id": msg.SenderID, "preview": preview(msg.Text),
	}
	if err := s.bus.Publish(ctx, eventbus.Event{Subject: eventbus.SubjNotifyPush, Key: userID, Data: wire.Marshal(job)}); err != nil {
		s.log.Warn("enqueue push failed", "user", userID, "err", err)
	}
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
	job := map[string]any{
		"user_id": userID, "chat_id": c.ChatID, "call_id": c.CallID,
		"sender_id": c.InitiatorID, "preview": "Incoming " + c.Kind + " call",
	}
	if err := s.bus.Publish(ctx, eventbus.Event{Subject: eventbus.SubjNotifyPush, Key: userID, Data: wire.Marshal(job)}); err != nil {
		s.log.Warn("enqueue call push failed", "user", userID, "err", err)
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
	payload := wire.Marshal(body)
	if err := s.eachMember(ctx, body.ChatID, func(uid string) {
		s.route(ctx, uid, "", wire.MsgPollState, payload)
	}); err != nil {
		return err
	}
	return nil
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
	payload := wire.Marshal(body)
	if err := s.eachMember(ctx, body.ChatID, func(uid string) {
		s.route(ctx, uid, "", wire.MsgPinned, payload)
	}); err != nil {
		return err
	}
	return nil
}
