// Package pin implements pinned messages and cross-device draft sync.
//
// The two live together because both are "chat-adjacent state" that is neither a
// message nor chat metadata — but their VISIBILITY is opposite, and that shapes
// delivery: a pin is chat-wide (fanout to every member), a draft is private to
// one user and synced only to THAT user's other devices.
package pin

import (
	"context"
	"unicode/utf8"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/internal/tracing"
	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// New builds the pin/draft service.
func New(pins store.PinStore, drafts store.DraftStore, chats Chats, bus eventbus.Bus) *Service {
	return &Service{pins: pins, drafts: drafts, chats: chats, bus: bus}
}

// Pin pins a message and broadcasts the new pin set to the chat.
func (s *Service) Pin(ctx context.Context, chatID, messageID, userID string, now int64) error {
	ctx, span := tracing.Start(ctx, "pin.Pin")
	defer span.End()
	if err := s.authorize(ctx, chatID, userID); err != nil {
		return err
	}
	if err := s.pins.Pin(ctx, &model.PinnedMessage{
		ChatID: chatID, MessageID: messageID, PinnedBy: userID, PinnedAt: now,
	}); err != nil {
		return err
	}
	s.broadcast(ctx, chatID)
	return nil
}

// Unpin removes a pin and broadcasts the new set.
func (s *Service) Unpin(ctx context.Context, chatID, messageID, userID string) error {
	if err := s.authorize(ctx, chatID, userID); err != nil {
		return err
	}
	if err := s.pins.Unpin(ctx, chatID, messageID); err != nil {
		return err
	}
	s.broadcast(ctx, chatID)
	return nil
}

// ListPins returns a chat's pins for any member.
func (s *Service) ListPins(ctx context.Context, chatID, userID string) ([]*model.PinnedMessage, error) {
	member, err := s.chats.IsMember(ctx, chatID, userID)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, ErrForbidden
	}
	return s.pins.ListPins(ctx, chatID)
}

func (s *Service) authorize(ctx context.Context, chatID, userID string) error {
	ok, err := s.chats.CanPin(ctx, chatID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrForbidden
	}
	return nil
}

// broadcast publishes the chat's current pin set for fanout to members.
func (s *Service) broadcast(ctx context.Context, chatID string) {
	pins, err := s.pins.ListPins(ctx, chatID)
	if err != nil {
		return
	}
	body := wire.PinnedBody{ChatID: chatID}
	for _, p := range pins {
		body.Pins = append(body.Pins, wire.Pin{
			MessageID: p.MessageID, PinnedBy: p.PinnedBy, PinnedAt: p.PinnedAt,
		})
	}
	_ = s.bus.Publish(ctx, eventbus.Event{
		Subject: eventbus.SubjPinned,
		Key:     chatID,
		Data:    wire.Marshal(body),
		Headers: tracing.Inject(ctx),
	})
}

// SetDraft saves what a user is composing. Membership is required so a draft
// cannot be parked against a chat the user has no access to. Clearing the text
// deletes the draft — that is what "I erased what I typed" must mean, otherwise
// an empty box on one device would not empty on the others.
func (s *Service) SetDraft(ctx context.Context, userID, chatID, text, replyTo string, now int64) error {
	if utf8.RuneCountInString(text) > MaxDraftLen {
		return ErrTooLong
	}
	member, err := s.chats.IsMember(ctx, chatID, userID)
	if err != nil {
		return err
	}
	if !member {
		return ErrForbidden
	}
	if text == "" && replyTo == "" {
		return s.drafts.DeleteDraft(ctx, userID, chatID)
	}
	return s.drafts.SetDraft(ctx, &model.Draft{
		UserID: userID, ChatID: chatID, Text: text, ReplyTo: replyTo, UpdatedAt: now,
	})
}

// SyncDrafts returns ONE PAGE of the user's drafts changed after `since`, plus
// the new cursor — the same incremental shape as contact sync, including how the
// page is cut on a timestamp boundary (see contact.Service.Sync for why).
func (s *Service) SyncDrafts(ctx context.Context, userID string, since int64) ([]*model.Draft, int64, error) {
	rows, err := s.drafts.ListDrafts(ctx, userID, since, SyncPageSize+1)
	if err != nil {
		return nil, 0, err
	}
	if len(rows) <= SyncPageSize {
		high := since
		for _, d := range rows {
			if d.UpdatedAt > high {
				high = d.UpdatedAt
			}
		}
		return rows, high, nil
	}
	boundary := rows[SyncPageSize].UpdatedAt
	page := rows[:SyncPageSize]
	for len(page) > 0 && page[len(page)-1].UpdatedAt == boundary {
		page = page[:len(page)-1]
	}
	if len(page) == 0 {
		return rows[:SyncPageSize], boundary, nil
	}
	return page, page[len(page)-1].UpdatedAt, nil
}
