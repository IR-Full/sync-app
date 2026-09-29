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
	"sync"
	"time"

	"github.com/IR-Full/sync-app/server/pkg/eventbus"
	"github.com/IR-Full/sync-app/server/pkg/ratelimit"
	"github.com/IR-Full/sync-app/server/pkg/wire"
)

// AbuseEvent is a recorded detection (would persist to abuse_events + audit_logs).
type AbuseEvent struct {
	UserID    string
	ChatID    string
	MessageID string
	Rule      string
	Detail    string
	At        int64
}

// Service applies moderation rules to message events.
type Service struct {
	bus    eventbus.Bus
	log    *slog.Logger
	banned []string
	spam   *ratelimit.Limiter // per-user message velocity
	mu     sync.Mutex
	events []AbuseEvent // in-memory ring for inspection (bounded)
}

// New builds the moderation service. bannedTerms are matched against normalized
// text (see normalize), so look-alike alphabets, invisible characters, digit
// substitutions and injected punctuation do not evade the filter.
func New(bus eventbus.Bus, bannedTerms []string, log *slog.Logger) *Service {
	terms := make([]string, 0, len(bannedTerms))
	for _, t := range bannedTerms {
		// A term that normalizes to nothing (punctuation only) would match every
		// message, so it is dropped rather than allowed to flag everything.
		if n := normalize(t); n != "" {
			terms = append(terms, n)
		}
	}
	return &Service{
		bus:    bus,
		log:    log,
		banned: terms,
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
	// Rule 1: banned-term filter, on normalized text so the match survives
	// homoglyphs, zero-width characters and leetspeak.
	normalized := normalize(b.Text)
	for _, term := range s.banned {
		if strings.Contains(normalized, term) {
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
