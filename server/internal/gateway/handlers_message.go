// Handlers for the core message path: send, read receipts, typing, edit, delete,
// history, reactions and threads.
//
// This is the hot path of the whole system, and it lived in handlers_media.go
// until the file's name stopped describing any of it. A frame arriving here is
// the most common thing the gateway does; it deserves a file somebody can find.
package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/IR-Full/sync-app/server/internal/message"
	"github.com/IR-Full/sync-app/server/internal/metrics"
	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/reaction"
	"github.com/IR-Full/sync-app/server/internal/store"
	"github.com/IR-Full/sync-app/server/pkg/wire"
)

func (c *conn) handleSend(ctx context.Context, e wire.Envelope) error {
	var body wire.SendBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad send body")
	}
	chatID, err := c.resolveChat(ctx, body.ChatID)
	if err != nil {
		return c.replyResolveErr(e.RequestID, err)
	}
	// A cloud SEND into a secret chat would STORE the plaintext, which is the one
	// thing the chat exists to prevent. Refused rather than tolerated: a client
	// that gets this wrong has a bug, and accepting it as nearly-right is how a
	// secret conversation ends up half in the message log.
	if c.refuseIfSecret(ctx, e.RequestID, chatID, "send") {
		return nil
	}
	t0 := time.Now()
	res, err := c.gw.svc.Broker.Submit(ctx, message.Command{
		Op:         message.OpCreate,
		ActorID:    c.userID,
		ChatID:     chatID,
		DedupKey:   body.DedupKey,
		Text:       body.Text,
		MediaRef:   body.MediaRef,
		ReplyTo:    body.ReplyTo,
		Attachment: message.ModelAttachment(body.Attachment),
		TTLSeconds: body.TTLSeconds,
	})
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}
	metrics.SendAckSeconds.Observe(time.Since(t0).Seconds())
	if !res.Duplicate {
		metrics.MessagesSent.Inc()
	}
	m := res.Message
	return c.reply(wire.MsgSendAck, e.RequestID, wire.SendAckBody{
		DedupKey:  body.DedupKey,
		MessageID: m.ID,
		ChatID:    m.ChatID,
		ChatSeq:   m.Seq,
		Timestamp: m.CreatedAt,
		Duplicate: res.Duplicate,
	})
}

func (c *conn) handleRead(ctx context.Context, e wire.Envelope) error {
	var body wire.ReadBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad read body")
	}
	chatID, err := c.resolveChat(ctx, body.ChatID)
	if err != nil {
		return c.replyResolveErr(e.RequestID, err)
	}
	if err := c.gw.svc.Msg.MarkRead(ctx, c.userID, chatID, body.UpToChatSeq); err != nil {
		return c.replyForError(e.RequestID, err)
	}
	return nil
}

// handleTyping relays a typing indicator. This is the cheapest frame a client
// can send and one of the most expensive to serve — it fans out to every member
// of the chat, on every node holding one — so it is throttled twice: once per
// connection (before the chat is resolved, so a flood costs no lookups) and once
// per chat. Both drops are silent: typing is best-effort by definition, and an
// error reply would cost more than the frame it refuses.
func (c *conn) handleTyping(ctx context.Context, e wire.Envelope) error {
	if !c.typingLimit.Allow() {
		metrics.ThrottleDropped.WithLabelValues("typing").Inc()
		return nil
	}
	var body wire.TypingBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return nil // typing is best-effort; ignore malformed
	}
	chatID, err := c.resolveChat(ctx, body.ChatID)
	if err != nil {
		return nil
	}
	if !c.typingChatLimit.Allow(chatID) {
		metrics.ThrottleDropped.WithLabelValues("typing").Inc()
		return nil
	}
	return c.gw.svc.Presence.Typing(ctx, chatID, c.userID, body.Active)
}

func (c *conn) handleEdit(ctx context.Context, e wire.Envelope) error {
	var body wire.EditBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad edit body")
	}
	if !validID(body.ChatID) || !validID(body.MessageID) {
		return c.replyError(e.RequestID, wire.ErrBadArg, "invalid id")
	}
	// Editing rewrites what the other side is shown, so after a block it is just
	// another way of writing to them.
	if err := c.refuseIfBlocked(ctx, body.ChatID); err != nil {
		return c.replyBlocked(e.RequestID, err)
	}
	_, err := c.gw.svc.Broker.Submit(ctx, message.Command{
		Op: message.OpEdit, ActorID: c.userID, ChatID: body.ChatID, MessageID: body.MessageID, Text: body.Text,
	})
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}
	return nil
}

func (c *conn) handleDelete(ctx context.Context, e wire.Envelope) error {
	var body wire.DeleteBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad delete body")
	}
	if !validID(body.ChatID) || !validID(body.MessageID) {
		return c.replyError(e.RequestID, wire.ErrBadArg, "invalid id")
	}
	_, err := c.gw.svc.Broker.Submit(ctx, message.Command{
		Op: message.OpDelete, ActorID: c.userID, ChatID: body.ChatID, MessageID: body.MessageID,
	})
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}
	return nil
}

// handleHistory streams a page of past messages then a HistoryOK cursor. The
// messages are sent as MsgNew so the client's normal ingest path handles them.
func (c *conn) handleHistory(ctx context.Context, e wire.Envelope) error {
	var body wire.HistoryBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad history body")
	}
	limit := body.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	chatID, err := c.resolveChat(ctx, body.ChatID)
	if err != nil {
		return c.replyResolveErr(e.RequestID, err)
	}
	// A secret chat has no server-side history, and an EMPTY page would be the
	// wrong answer rather than a harmless one: it is indistinguishable from a chat
	// that has simply been quiet, so a client would draw "no messages" over a
	// conversation it holds locally. Saying so explicitly is what lets the client
	// use its own store instead.
	if c.refuseIfSecret(ctx, e.RequestID, chatID, "history") {
		return nil
	}
	msgs, err := c.gw.svc.Msg.History(ctx, c.userID, chatID, body.BeforeSeq, limit)
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}

	var nextBefore uint64
	for _, m := range msgs {
		nextBefore = m.Seq
	}
	// The RESOLVED id, not the string that was sent. A client may address a chat
	// as "@bob"; echoing that back makes the terminator the one frame in the
	// stream that does not name the chat the messages actually came from.
	done := len(msgs) < limit

	// One frame instead of a hundred, for peers that said they can read it.
	//
	// The per-message stream below is not legacy to be cleaned up later: iOS and
	// Android do not advertise CapBatching, so it stays the answer for them. That
	// is what the capability negotiation is for, and it is why this branch can be
	// added without a protocol version bump.
	if c.peerCaps&wire.CapBatching != 0 {
		page := wire.HistoryPageBody{
			Messages:   make([]wire.NewMessageBody, 0, len(msgs)),
			ChatID:     chatID,
			NextBefore: nextBefore,
			Done:       done,
		}
		for _, m := range msgs {
			page.Messages = append(page.Messages, msgToWire(m))
		}
		return c.reply(wire.MsgHistoryPage, e.RequestID, page)
	}

	for _, m := range msgs {
		_ = c.reply(wire.MsgNew, e.RequestID, msgToWire(m))
	}
	return c.reply(wire.MsgHistoryOK, e.RequestID, wire.HistoryOKBody{
		ChatID:     chatID,
		NextBefore: nextBefore,
		Done:       done,
	})
}

// replyForError maps domain errors to protocol error codes.
func (c *conn) replyForError(reqID uint64, err error) error {
	switch {
	case errors.Is(err, message.ErrForbidden):
		return c.replyError(reqID, wire.ErrForbidden, "forbidden")
	case errors.Is(err, store.ErrNotFound):
		return c.replyError(reqID, wire.ErrNotFound, "not found")
	case errors.Is(err, message.ErrTooLong), errors.Is(err, message.ErrEmptyMessage), errors.Is(err, message.ErrBadCommand):
		return c.replyError(reqID, wire.ErrBadArg, err.Error())
	default:
		c.log.Warn("internal error", "err", err)
		return c.replyError(reqID, wire.ErrInternal, "internal error")
	}
}

// handleReact toggles an emoji reaction on a message. The reaction service
// authorizes (chat membership), persists the toggle, and publishes
// message.reaction; fanout delivers MsgReactUpd to every member — including the
// reactor's other devices, so multi-device stays in sync. The reply carries the
// post-change tally so the reacting client renders immediately without waiting
// for the fanout round trip.
func (c *conn) handleReact(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Reactor == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "reactions not enabled")
	}
	var body wire.ReactBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad react body")
	}
	if body.MessageID == "" {
		return c.replyError(e.RequestID, wire.ErrBadArg, "message_id required")
	}
	chatID, err := c.resolveChat(ctx, body.ChatID)
	if err != nil {
		return c.replyResolveErr(e.RequestID, err)
	}
	added, counts, err := c.gw.svc.Reactor.Toggle(ctx, chatID, body.MessageID, c.userID, body.Emoji, time.Now().UnixMilli())
	if err != nil {
		switch {
		case errors.Is(err, reaction.ErrForbidden):
			return c.replyError(e.RequestID, wire.ErrForbidden, "forbidden")
		case errors.Is(err, reaction.ErrBadEmoji):
			return c.replyError(e.RequestID, wire.ErrBadArg, "invalid emoji")
		}
		return c.replyForError(e.RequestID, err)
	}
	return c.reply(wire.MsgReactUpd, e.RequestID, wire.ReactUpdateBody{
		ChatID: chatID, MessageID: body.MessageID, UserID: c.userID,
		Emoji: body.Emoji, Added: added, Counts: counts,
	})
}

// msgToWire converts a stored message to its wire form (history/thread streams
// and export all render the same shape as live fanout delivery).
func msgToWire(m *model.Message) wire.NewMessageBody {
	m = message.Redacted(m)
	return wire.NewMessageBody{
		MessageID:  m.ID,
		ChatID:     m.ChatID,
		SenderID:   m.SenderID,
		ChatSeq:    m.Seq,
		Text:       m.Text,
		MediaRef:   m.MediaRef,
		Attachment: message.WireAttachment(m.Attachment),
		ReplyTo:    m.ReplyTo,
		Forward:    message.WireForward(m.Forward),
		ExpiresAt:  m.ExpiresAt,
		ThreadRoot: m.ThreadRoot,
		ReplyCount: m.ReplyCount,
		Edited:     m.Edited,
		Deleted:    m.Deleted,
		Timestamp:  m.CreatedAt,
	}
}

// handleThread streams the replies under a thread root, oldest first, then a
// ThreadOK cursor. A thread is one indexed read because the root is resolved
// server-side at write time (see message.Send).
func (c *conn) handleThread(ctx context.Context, e wire.Envelope) error {
	var body wire.ThreadBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad thread body")
	}
	if body.RootID == "" {
		return c.replyError(e.RequestID, wire.ErrBadArg, "root_id required")
	}
	limit := body.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	chatID, err := c.resolveChat(ctx, body.ChatID)
	if err != nil {
		return c.replyResolveErr(e.RequestID, err)
	}
	msgs, err := c.gw.svc.Msg.Thread(ctx, c.userID, chatID, body.RootID, body.AfterSeq, limit)
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}
	var nextAfter uint64
	for _, m := range msgs {
		nextAfter = m.Seq
		_ = c.reply(wire.MsgNew, e.RequestID, msgToWire(m))
	}
	return c.reply(wire.MsgThreadOK, e.RequestID, wire.ThreadOKBody{
		ChatID: chatID, RootID: body.RootID, NextAfter: nextAfter, Done: len(msgs) < limit,
	})
}
