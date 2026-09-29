// Package poll is the Polls service: a question with fixed options posted into a
// chat, plus voting and live tallies.
//
// A poll is ANCHORED TO A MESSAGE: the question is posted through the normal
// message write path, so the poll appears in history, respects chat permissions,
// and orders with everything else. Only the tally lives in its own tables. That
// keeps polls out of the message hot path while still making them first-class
// chat content.
package poll

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/store"
	"github.com/IR-Full/sync-app/server/internal/tracing"
	"github.com/IR-Full/sync-app/server/pkg/eventbus"
	"github.com/IR-Full/sync-app/server/pkg/id"
	"github.com/IR-Full/sync-app/server/pkg/wire"
)

// Limits keep a poll a poll — not a free-text broadcast channel.
const (
	MaxQuestionLen = 300
	MaxOptionLen   = 100
	MinOptions     = 2
	MaxOptions     = 10
)

var (
	// ErrForbidden means the actor may not act on this poll/chat.
	ErrForbidden = errors.New("poll: forbidden")
	// ErrClosed means the poll no longer accepts votes.
	ErrClosed = errors.New("poll: closed")
	// ErrBadPoll means the question/options failed validation.
	ErrBadPoll = errors.New("poll: invalid question or options")
	// ErrBadOption means the option index is out of range.
	ErrBadOption = errors.New("poll: option out of range")
)

// Chats supplies the membership check.
type Chats interface {
	IsMember(ctx context.Context, chatID, userID string) (bool, error)
}

// Service creates polls, records votes, and broadcasts tallies.
type Service struct {
	store store.PollStore
	chats Chats
	bus   eventbus.Bus
	ids   *id.Generator
}

// CreateInput describes a new poll. MessageID is the message that carries the
// question (created by the caller through the normal write path).
type CreateInput struct {
	ChatID      string
	MessageID   string
	CreatorID   string
	Question    string
	Options     []string
	MultiChoice bool
	Anonymous   bool
}

// New builds the poll service.
func New(st store.PollStore, chats Chats, bus eventbus.Bus, ids *id.Generator) *Service {
	return &Service{store: st, chats: chats, bus: bus, ids: ids}
}

// Create validates and stores a poll, then broadcasts its initial state.
func (s *Service) Create(ctx context.Context, in CreateInput, now int64) (*model.Poll, error) {
	ctx, span := tracing.Start(ctx, "poll.Create")
	defer span.End()

	if err := validate(in.Question, in.Options); err != nil {
		return nil, err
	}
	member, err := s.chats.IsMember(ctx, in.ChatID, in.CreatorID)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, ErrForbidden
	}
	p := &model.Poll{
		ID: s.ids.NextString(), ChatID: in.ChatID, MessageID: in.MessageID,
		CreatorID: in.CreatorID, Question: strings.TrimSpace(in.Question),
		Options: in.Options, MultiChoice: in.MultiChoice, Anonymous: in.Anonymous,
		CreatedAt: now,
	}
	if err := s.store.CreatePoll(ctx, p); err != nil {
		return nil, err
	}
	s.broadcast(ctx, p, nil)
	return p, nil
}

// Vote records a choice and broadcasts the updated tally. Single-choice polls
// replace the voter's previous pick; multi-choice polls toggle the option.
func (s *Service) Vote(ctx context.Context, pollID, userID string, option int32, now int64) (*model.Poll, error) {
	p, err := s.store.GetPoll(ctx, pollID)
	if err != nil {
		return nil, err
	}
	if p.Closed {
		return nil, ErrClosed
	}
	if option < 0 || int(option) >= len(p.Options) {
		return nil, ErrBadOption
	}
	member, err := s.chats.IsMember(ctx, p.ChatID, userID)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, ErrForbidden
	}
	if _, err := s.store.Vote(ctx, &model.PollVote{
		PollID: pollID, UserID: userID, OptionIndex: option, CreatedAt: now,
	}, p.MultiChoice); err != nil {
		// The store re-checks `closed` inside the write transaction, so it — not the
		// read above — is what actually decides. Report it as the same refusal.
		if errors.Is(err, store.ErrPollClosed) {
			return nil, ErrClosed
		}
		return nil, err
	}
	s.broadcast(ctx, p, nil)
	return p, nil
}

// Close stops accepting votes. Only the poll's creator may close it.
func (s *Service) Close(ctx context.Context, pollID, userID string) (*model.Poll, error) {
	p, err := s.store.GetPoll(ctx, pollID)
	if err != nil {
		return nil, err
	}
	if p.CreatorID != userID {
		return nil, ErrForbidden
	}
	if err := s.store.ClosePoll(ctx, pollID); err != nil {
		return nil, err
	}
	p.Closed = true
	s.broadcast(ctx, p, nil)
	return p, nil
}

// Results returns the poll with its current tally and, for a named user, which
// options they picked (so their client can show their own selection).
func (s *Service) Results(ctx context.Context, pollID, userID string) (wire.PollStateBody, error) {
	p, err := s.store.GetPoll(ctx, pollID)
	if err != nil {
		return wire.PollStateBody{}, err
	}
	member, err := s.chats.IsMember(ctx, p.ChatID, userID)
	if err != nil {
		return wire.PollStateBody{}, err
	}
	if !member {
		return wire.PollStateBody{}, ErrForbidden
	}
	mine, err := s.store.VotedOptions(ctx, pollID, userID)
	if err != nil {
		return wire.PollStateBody{}, err
	}
	return s.state(ctx, p, mine), nil
}

// ByMessage resolves the poll attached to a message (clients hold message ids).
func (s *Service) ByMessage(ctx context.Context, messageID string) (*model.Poll, error) {
	return s.store.GetPollByMessage(ctx, messageID)
}

// state renders the wire view of a poll: options with counts, plus the caller's
// own selection when provided.
func (s *Service) state(ctx context.Context, p *model.Poll, mine []int32) wire.PollStateBody {
	tally, _ := s.store.Tally(ctx, p.ID)
	body := wire.PollStateBody{
		PollID: p.ID, ChatID: p.ChatID, MessageID: p.MessageID,
		Question: p.Question, MultiChoice: p.MultiChoice, Anonymous: p.Anonymous,
		Closed: p.Closed, MyVotes: mine,
	}
	total := 0
	for _, n := range tally {
		total += n
	}
	body.TotalVotes = int32(total)
	for i, text := range p.Options {
		body.Options = append(body.Options, wire.PollOption{
			Index: int32(i), Text: text, Votes: int32(tally[int32(i)]),
		})
	}
	return body
}

// broadcast publishes the poll's state for fanout to the chat's members. MyVotes
// is deliberately omitted: the event goes to everyone, and one member's
// selections are not another's business (especially in an anonymous poll).
func (s *Service) broadcast(ctx context.Context, p *model.Poll, mine []int32) {
	body := s.state(ctx, p, mine)
	_ = s.bus.Publish(ctx, eventbus.Event{
		Subject: eventbus.SubjPollState,
		Key:     p.ChatID,
		Data:    wire.Marshal(body),
		Headers: tracing.Inject(ctx),
	})
}

func validate(question string, options []string) error {
	q := strings.TrimSpace(question)
	if q == "" || utf8.RuneCountInString(q) > MaxQuestionLen {
		return ErrBadPoll
	}
	if len(options) < MinOptions || len(options) > MaxOptions {
		return ErrBadPoll
	}
	seen := map[string]bool{}
	for _, o := range options {
		o = strings.TrimSpace(o)
		if o == "" || utf8.RuneCountInString(o) > MaxOptionLen {
			return ErrBadPoll
		}
		if seen[o] { // duplicate options make a tally meaningless
			return ErrBadPoll
		}
		seen[o] = true
	}
	return nil
}
