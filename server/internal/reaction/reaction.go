// Package reaction is the Reactions service: emoji reactions on messages with
// toggle semantics. It authorizes against chat membership, persists the toggle,
// and emits message.reaction on the bus so fanout delivers the update to every
// member — the same write→event→fanout shape as the message path, so reactions
// scale and degrade the same way.
package reaction

import (
	"context"
	"unicode/utf8"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/internal/tracing"
	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// New builds the reaction service.
func New(st store.ReactionStore, chats Chats, bus eventbus.Bus) *Service {
	return &Service{store: st, chats: chats, bus: bus}
}

// Toggle applies a reaction and returns whether it was added (false = removed)
// plus the full per-emoji tally after the change, so a client renders without a
// re-fetch. It publishes message.reaction for fanout.
func (s *Service) Toggle(ctx context.Context, chatID, messageID, userID, emoji string, now int64) (bool, map[string]int, error) {
	ctx, span := tracing.Start(ctx, "reaction.Toggle")
	defer span.End()

	if emoji == "" || utf8.RuneCountInString(emoji) > maxEmojiRunes || !utf8.ValidString(emoji) {
		return false, nil, ErrBadEmoji
	}
	member, err := s.chats.IsMember(ctx, chatID, userID)
	if err != nil {
		return false, nil, err
	}
	if !member {
		return false, nil, ErrForbidden
	}

	added, err := s.store.SetReaction(ctx, &model.Reaction{
		ChatID: chatID, MessageID: messageID, UserID: userID, Emoji: emoji, CreatedAt: now,
	})
	if err != nil {
		return false, nil, err
	}

	counts, err := s.counts(ctx, chatID, messageID)
	if err != nil {
		return added, nil, err
	}

	upd := wire.ReactUpdateBody{
		ChatID: chatID, MessageID: messageID, UserID: userID,
		Emoji: emoji, Added: added, Counts: counts,
	}
	_ = s.bus.Publish(ctx, eventbus.Event{
		Subject: eventbus.SubjReaction,
		Key:     chatID,
		Data:    wire.Marshal(upd),
		Headers: tracing.Inject(ctx),
	})
	return added, counts, nil
}

// counts tallies reactions per emoji for a message.
func (s *Service) counts(ctx context.Context, chatID, messageID string) (map[string]int, error) {
	rs, err := s.store.ListReactions(ctx, chatID, messageID)
	if err != nil {
		return nil, err
	}
	if len(rs) == 0 {
		return nil, nil
	}
	out := make(map[string]int, len(rs))
	for _, r := range rs {
		out[r.Emoji]++
	}
	return out, nil
}

// List returns the per-emoji tally for a message (used by history hydration).
func (s *Service) List(ctx context.Context, chatID, messageID string) (map[string]int, error) {
	return s.counts(ctx, chatID, messageID)
}
