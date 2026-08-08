// Package moderation is the Moderation/Abuse Service (Sections 6, 14). It
// consumes message events and applies anti-abuse rules: a banned-term filter and
// a per-user spam-velocity limit. Detections are recorded as abuse events and
// (in production) emitted as abuse.action for enforcement. This service is
// intentionally advisory in the MVP — it observes and records rather than
// blocking the write path, so moderation latency never affects delivery.
package moderation

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/ratelimit"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// New builds the moderation service. bannedTerms are matched case-insensitively.
func New(bus eventbus.Bus, bannedTerms []string, log *slog.Logger) *Service {
	lowered := make([]string, len(bannedTerms))
	for i, t := range bannedTerms {
		lowered[i] = strings.ToLower(t)
	}
	return &Service{
		bus:    bus,
		log:    log,
		banned: lowered,
		// Flag users exceeding ~5 msg/s sustained (burst 15).
		spam: ratelimit.NewLimiter(5, 15),
	}
}

// Start subscribes to created/edited messages.
func (s *Service) Start() error {
	if err := s.bus.Subscribe(eventbus.SubjMessageCreated, "moderation", s.onMessage); err != nil {
		return err
	}
	return s.bus.Subscribe(eventbus.SubjMessageEdited, "moderation", s.onMessage)
}

func (s *Service) onMessage(ctx context.Context, e eventbus.Event) error {
	var b wire.NewMessageBody
	if err := wire.Unmarshal(e.Data, &b); err != nil {
		return err
	}
	// Rule 1: banned-term filter.
	lower := strings.ToLower(b.Text)
	for _, term := range s.banned {
		if term != "" && strings.Contains(lower, term) {
			s.record(AbuseEvent{
				UserID: b.SenderID, ChatID: b.ChatID, MessageID: b.MessageID,
				Rule: "banned_term", Detail: term, At: time.Now().UnixMilli(),
			})
			break
		}
	}
	// Rule 2: spam velocity per user.
	if !s.spam.Allow(b.SenderID) {
		s.record(AbuseEvent{
			UserID: b.SenderID, ChatID: b.ChatID, MessageID: b.MessageID,
			Rule: "spam_velocity", Detail: "message rate exceeded", At: time.Now().UnixMilli(),
		})
	}
	return nil
}

func (s *Service) record(ev AbuseEvent) {
	s.log.Warn("abuse detected", "rule", ev.Rule, "user", ev.UserID, "chat", ev.ChatID, "detail", ev.Detail)
	s.mu.Lock()
	s.events = append(s.events, ev)
	if len(s.events) > 1000 {
		s.events = s.events[len(s.events)-1000:]
	}
	s.mu.Unlock()
	// Production: persist + s.bus.Publish(abuse.action) for auto-enforcement.
}

// Events returns a snapshot of recent detections (for an admin endpoint).
func (s *Service) Events() []AbuseEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AbuseEvent, len(s.events))
	copy(out, s.events)
	return out
}
