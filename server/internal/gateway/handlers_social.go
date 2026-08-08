// Handlers for the per-user layer around a chat: contacts and blocking,
// forwarding and scheduling, pins and drafts. What ties them together is
// visibility — each one is either private to a user, shared across that user's
// devices, or chat-wide, and the routing differs accordingly.
package gateway

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/synapse-chat/synapse/internal/contact"
	"github.com/synapse-chat/synapse/internal/message"
	"github.com/synapse-chat/synapse/internal/pin"
	"github.com/synapse-chat/synapse/internal/schedule"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// --- Contacts & blocking ---

func (c *conn) handleContactAdd(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Contacts == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "contacts not enabled")
	}
	var body wire.ContactAddBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad contact body")
	}
	ct, err := c.gw.svc.Contacts.Add(ctx, c.userID, body.Target, body.Name, time.Now().UnixMilli())
	if err != nil {
		return c.replyContactErr(e.RequestID, err)
	}
	return c.reply(wire.MsgContactList, e.RequestID, wire.ContactListBody{
		Contacts: []wire.Contact{{UserID: ct.UserID, Name: ct.Name, Blocked: ct.Blocked, UpdatedAt: ct.UpdatedAt}},
		Cursor:   ct.UpdatedAt,
	})
}

func (c *conn) handleContactRemove(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Contacts == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "contacts not enabled")
	}
	var body wire.ContactRemoveBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad contact body")
	}
	if err := c.gw.svc.Contacts.Remove(ctx, c.userID, body.Target); err != nil {
		return c.replyContactErr(e.RequestID, err)
	}
	return c.reply(wire.MsgContactList, e.RequestID, wire.ContactListBody{})
}

// handleContactSync returns everything changed since the client's cursor — the
// whole point of the address book being per-owner and timestamped.
func (c *conn) handleContactSync(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Contacts == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "contacts not enabled")
	}
	var body wire.ContactSyncBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad sync body")
	}
	list, cursor, err := c.gw.svc.Contacts.Sync(ctx, c.userID, body.Since)
	if err != nil {
		return c.replyContactErr(e.RequestID, err)
	}
	out := wire.ContactListBody{Cursor: cursor}
	for _, ct := range list {
		out.Contacts = append(out.Contacts, wire.Contact{
			UserID: ct.UserID, Name: ct.Name, Blocked: ct.Blocked, UpdatedAt: ct.UpdatedAt,
		})
	}
	return c.reply(wire.MsgContactList, e.RequestID, out)
}

func (c *conn) handleBlock(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Contacts == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "contacts not enabled")
	}
	var body wire.BlockBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad block body")
	}
	if err := c.gw.svc.Contacts.SetBlocked(ctx, c.userID, body.Target, body.Blocked, time.Now().UnixMilli()); err != nil {
		return c.replyContactErr(e.RequestID, err)
	}
	c.gw.audit(ctx, "contact.block", c.userID, body.Target, strconv.FormatBool(body.Blocked))
	return c.reply(wire.MsgContactList, e.RequestID, wire.ContactListBody{})
}

func (c *conn) replyContactErr(reqID uint64, err error) error {
	switch {
	case errors.Is(err, contact.ErrSelf):
		return c.replyError(reqID, wire.ErrBadArg, "cannot target yourself")
	case errors.Is(err, contact.ErrBadName):
		return c.replyError(reqID, wire.ErrBadArg, "invalid name")
	}
	return c.replyForError(reqID, err)
}

// --- Forwarding & scheduling ---

// handleForward copies a message into another chat. Both ends are authorized:
// the forwarder must be able to READ the source and POST to the destination.
func (c *conn) handleForward(ctx context.Context, e wire.Envelope) error {
	var body wire.ForwardBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad forward body")
	}
	src, err := c.resolveChat(ctx, body.FromChatID)
	if err != nil {
		return c.replyResolveErr(e.RequestID, err)
	}
	dst, err := c.resolveChat(ctx, body.ToChatID)
	if err != nil {
		return c.replyResolveErr(e.RequestID, err)
	}
	m, dup, err := c.gw.svc.Msg.Forward(ctx, c.userID, src, body.MessageID, dst, body.DedupKey)
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}
	return c.reply(wire.MsgSendAck, e.RequestID, wire.SendAckBody{
		DedupKey: body.DedupKey, MessageID: m.ID, ChatID: m.ChatID,
		ChatSeq: m.Seq, Timestamp: m.CreatedAt, Duplicate: dup,
	})
}

func (c *conn) handleSchedule(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Schedule == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "scheduling not enabled")
	}
	var body wire.ScheduleBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad schedule body")
	}
	chatID, err := c.resolveChat(ctx, body.ChatID)
	if err != nil {
		return c.replyResolveErr(e.RequestID, err)
	}
	m, err := c.gw.svc.Schedule.Schedule(ctx, schedule.Input{
		ChatID: chatID, SenderID: c.userID, Text: body.Text, MediaRef: body.MediaRef,
		Attachment: message.ModelAttachment(body.Attachment), ReplyTo: body.ReplyTo,
		TTLSeconds: body.TTLSeconds, SendAt: body.SendAt,
	}, time.Now().UnixMilli())
	if err != nil {
		return c.replyScheduleErr(e.RequestID, err)
	}
	return c.reply(wire.MsgScheduled, e.RequestID, wire.ScheduledBody{
		Items: []wire.ScheduledItem{{ID: m.ID, ChatID: m.ChatID, Text: m.Text, SendAt: m.SendAt}},
	})
}

func (c *conn) handleScheduleList(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Schedule == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "scheduling not enabled")
	}
	var body wire.ScheduleListBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad list body")
	}
	chatID, err := c.resolveChat(ctx, body.ChatID)
	if err != nil {
		return c.replyResolveErr(e.RequestID, err)
	}
	list, err := c.gw.svc.Schedule.List(ctx, c.userID, chatID)
	if err != nil {
		return c.replyScheduleErr(e.RequestID, err)
	}
	out := wire.ScheduledBody{}
	for _, m := range list {
		out.Items = append(out.Items, wire.ScheduledItem{ID: m.ID, ChatID: m.ChatID, Text: m.Text, SendAt: m.SendAt})
	}
	return c.reply(wire.MsgScheduled, e.RequestID, out)
}

func (c *conn) handleScheduleCancel(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Schedule == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "scheduling not enabled")
	}
	var body wire.ScheduleCancelBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad cancel body")
	}
	if err := c.gw.svc.Schedule.Cancel(ctx, body.ID, c.userID); err != nil {
		return c.replyScheduleErr(e.RequestID, err)
	}
	return c.reply(wire.MsgScheduled, e.RequestID, wire.ScheduledBody{})
}

func (c *conn) replyScheduleErr(reqID uint64, err error) error {
	switch {
	case errors.Is(err, schedule.ErrForbidden):
		return c.replyError(reqID, wire.ErrForbidden, "forbidden")
	case errors.Is(err, schedule.ErrPastTime):
		return c.replyError(reqID, wire.ErrBadArg, "send time must be in the future")
	case errors.Is(err, schedule.ErrTooFar):
		return c.replyError(reqID, wire.ErrBadArg, "send time too far ahead")
	}
	return c.replyForError(reqID, err)
}

// --- Pins & drafts ---

func (c *conn) handlePin(ctx context.Context, e wire.Envelope, unpin bool) error {
	if c.gw.svc.Pins == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "pins not enabled")
	}
	var body wire.PinBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad pin body")
	}
	chatID, err := c.resolveChat(ctx, body.ChatID)
	if err != nil {
		return c.replyResolveErr(e.RequestID, err)
	}
	if unpin {
		err = c.gw.svc.Pins.Unpin(ctx, chatID, body.MessageID, c.userID)
	} else {
		err = c.gw.svc.Pins.Pin(ctx, chatID, body.MessageID, c.userID, time.Now().UnixMilli())
	}
	if err != nil {
		return c.replyPinErr(e.RequestID, err)
	}
	return c.replyPins(ctx, e.RequestID, chatID)
}

func (c *conn) handlePinList(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Pins == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "pins not enabled")
	}
	var body wire.PinBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad pin body")
	}
	chatID, err := c.resolveChat(ctx, body.ChatID)
	if err != nil {
		return c.replyResolveErr(e.RequestID, err)
	}
	return c.replyPins(ctx, e.RequestID, chatID)
}

func (c *conn) replyPins(ctx context.Context, reqID uint64, chatID string) error {
	pins, err := c.gw.svc.Pins.ListPins(ctx, chatID, c.userID)
	if err != nil {
		return c.replyPinErr(reqID, err)
	}
	out := wire.PinnedBody{ChatID: chatID}
	for _, p := range pins {
		out.Pins = append(out.Pins, wire.Pin{MessageID: p.MessageID, PinnedBy: p.PinnedBy, PinnedAt: p.PinnedAt})
	}
	return c.reply(wire.MsgPinned, reqID, out)
}

// handleDraftSet saves a draft and mirrors it to the user's OTHER devices — a
// draft is private, so it is routed per-user, never to the chat.
func (c *conn) handleDraftSet(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Pins == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "drafts not enabled")
	}
	var body wire.DraftBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad draft body")
	}
	chatID, err := c.resolveChat(ctx, body.ChatID)
	if err != nil {
		return c.replyResolveErr(e.RequestID, err)
	}
	now := time.Now().UnixMilli()
	if err := c.gw.svc.Pins.SetDraft(ctx, c.userID, chatID, body.Text, body.ReplyTo, now); err != nil {
		return c.replyPinErr(e.RequestID, err)
	}
	// Mirror to this user's other devices so composing continues elsewhere.
	c.gw.routeToUser(ctx, c.userID, "", wire.MsgDrafts, wire.Marshal(wire.DraftsBody{
		Drafts: []wire.DraftItem{{ChatID: chatID, Text: body.Text, ReplyTo: body.ReplyTo, UpdatedAt: now}},
		Cursor: now,
	}))
	return nil
}

func (c *conn) handleDraftSync(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Pins == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "drafts not enabled")
	}
	var body wire.DraftSyncBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad sync body")
	}
	list, cursor, err := c.gw.svc.Pins.SyncDrafts(ctx, c.userID, body.Since)
	if err != nil {
		return c.replyPinErr(e.RequestID, err)
	}
	out := wire.DraftsBody{Cursor: cursor}
	for _, d := range list {
		out.Drafts = append(out.Drafts, wire.DraftItem{ChatID: d.ChatID, Text: d.Text, ReplyTo: d.ReplyTo, UpdatedAt: d.UpdatedAt})
	}
	return c.reply(wire.MsgDrafts, e.RequestID, out)
}

func (c *conn) replyPinErr(reqID uint64, err error) error {
	switch {
	case errors.Is(err, pin.ErrForbidden):
		return c.replyError(reqID, wire.ErrForbidden, "forbidden")
	case errors.Is(err, pin.ErrTooLong):
		return c.replyError(reqID, wire.ErrBadArg, "draft too long")
	}
	return c.replyForError(reqID, err)
}

// handlePushToken registers this device's push token. It is scoped to the
// authenticated user and THIS connection's device: a client may only ever speak
// for the device it is connected as.
func (c *conn) handlePushToken(ctx context.Context, e wire.Envelope) error {
	var body wire.PushTokenBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad push token body")
	}
	if len(body.Token) > 512 {
		return c.replyError(e.RequestID, wire.ErrBadArg, "token too long")
	}
	if err := c.gw.svc.Users.SetPushToken(ctx, c.userID, c.deviceID, body.Token); err != nil {
		return c.replyForError(e.RequestID, err)
	}
	return c.reply(wire.MsgPushToken, e.RequestID, wire.PushTokenBody{})
}
