// Package presence is the Presence Service (Section 6). Presence is ephemeral,
// high-churn state (online flag, last-seen, typing) that must be fast and is
// acceptable to lose on restart — so it lives in Redis in production (with TTLs
// that auto-expire stale "online" markers) and in memory for local dev. Typing
// is fire-and-forget: relayed via the event bus, never persisted.
package presence

import (
	"context"
	"time"

	"github.com/synapse-chat/synapse/internal/metrics"
	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/tracing"
	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// New builds the presence service. ttl is how long an "online" marker survives
// without a heartbeat (the gateway refreshes it on each ping).
func New(backend Backend, bus eventbus.Bus, ttl time.Duration) *Service {
	if ttl == 0 {
		ttl = 60 * time.Second
	}
	return &Service{backend: backend, bus: bus, ttl: ttl}
}

// Online marks a user online and publishes the transition.
func (s *Service) Online(ctx context.Context, userID string) error {
	if err := s.backend.SetOnline(ctx, userID, s.ttl); err != nil {
		return err
	}
	return s.publish(ctx, model.Presence{UserID: userID, Online: true, LastSeenMs: nowMs()})
}

// Heartbeat refreshes the online TTL (called on each ping).
func (s *Service) Heartbeat(ctx context.Context, userID string) error {
	return s.backend.SetOnline(ctx, userID, s.ttl)
}

// Offline marks a user offline with a last-seen timestamp.
func (s *Service) Offline(ctx context.Context, userID string) error {
	now := nowMs()
	if err := s.backend.SetOffline(ctx, userID, now); err != nil {
		return err
	}
	return s.publish(ctx, model.Presence{UserID: userID, Online: false, LastSeenMs: now})
}

// Get returns a user's current presence.
func (s *Service) Get(ctx context.Context, userID string) (model.Presence, error) {
	return s.backend.Get(ctx, userID)
}

// Typing relays a typing indicator for a chat via the bus (fanout delivers it).
func (s *Service) Typing(ctx context.Context, chatID, userID string, active bool) error {
	return s.bus.Publish(ctx, eventbus.Event{
		Subject: eventbus.SubjTyping,
		Key:     chatID,
		Headers: tracing.Inject(ctx),
		Data:    wire.Marshal(wire.TypingBody{ChatID: chatID, UserID: userID, Active: active}),
	})
}

func (s *Service) publish(ctx context.Context, p model.Presence) error {
	state := "offline"
	if p.Online {
		state = "online"
	}
	metrics.PresenceTransitions.WithLabelValues(state).Inc()
	return s.bus.Publish(ctx, eventbus.Event{
		Subject: eventbus.SubjPresence,
		Key:     p.UserID,
		Data:    wire.Marshal(wire.PresenceBody{UserID: p.UserID, Online: p.Online, LastSeenMs: p.LastSeenMs}),
	})
}

func nowMs() int64 { return time.Now().UnixMilli() }

//  --- in-memory backend ---

// NewMemoryBackend returns an in-process presence backend for dev/tests.
func NewMemoryBackend() Backend {
	return &memoryBackend{data: map[string]model.Presence{}}
}

func (m *memoryBackend) SetOnline(_ context.Context, userID string, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[userID] = model.Presence{UserID: userID, Online: true, LastSeenMs: nowMs()}
	return nil
}

func (m *memoryBackend) SetOffline(_ context.Context, userID string, lastSeen int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[userID] = model.Presence{UserID: userID, Online: false, LastSeenMs: lastSeen}
	return nil
}

func (m *memoryBackend) Get(_ context.Context, userID string) (model.Presence, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.data[userID]
	if !ok {
		return model.Presence{UserID: userID, Online: false}, nil
	}
	return p, nil
}
