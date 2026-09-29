// Package schedule implements deferred sending ("send this at 9am") and the
// self-destruct reaper.
//
// A pending send lives OUTSIDE the message log until it is due. That matters for
// correctness, not just tidiness: a scheduled message must not hold a chat
// sequence number (cancelling it would leave a permanent gap in a gap-free
// sequence) and must not be visible to history, search, or fanout before its
// time.
//
// The dispatcher claims due rows with SKIP LOCKED, so running it on several
// nodes never sends the same message twice.
package schedule

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/message"
	"github.com/SyncApp-chat/SyncApp/internal/metrics"
	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/pkg/id"
)

var (
	// ErrForbidden means the caller may not post to the target chat.
	ErrForbidden = errors.New("schedule: forbidden")
	// ErrPastTime means the requested send time is not in the future.
	ErrPastTime = errors.New("schedule: send time must be in the future")
	// ErrTooFar means the send time exceeds the allowed horizon.
	ErrTooFar = errors.New("schedule: send time too far ahead")
)

// MaxHorizon bounds how far ahead a message may be scheduled. Without a cap, a
// pending table becomes an unbounded, unaudited store of future traffic.
const MaxHorizon = 365 * 24 * time.Hour

// Retention for fired sends. Long enough that "did my scheduled message go?"
// can still be answered from the pending table; short enough that it never
// becomes a duplicate of the message log.
const (
	sentRetain = time.Hour
	purgeEvery = 10 * time.Minute
	purgeBatch = 500
)

// Chats authorizes posting into the destination chat.
type Chats interface {
	CanPost(ctx context.Context, chatID, userID string) (bool, error)
}

// BlockGate reports whether a block between the two people in a 1:1 chat
// refuses this sender's message. Optional: without one, only CanPost is checked.
type BlockGate interface {
	Blocked(ctx context.Context, chatID, senderID string) (bool, error)
}

// Sender delivers a due message through the normal write path (so it gets a seq,
// an outbox event, and fanout exactly like a live send).
type Sender interface {
	Send(ctx context.Context, in message.SendInput) (*model.Message, bool, error)
	ExpireDue(ctx context.Context, now int64, limit int) (int, error)
}

// Service schedules sends and runs the dispatcher + expiry reaper.
type Service struct {
	store  store.ScheduleStore
	chats  Chats
	sender Sender
	blocks BlockGate
	ids    *id.Generator
	log    *slog.Logger
}

// Input describes a deferred send.
type Input struct {
	ChatID     string
	SenderID   string
	Text       string
	MediaRef   string
	Attachment *model.Attachment
	ReplyTo    string
	TTLSeconds int32
	SendAt     int64 // unix millis
}

// New builds the schedule service.
func New(st store.ScheduleStore, chats Chats, sender Sender, ids *id.Generator, log *slog.Logger) *Service {
	return &Service{store: st, chats: chats, sender: sender, ids: ids, log: log}
}

// WithBlockGate makes the dispatcher honour blocks at fire time.
func (s *Service) WithBlockGate(g BlockGate) *Service { s.blocks = g; return s }

// Schedule validates and stores a pending send. Permission is checked NOW (at
// schedule time) and again when it fires, so losing access in between cannot
// deliver the message.
func (s *Service) Schedule(ctx context.Context, in Input, now int64) (*model.ScheduledMessage, error) {
	if in.SendAt <= now {
		return nil, ErrPastTime
	}
	if in.SendAt > now+MaxHorizon.Milliseconds() {
		return nil, ErrTooFar
	}
	ok, err := s.chats.CanPost(ctx, in.ChatID, in.SenderID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrForbidden
	}
	m := &model.ScheduledMessage{
		ID: s.ids.NextString(), ChatID: in.ChatID, SenderID: in.SenderID,
		Text: in.Text, MediaRef: in.MediaRef, Attachment: in.Attachment,
		ReplyTo: in.ReplyTo, TTLSeconds: in.TTLSeconds,
		SendAt: in.SendAt, CreatedAt: now,
	}
	if err := s.store.CreateScheduled(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

// Cancel removes a pending send (owner only — enforced in the store's WHERE).
func (s *Service) Cancel(ctx context.Context, id, senderID string) error {
	return s.store.CancelScheduled(ctx, id, senderID)
}

// List returns a user's pending sends in a chat.
func (s *Service) List(ctx context.Context, senderID, chatID string) ([]*model.ScheduledMessage, error) {
	return s.store.ListScheduled(ctx, senderID, chatID)
}

// Run drives the dispatcher and the self-destruct reaper until ctx is cancelled.
func (s *Service) Run(ctx context.Context, tick time.Duration) {
	if tick <= 0 {
		tick = 5 * time.Second
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	// Fired rows are collected on a much slower cadence than they are dispatched:
	// a sent row is only a short audit trail ("this went out at T"), and the
	// message it became lives in the message log from that moment.
	purge := time.NewTicker(purgeEvery)
	defer purge.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.dispatchDue(ctx)
			if n, err := s.sender.ExpireDue(ctx, time.Now().UnixMilli(), 200); err != nil {
				s.log.Warn("expire due", "err", err)
			} else if n > 0 {
				s.log.Info("self-destructed messages", "count", n)
			}
		case <-purge.C:
			s.purgeSent(ctx)
		}
	}
}

func (s *Service) purgeSent(ctx context.Context) {
	before := time.Now().Add(-sentRetain).UnixMilli()
	for {
		n, err := s.store.PurgeSentScheduled(ctx, before, purgeBatch)
		if err != nil {
			s.log.Warn("scheduled purge failed", "err", err)
			return
		}
		if n > 0 {
			metrics.RowsPurged.WithLabelValues("scheduled_messages").Add(float64(n))
		}
		if n < purgeBatch {
			return
		}
	}
}

// dispatchDue sends everything whose time has come.
func (s *Service) dispatchDue(ctx context.Context) {
	due, err := s.store.ClaimDueScheduled(ctx, time.Now().UnixMilli(), 100)
	if err != nil {
		s.log.Warn("claim due scheduled", "err", err)
		return
	}
	for _, m := range due {
		// Re-check permission at FIRE time: the sender may have been removed from
		// the chat since scheduling, and a scheduled message must not outlive that.
		ok, err := s.chats.CanPost(ctx, m.ChatID, m.SenderID)
		if err != nil || !ok {
			s.log.Info("dropping scheduled message: sender may no longer post",
				"id", m.ID, "chat", m.ChatID, "user", m.SenderID)
			continue
		}
		// Same for a block placed after scheduling: the gateway refuses the send
		// at scheduling time, and without this a message queued the minute before
		// would still arrive the day after.
		if s.blocks != nil {
			if blocked, err := s.blocks.Blocked(ctx, m.ChatID, m.SenderID); err != nil || blocked {
				s.log.Info("dropping scheduled message: blocked", "id", m.ID, "chat", m.ChatID)
				continue
			}
		}
		if _, _, err := s.sender.Send(ctx, message.SendInput{
			SenderID: m.SenderID, ChatID: m.ChatID,
			DedupKey:   "sched:" + m.ID, // idempotent: a retry cannot double-send
			Text:       m.Text,
			MediaRef:   m.MediaRef,
			Attachment: m.Attachment,
			ReplyTo:    m.ReplyTo,
			TTLSeconds: m.TTLSeconds,
		}); err != nil {
			s.log.Warn("send scheduled message", "id", m.ID, "err", err)
		}
	}
}
