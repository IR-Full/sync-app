// Handlers for search and media. Both are expensive per request and charged
// to the user rather than the connection; media bytes never travel over the
// protocol, only signed URLs and short references do.
package gateway

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/synapse-chat/synapse/internal/message"
	"github.com/synapse-chat/synapse/internal/metrics"
	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/reaction"
	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// --- Search: full-text query, permission-filtered to the user's chats. ---

func (c *conn) handleSearch(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Search == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "search disabled")
	}
	if !c.allowUser(ctx, "search") {
		return c.replyErrorRetry(e.RequestID, wire.ErrRateLimited, "search rate limited", 1000)
	}
	var body wire.SearchBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad search")
	}
	limit := body.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	results, err := c.gw.svc.Search.Query(ctx, c.userID, body.Query, limit)
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}
	hits := make([]wire.SearchHit, 0, len(results))
	for _, r := range results {
		hits = append(hits, wire.SearchHit{
			MessageID: r.MessageID, ChatID: r.ChatID, SenderID: r.SenderID, Seq: r.Seq, Text: r.Text,
		})
	}
	return c.reply(wire.MsgSearchResults, e.RequestID,
		wire.SearchResultsBody{Query: body.Query, Hits: hits})
}

// --- Media: issue signed upload/download URLs; bytes go over HTTP. ---

func (c *conn) handleMediaInit(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Media == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "media disabled")
	}
	if !c.allowUser(ctx, "media") {
		return c.replyErrorRetry(e.RequestID, wire.ErrRateLimited, "upload rate limited", 2000)
	}
	var body wire.MediaInitBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad media init")
	}
	t, err := c.gw.svc.Media.InitUpload(c.userID, body.Filename, body.ContentType, body.Size)
	if err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, err.Error())
	}
	return c.reply(wire.MsgMediaTicket, e.RequestID, wire.MediaTicketBody{
		MediaRef: t.MediaRef, UploadURL: t.UploadURL, ExpiresAt: t.ExpiresAt,
	})
}

func (c *conn) handleMediaFetch(_ context.Context, e wire.Envelope) error {
	if c.gw.svc.Media == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "media disabled")
	}
	var body wire.MediaFetchBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad media fetch")
	}
	url, exp, err := c.gw.svc.Media.DownloadURL(c.userID, body.MediaRef)
	if err != nil {
		return c.replyError(e.RequestID, wire.ErrNotFound, err.Error())
	}
	return c.reply(wire.MsgMediaURL, e.RequestID, wire.MediaURLBody{
		MediaRef: body.MediaRef, DownloadURL: url, ExpiresAt: exp,
	})
}

// resolveChat turns a SendBody chat target into a concrete chat id. A target of
// "@username" resolves (or creates) the canonical 1:1 chat with that user, which
// lets a client start a direct conversation without knowing the chat id.
func (c *conn) resolveChat(ctx context.Context, target string) (string, error) {
	if !strings.HasPrefix(target, "@") {
		// A concrete chat id must be a numeric snowflake. Rejecting malformed ids
		// here surfaces bugs loudly instead of silently coercing to 0 at the store.
		if !validID(target) {
			return "", errors.New("invalid chat id")
		}
		return target, nil
	}
	uname := strings.ToLower(strings.TrimPrefix(target, "@"))
	u, err := c.gw.svc.Users.GetUserByUsername(ctx, uname)
	if errors.Is(err, store.ErrNotFound) {
		return "", errors.New("no such user")
	}
	if err != nil {
		return "", err
	}
	// Blocking must actually stop traffic, in BOTH directions: if either side
	// blocked the other, the chat does not resolve — so a blocked sender cannot
	// message, and cannot read the target's replies by reopening the chat either.
	if c.gw.svc.Contacts != nil {
		blocked, err := c.gw.svc.Contacts.BlocksBetween(ctx, c.userID, u.ID)
		if err != nil {
			return "", err
		}
		if blocked {
			return "", errBlocked
		}
	}
	// Messaging an existing direct chat is unrestricted; only CREATING a new one
	// is rate-limited (caps mass-DM spam) — so look up first, create only if absent.
	if ch, err := c.gw.svc.Chat.FindDirect(ctx, c.userID, u.ID); err == nil {
		return ch.ID, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	if !c.gw.newChatLimiter.Allow(c.userID) {
		return "", errNewChatThrottled
	}
	ch, err := c.gw.svc.Chat.EnsureDirect(ctx, c.userID, u.ID)
	if err != nil {
		return "", err
	}
	return ch.ID, nil
}

// validID reports whether s is a non-empty base-10 snowflake id. All server IDs
// are numeric; anything else from a client is malformed input.
func validID(s string) bool {
	if s == "" || len(s) > 20 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// replyResolveErr maps a resolveChat failure to the right protocol error code.
func (c *conn) replyResolveErr(reqID uint64, err error) error {
	if errors.Is(err, errNewChatThrottled) {
		return c.replyErrorRetry(reqID, wire.ErrRateLimited, "too many new chats", 1000)
	}
	if errors.Is(err, errBlocked) {
		return c.replyError(reqID, wire.ErrForbidden, "blocked")
	}
	return c.replyError(reqID, wire.ErrNotFound, err.Error())
}

func (c *conn) handleSend(ctx context.Context, e wire.Envelope) error {
	var body wire.SendBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad send body")
	}
	chatID, err := c.resolveChat(ctx, body.ChatID)
	if err != nil {
		return c.replyResolveErr(e.RequestID, err)
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
	msgs, err := c.gw.svc.Msg.History(ctx, c.userID, chatID, body.BeforeSeq, limit)
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}
	var nextBefore uint64
	for _, m := range msgs {
		nextBefore = m.Seq
		_ = c.reply(wire.MsgNew, e.RequestID, msgToWire(m))
	}
	return c.reply(wire.MsgHistoryOK, e.RequestID, wire.HistoryOKBody{
		ChatID:     body.ChatID,
		NextBefore: nextBefore,
		Done:       len(msgs) < limit,
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
		ChatID: body.ChatID, RootID: body.RootID, NextAfter: nextAfter, Done: len(msgs) < limit,
	})
}
