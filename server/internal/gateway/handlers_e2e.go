// Handlers for end-to-end encrypted chats: the prekey directory and the
// ciphertext relay. The server stores only public keys and forwards opaque
// bytes — it can never derive a shared secret or read a secret message.
package gateway

import (
	"context"

	"github.com/synapse-chat/synapse/pkg/wire"
)

// --- E2E secret chats: the server relays opaque bytes and stores public keys. ---

func (c *conn) handleKeyPublish(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.KeyDir == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "secret chats disabled")
	}
	var body wire.KeyPublishBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad key publish")
	}
	c.gw.svc.KeyDir.Publish(ctx, c.userID, c.deviceID, body)
	return nil
}

func (c *conn) handleKeyFetch(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.KeyDir == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "secret chats disabled")
	}
	var body wire.KeyFetchBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad key fetch")
	}
	bundle, ok := c.gw.svc.KeyDir.Fetch(ctx, body.UserID, body.DeviceID)
	if !ok {
		return c.replyError(e.RequestID, wire.ErrNotFound, "no keys for device")
	}
	return c.reply(wire.MsgKeyBundle, e.RequestID, bundle)
}

// handleKeyFetchAll returns prekey bundles for every device of a user, so the
// sender can encrypt a secret message to all of them (multi-device sync).
func (c *conn) handleKeyFetchAll(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.KeyDir == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "secret chats disabled")
	}
	var body wire.KeyFetchBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad key fetch")
	}
	bundles := c.gw.svc.KeyDir.FetchAll(ctx, body.UserID)
	return c.reply(wire.MsgKeyBundles, e.RequestID, wire.KeyBundlesBody{UserID: body.UserID, Bundles: bundles})
}

func (c *conn) handleSecretSend(ctx context.Context, e wire.Envelope) error {
	var body wire.SecretMsgBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad secret message")
	}
	// The server cannot read ciphertext; it only stamps the sender and relays to
	// the addressed device on whatever node holds it (cross-node capable).
	body.FromUserID = c.userID
	body.FromDeviceID = c.deviceID
	c.gw.routeToUser(ctx, body.ToUserID, body.ToDeviceID, wire.MsgSecretRecv, wire.Marshal(body))
	return nil
}

// handleChatExport dumps a cloud chat's metadata, members, and messages for the
// chat OWNER (or a platform admin). This is the "as creator, get a chat's data"
// capability. Secret chats have no server-readable content, so only cloud chats
// (direct/group/channel) are exportable here. It is audited.
// allowUser checks a per-USER budget for an expensive action. Costly work is
// charged to the account that asked for it, not to the socket: opening a second
// connection must not buy a second budget, which is exactly what the
// per-connection flood bucket allows.
func (c *conn) allowUser(ctx context.Context, action string) bool {
	if c.gw.userLimits == nil {
		return true
	}
	return c.gw.userLimits.Allow(ctx, action+":"+c.userID)
}

func (c *conn) handleChatExport(ctx context.Context, e wire.Envelope) error {
	if !c.allowUser(ctx, "export") {
		return c.replyErrorRetry(e.RequestID, wire.ErrRateLimited, "export rate limited", 5000)
	}
	var body wire.ChatExportBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad export body")
	}
	if !validID(body.ChatID) {
		return c.replyError(e.RequestID, wire.ErrBadArg, "invalid chat id")
	}
	ch, err := c.gw.svc.Chat.Get(ctx, body.ChatID)
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}
	// Authorization: chat owner, or a platform admin/moderator (RBAC).
	if ch.OwnerID != c.userID && !c.gw.canExportAny(c.userID) {
		c.gw.audit(ctx, "chat.export.denied", c.userID, body.ChatID, "not owner/admin/moderator")
		return c.replyError(e.RequestID, wire.ErrForbidden, "only the chat owner, an admin, or a moderator may export")
	}

	members, err := c.gw.svc.Chat.Members(ctx, body.ChatID)
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}
	// First frame: metadata + members (no messages). Streaming pages keep each
	// frame well under the 16 MiB cap regardless of chat size.
	header := wire.ChatExportResultBody{ChatID: ch.ID, Type: string(ch.Type), Title: ch.Title, OwnerID: ch.OwnerID}
	for _, m := range members {
		header.Members = append(header.Members, wire.ChatMemberInfo{UserID: m.UserID, Role: string(m.Role), JoinedAt: m.JoinedAt})
	}
	_ = c.reply(wire.MsgChatExportResult, e.RequestID, header)

	// Stream messages oldest→newest in bounded pages. We page the store
	// newest-first, reverse each page, and emit it.
	const pageSize = 200
	var before uint64 // 0 = latest
	total := 0
	for {
		page, err := c.gw.svc.Msg.History(ctx, c.userID, body.ChatID, before, pageSize)
		if err != nil {
			return c.replyForError(e.RequestID, err)
		}
		if len(page) == 0 {
			break
		}
		msgs := make([]wire.NewMessageBody, 0, len(page))
		for _, m := range page { // page is newest-first; client sorts by ChatSeq on Done
			msgs = append(msgs, wire.NewMessageBody{
				MessageID: m.ID, ChatID: m.ChatID, SenderID: m.SenderID, ChatSeq: m.Seq,
				Text: m.Text, MediaRef: m.MediaRef, ReplyTo: m.ReplyTo, Edited: m.Edited, Deleted: m.Deleted, Timestamp: m.CreatedAt,
			})
		}
		before = page[len(page)-1].Seq // oldest seq in this page
		total += len(msgs)
		_ = c.reply(wire.MsgChatExportResult, e.RequestID, wire.ChatExportResultBody{ChatID: ch.ID, Messages: msgs})
		if len(page) < pageSize {
			break
		}
	}
	// Final frame marks completion.
	c.gw.audit(ctx, "chat.export", c.userID, body.ChatID, "exported")
	return c.reply(wire.MsgChatExportResult, e.RequestID, wire.ChatExportResultBody{ChatID: ch.ID, Done: true})
}
