// Handlers for call signaling and polls. The server owns call SIGNALING only:
// SDP/ICE payloads are relayed between verified participants and never parsed,
// and media never touches it.
package gateway

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/synapse-chat/synapse/internal/call"
	"github.com/synapse-chat/synapse/internal/message"
	"github.com/synapse-chat/synapse/internal/metrics"
	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/poll"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// --- Calls & conferences (signaling only) ---

// handleCallInvite starts a call in the current chat, or joins the one already
// ringing there. Every other member's devices get a ringing push via fanout.
func (c *conn) handleCallInvite(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Calls == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "calls not enabled")
	}
	var body wire.CallInviteBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad call invite")
	}
	chatID, err := c.resolveChat(ctx, body.ChatID)
	if err != nil {
		return c.replyResolveErr(e.RequestID, err)
	}
	kind := model.CallKind(body.Kind)
	if kind == "" {
		kind = model.CallAudio
	}
	call, err := c.gw.svc.Calls.Invite(ctx, chatID, c.userID, c.deviceID, kind, time.Now().UnixMilli())
	if err != nil {
		return c.replyCallErr(e.RequestID, err)
	}
	c.gw.audit(ctx, "call.invite", c.userID, call.ID, string(kind))
	return c.replyCallState(ctx, e.RequestID, call.ID)
}

func (c *conn) handleCallAccept(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Calls == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "calls not enabled")
	}
	var body wire.CallActionBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad call action")
	}
	if _, err := c.gw.svc.Calls.Accept(ctx, body.CallID, c.userID, c.deviceID, time.Now().UnixMilli()); err != nil {
		return c.replyCallErr(e.RequestID, err)
	}
	return c.replyCallState(ctx, e.RequestID, body.CallID)
}

func (c *conn) handleCallDecline(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Calls == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "calls not enabled")
	}
	var body wire.CallActionBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad call action")
	}
	if err := c.gw.svc.Calls.Decline(ctx, body.CallID, c.userID, time.Now().UnixMilli()); err != nil {
		return c.replyCallErr(e.RequestID, err)
	}
	return c.replyCallState(ctx, e.RequestID, body.CallID)
}

func (c *conn) handleCallHangup(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Calls == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "calls not enabled")
	}
	var body wire.CallActionBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad call action")
	}
	if err := c.gw.svc.Calls.Hangup(ctx, body.CallID, c.userID, time.Now().UnixMilli()); err != nil {
		return c.replyCallErr(e.RequestID, err)
	}
	return c.replyCallState(ctx, e.RequestID, body.CallID)
}

// handleCallSignal relays ONE opaque WebRTC payload (SDP offer/answer or ICE
// candidate) to another participant's device. The server authorizes BOTH ends
// against the call roster and never parses or stores the payload — that is what
// keeps media end-to-end while signaling stays server-mediated.
func (c *conn) handleCallSignal(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Calls == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "calls not enabled")
	}
	// Signaling bypasses the send bucket (ICE gathering is bursty and must not
	// compete with messages for it), so it carries its own ceiling — otherwise a
	// participant could use the relay as an unmetered channel to another. Unlike
	// typing, a drop here breaks call setup, so the client is told to back off
	// instead of being ignored.
	if !c.signalLimit.Allow() {
		metrics.ThrottleDropped.WithLabelValues("signal").Inc()
		return c.replyErrorRetry(e.RequestID, wire.ErrFlood, "signaling rate limited", 200)
	}
	var body wire.CallSignalBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad call signal")
	}
	// Sender must be in the call…
	ok, err := c.gw.svc.Calls.InCall(ctx, body.CallID, c.userID)
	if err != nil {
		return c.replyCallErr(e.RequestID, err)
	}
	if !ok {
		return c.replyError(e.RequestID, wire.ErrForbidden, "not a call participant")
	}
	// …and so must the recipient, or signaling becomes an unsolicited-message channel.
	toOK, err := c.gw.svc.Calls.InCall(ctx, body.CallID, body.ToUserID)
	if err != nil {
		return c.replyCallErr(e.RequestID, err)
	}
	if !toOK {
		return c.replyError(e.RequestID, wire.ErrForbidden, "recipient not in call")
	}
	body.FromUserID = c.userID
	body.FromDeviceID = c.deviceID
	c.gw.routeToUser(ctx, body.ToUserID, body.ToDeviceID, wire.MsgCallSignal, wire.Marshal(body))
	return nil
}

// replyCallState echoes the room's current state to the caller (the same body
// fanout pushes to everyone else).
func (c *conn) replyCallState(ctx context.Context, reqID uint64, callID string) error {
	call, err := c.gw.svc.Calls.Get(ctx, callID)
	if err != nil {
		return c.replyCallErr(reqID, err)
	}
	parts, err := c.gw.svc.Calls.Participants(ctx, callID)
	if err != nil {
		return c.replyCallErr(reqID, err)
	}
	body := wire.CallStateBody{
		CallID: call.ID, ChatID: call.ChatID, InitiatorID: call.InitiatorID,
		Kind: string(call.Kind), State: string(call.State),
	}
	for _, p := range parts {
		body.Participants = append(body.Participants, wire.CallParticipant{
			UserID: p.UserID, DeviceID: p.DeviceID, State: string(p.State),
		})
	}
	return c.reply(wire.MsgCallState, reqID, body)
}

func (c *conn) replyCallErr(reqID uint64, err error) error {
	switch {
	case errors.Is(err, call.ErrForbidden):
		return c.replyError(reqID, wire.ErrForbidden, "forbidden")
	case errors.Is(err, call.ErrCallEnded):
		return c.replyError(reqID, wire.ErrConflict, "call ended")
	case errors.Is(err, call.ErrBadKind):
		return c.replyError(reqID, wire.ErrBadArg, "invalid call kind")
	}
	return c.replyForError(reqID, err)
}

// --- Polls ---

// handlePollCreate posts a poll. The QUESTION goes through the normal message
// write path first, so the poll lands in history with correct ordering and
// permissions; the poll entity then attaches to that message id.
func (c *conn) handlePollCreate(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Polls == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "polls not enabled")
	}
	var body wire.PollCreateBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad poll body")
	}
	chatID, err := c.resolveChat(ctx, body.ChatID)
	if err != nil {
		return c.replyResolveErr(e.RequestID, err)
	}
	// Post the question as a message (authorization + ordering happen here).
	res, err := c.gw.svc.Broker.Submit(ctx, message.Command{
		Op: message.OpCreate, ActorID: c.userID, ChatID: chatID,
		DedupKey: "poll:" + strconv.FormatUint(e.RequestID, 10) + ":" + c.sessionID,
		Text:     body.Question,
	})
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}
	p, err := c.gw.svc.Polls.Create(ctx, poll.CreateInput{
		ChatID: chatID, MessageID: res.Message.ID, CreatorID: c.userID,
		Question: body.Question, Options: body.Options,
		MultiChoice: body.MultiChoice, Anonymous: body.Anonymous,
	}, time.Now().UnixMilli())
	if err != nil {
		return c.replyPollErr(e.RequestID, err)
	}
	return c.replyPollState(ctx, e.RequestID, p.ID)
}

func (c *conn) handlePollVote(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Polls == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "polls not enabled")
	}
	var body wire.PollVoteBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad vote body")
	}
	if _, err := c.gw.svc.Polls.Vote(ctx, body.PollID, c.userID, body.Option, time.Now().UnixMilli()); err != nil {
		return c.replyPollErr(e.RequestID, err)
	}
	return c.replyPollState(ctx, e.RequestID, body.PollID)
}

func (c *conn) handlePollClose(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Polls == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "polls not enabled")
	}
	var body wire.PollCloseBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad close body")
	}
	if _, err := c.gw.svc.Polls.Close(ctx, body.PollID, c.userID); err != nil {
		return c.replyPollErr(e.RequestID, err)
	}
	return c.replyPollState(ctx, e.RequestID, body.PollID)
}

// replyPollState answers the caller with the tally INCLUDING their own votes
// (the broadcast to other members omits MyVotes).
func (c *conn) replyPollState(ctx context.Context, reqID uint64, pollID string) error {
	st, err := c.gw.svc.Polls.Results(ctx, pollID, c.userID)
	if err != nil {
		return c.replyPollErr(reqID, err)
	}
	return c.reply(wire.MsgPollState, reqID, st)
}

func (c *conn) replyPollErr(reqID uint64, err error) error {
	switch {
	case errors.Is(err, poll.ErrForbidden):
		return c.replyError(reqID, wire.ErrForbidden, "forbidden")
	case errors.Is(err, poll.ErrClosed):
		return c.replyError(reqID, wire.ErrConflict, "poll closed")
	case errors.Is(err, poll.ErrBadPoll):
		return c.replyError(reqID, wire.ErrBadArg, "invalid question or options")
	case errors.Is(err, poll.ErrBadOption):
		return c.replyError(reqID, wire.ErrBadArg, "option out of range")
	}
	return c.replyForError(reqID, err)
}
