// Package call is the Call Signaling service: voice/video calls and multi-party
// conferences.
//
// SCOPE — what this server does and does NOT do:
//
//	DOES: own the call ROOM (who is invited, who joined, lifecycle state), notify
//	participants, and RELAY opaque WebRTC signaling payloads (SDP offers/answers
//	and ICE candidates) between participants' devices.
//
//	DOES NOT: carry audio or video. Media flows peer-to-peer (1:1) or through an
//	SFU/TURN deployment (conferences, or peers behind symmetric NAT). Those are
//	separate infrastructure — LiveKit/Janus/mediasoup style — that plug into this
//	signaling. The signaling payloads are opaque to the server: it never parses
//	SDP and never stores it.
//
// The room model is uniform: a 1:1 call is a conference with two participants, so
// group calls need no separate code path.
package call

import (
	"context"
	"errors"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/internal/tracing"
	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/id"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// New builds the call service.
func New(st store.CallStore, chats Chats, bus eventbus.Bus, ids *id.Generator) *Service {
	return &Service{store: st, chats: chats, bus: bus, ids: ids}
}

// Invite starts a call in a chat, or joins the one already in progress (so two
// people pressing "call" at the same moment land in ONE room instead of two
// rival ones). Every other chat member is invited; their devices get a ringing
// notification through the normal fanout path.
func (s *Service) Invite(ctx context.Context, chatID, initiatorID, deviceID string, kind model.CallKind, now int64) (*model.Call, error) {
	ctx, span := tracing.Start(ctx, "call.Invite")
	defer span.End()

	if kind != model.CallAudio && kind != model.CallVideo {
		return nil, ErrBadKind
	}
	member, err := s.chats.IsMember(ctx, chatID, initiatorID)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, ErrForbidden
	}

	// Join an in-progress call rather than starting a second room.
	if existing, err := s.store.ActiveCallForChat(ctx, chatID); err == nil {
		if _, err := s.Accept(ctx, existing.ID, initiatorID, deviceID, now); err != nil {
			return nil, err
		}
		return s.store.GetCall(ctx, existing.ID)
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	c := &model.Call{
		ID: s.ids.NextString(), ChatID: chatID, InitiatorID: initiatorID,
		Kind: kind, State: model.CallRinging, CreatedAt: now,
	}
	if err := s.store.CreateCall(ctx, c); err != nil {
		return nil, err
	}
	// The initiator is joined by definition; everyone else is invited (ringing).
	if err := s.store.UpsertParticipant(ctx, &model.CallParticipant{
		CallID: c.ID, UserID: initiatorID, DeviceID: deviceID,
		State: model.PartJoined, JoinedAt: now,
	}); err != nil {
		return nil, err
	}
	members, err := s.chats.MemberIDs(ctx, chatID)
	if err != nil {
		return nil, err
	}
	for _, uid := range members {
		if uid == initiatorID {
			continue
		}
		if err := s.store.UpsertParticipant(ctx, &model.CallParticipant{
			CallID: c.ID, UserID: uid, State: model.PartInvited,
		}); err != nil {
			return nil, err
		}
	}
	s.broadcast(ctx, c)
	return c, nil
}

// Accept marks a user as joined (from a specific device — the first device to
// accept takes the call) and flips a ringing room to active.
func (s *Service) Accept(ctx context.Context, callID, userID, deviceID string, now int64) (*model.Call, error) {
	c, err := s.store.GetCall(ctx, callID)
	if err != nil {
		return nil, err
	}
	if c.State == model.CallEnded {
		return nil, ErrCallEnded
	}
	if err := s.authorize(ctx, c, userID); err != nil {
		return nil, err
	}
	if err := s.store.UpsertParticipant(ctx, &model.CallParticipant{
		CallID: callID, UserID: userID, DeviceID: deviceID,
		State: model.PartJoined, JoinedAt: now,
	}); err != nil {
		return nil, err
	}
	if c.State == model.CallRinging {
		if err := s.store.SetCallState(ctx, callID, model.CallActive, now); err != nil {
			return nil, err
		}
		c.State = model.CallActive
	}
	s.broadcast(ctx, c)
	return c, nil
}

// Decline records a rejection. If nobody is left who could still join, the room
// ends (a 1:1 call declined by the callee is over).
func (s *Service) Decline(ctx context.Context, callID, userID string, now int64) error {
	c, err := s.store.GetCall(ctx, callID)
	if err != nil {
		return err
	}
	if err := s.authorize(ctx, c, userID); err != nil {
		return err
	}
	if err := s.store.UpsertParticipant(ctx, &model.CallParticipant{
		CallID: callID, UserID: userID, State: model.PartDeclined, LeftAt: now,
	}); err != nil {
		return err
	}
	return s.settle(ctx, c, now)
}

// Hangup removes a participant. The room ends when the last joined participant
// leaves (or when a ringing call's only joiner — the caller — cancels).
func (s *Service) Hangup(ctx context.Context, callID, userID string, now int64) error {
	c, err := s.store.GetCall(ctx, callID)
	if err != nil {
		return err
	}
	if err := s.authorize(ctx, c, userID); err != nil {
		return err
	}
	if err := s.store.UpsertParticipant(ctx, &model.CallParticipant{
		CallID: callID, UserID: userID, State: model.PartLeft, LeftAt: now,
	}); err != nil {
		return err
	}
	return s.settle(ctx, c, now)
}

// settle ends the room when no one can still be talking, then broadcasts.
func (s *Service) settle(ctx context.Context, c *model.Call, now int64) error {
	parts, err := s.store.ListParticipants(ctx, c.ID)
	if err != nil {
		return err
	}
	joined, pending := 0, 0
	for _, p := range parts {
		switch p.State {
		case model.PartJoined:
			joined++
		case model.PartInvited:
			pending++
		}
	}
	// Fewer than two people connected and nobody still ringing → nothing to talk
	// to. (A conference stays alive while ≥2 remain, or while someone may join.)
	if joined < 2 && pending == 0 && c.State != model.CallEnded {
		if err := s.store.SetCallState(ctx, c.ID, model.CallEnded, now); err != nil {
			return err
		}
		c.State = model.CallEnded
		c.EndedAt = now
	}
	s.broadcast(ctx, c)
	return nil
}

// authorize allows a chat member (invited or already in the room) to act on a call.
func (s *Service) authorize(ctx context.Context, c *model.Call, userID string) error {
	member, err := s.chats.IsMember(ctx, c.ChatID, userID)
	if err != nil {
		return err
	}
	if !member {
		return ErrForbidden
	}
	return nil
}

// Participants returns the room roster.
func (s *Service) Participants(ctx context.Context, callID string) ([]*model.CallParticipant, error) {
	return s.store.ListParticipants(ctx, callID)
}

// Get returns a call by id.
func (s *Service) Get(ctx context.Context, callID string) (*model.Call, error) {
	return s.store.GetCall(ctx, callID)
}

// InCall reports whether a user is an active member of the room — the check that
// gates signaling relay, so a non-participant cannot inject SDP/ICE into a call.
func (s *Service) InCall(ctx context.Context, callID, userID string) (bool, error) {
	parts, err := s.store.ListParticipants(ctx, callID)
	if err != nil {
		return false, err
	}
	for _, p := range parts {
		if p.UserID == userID && (p.State == model.PartJoined || p.State == model.PartInvited) {
			return true, nil
		}
	}
	return false, nil
}

// broadcast publishes the room's current state for fanout to every participant.
func (s *Service) broadcast(ctx context.Context, c *model.Call) {
	parts, err := s.store.ListParticipants(ctx, c.ID)
	if err != nil {
		return
	}
	body := wire.CallStateBody{
		CallID: c.ID, ChatID: c.ChatID, InitiatorID: c.InitiatorID,
		Kind: string(c.Kind), State: string(c.State),
	}
	for _, p := range parts {
		body.Participants = append(body.Participants, wire.CallParticipant{
			UserID: p.UserID, DeviceID: p.DeviceID, State: string(p.State),
		})
	}
	_ = s.bus.Publish(ctx, eventbus.Event{
		Subject: eventbus.SubjCallState,
		Key:     c.ChatID,
		Data:    wire.Marshal(body),
		Headers: tracing.Inject(ctx),
	})
}
