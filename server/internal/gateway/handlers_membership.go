// Handlers for who is in a chat and who may act there: creation, public
// handles, invite links, and roles. These take a concrete chat id rather than
// resolving one, because a handle names a CHAT, not a peer.
package gateway

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/synapse-chat/synapse/internal/invite"
	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// --- Chat creation ---

// handleChatCreate makes a group or channel. Direct chats are NOT created here:
// they are resolved from a username on first message, which keeps the "one 1:1
// chat per pair" invariant in one place instead of two.
func (c *conn) handleChatCreate(ctx context.Context, e wire.Envelope) error {
	var body wire.ChatCreateBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad create body")
	}
	typ := model.ChatType(strings.ToLower(strings.TrimSpace(body.Type)))
	if typ == "" {
		typ = model.ChatGroup
	}
	if typ != model.ChatGroup && typ != model.ChatChannel {
		return c.replyError(e.RequestID, wire.ErrBadArg, "type must be group or channel")
	}
	title := strings.TrimSpace(body.Title)
	if title == "" || len(title) > maxChatTitle {
		return c.replyError(e.RequestID, wire.ErrBadArg, "title must be 1..128 characters")
	}
	if len(body.Members) > maxCreateMembers {
		return c.replyError(e.RequestID, wire.ErrBadArg, "too many initial members")
	}
	// Creating a chat is the same kind of act as starting a conversation, so it
	// shares the new-chat budget: a client cannot avoid that limit by making
	// groups instead of direct chats.
	if !c.gw.newChatLimiter.Allow(c.userID) {
		return c.replyErrorRetry(e.RequestID, wire.ErrRateLimited, "too many new chats", 1000)
	}

	ctx, cancel := context.WithTimeout(ctx, maxCreateResolveIn)
	defer cancel()
	members := make([]string, 0, len(body.Members))
	for _, m := range body.Members {
		uid, err := c.resolveUser(ctx, m)
		if err != nil {
			return c.replyError(e.RequestID, wire.ErrNotFound, "no such user: "+m)
		}
		if uid == c.userID {
			continue // the creator is added as owner below
		}
		members = append(members, uid)
	}

	ch, err := c.gw.svc.Chat.CreateGroup(ctx, c.userID, title, typ, members)
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}
	c.gw.audit(ctx, "chat.create", c.userID, ch.ID, string(typ))
	return c.reply(wire.MsgChatInfo, e.RequestID, wire.ChatInfoBody{
		ChatID: ch.ID, Type: string(ch.Type), Title: ch.Title, OwnerID: ch.OwnerID,
	})
}

// resolveUser accepts a "@username" or a bare user id and returns the id.
func (c *conn) resolveUser(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "@") {
		if !validID(ref) {
			return "", errors.New("invalid user id")
		}
		if _, err := c.gw.svc.Users.GetUser(ctx, ref); err != nil {
			return "", err
		}
		return ref, nil
	}
	u, err := c.gw.svc.Users.GetUserByUsername(ctx, strings.ToLower(strings.TrimPrefix(ref, "@")))
	if err != nil {
		return "", err
	}
	return u.ID, nil
}

// --- Public handles, invite links, admin rights ---

// These handlers address a chat DIRECTLY rather than through resolveChat: a
// handle names a chat, not a peer, so there is no "@user" to turn into a chat
// here. That also means the id arrives unvalidated, so each one checks it —
// otherwise a malformed id would travel to a BIGINT column and come back as an
// internal error instead of the bad argument it is.

func (c *conn) handleSetUsername(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Invites == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "invites not enabled")
	}
	var body wire.SetUsernameBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad username body")
	}
	if !validID(body.ChatID) {
		return c.replyError(e.RequestID, wire.ErrBadArg, "invalid chat id")
	}
	if err := c.gw.svc.Invites.SetUsername(ctx, body.ChatID, c.userID, body.Username); err != nil {
		return c.replyInviteErr(e.RequestID, err)
	}
	c.gw.audit(ctx, "chat.username", c.userID, body.ChatID, body.Username)
	return c.reply(wire.MsgInvites, e.RequestID, wire.InvitesBody{})
}

func (c *conn) handleInviteCreate(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Invites == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "invites not enabled")
	}
	var body wire.InviteCreateBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad invite body")
	}
	if !validID(body.ChatID) {
		return c.replyError(e.RequestID, wire.ErrBadArg, "invalid chat id")
	}
	// A link is a credential; minting them is cheap for the client and expensive
	// to account for, so the budget belongs to the user, not the socket.
	if !c.allowUser(ctx, "invite") {
		return c.replyErrorRetry(e.RequestID, wire.ErrRateLimited, "invite rate limited", 2000)
	}
	l, err := c.gw.svc.Invites.CreateLink(ctx, body.ChatID, c.userID, body.ExpiresAt, body.MaxUses, time.Now().UnixMilli())
	if err != nil {
		return c.replyInviteErr(e.RequestID, err)
	}
	c.gw.audit(ctx, "chat.invite.create", c.userID, body.ChatID, l.Code)
	return c.reply(wire.MsgInvites, e.RequestID, wire.InvitesBody{
		Links: []wire.InviteLink{{Code: l.Code, ChatID: l.ChatID, ExpiresAt: l.ExpiresAt, MaxUses: l.MaxUses, Uses: l.Uses}},
	})
}

func (c *conn) handleInviteRevoke(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Invites == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "invites not enabled")
	}
	var body wire.InviteRevokeBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad revoke body")
	}
	if !validID(body.ChatID) {
		return c.replyError(e.RequestID, wire.ErrBadArg, "invalid chat id")
	}
	if err := c.gw.svc.Invites.RevokeLink(ctx, body.ChatID, c.userID, body.Code); err != nil {
		return c.replyInviteErr(e.RequestID, err)
	}
	c.gw.audit(ctx, "chat.invite.revoke", c.userID, body.ChatID, body.Code)
	return c.reply(wire.MsgInvites, e.RequestID, wire.InvitesBody{})
}

func (c *conn) handleInviteList(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Invites == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "invites not enabled")
	}
	var body wire.InviteListBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad list body")
	}
	if !validID(body.ChatID) {
		return c.replyError(e.RequestID, wire.ErrBadArg, "invalid chat id")
	}
	links, err := c.gw.svc.Invites.ListLinks(ctx, body.ChatID, c.userID)
	if err != nil {
		return c.replyInviteErr(e.RequestID, err)
	}
	out := wire.InvitesBody{}
	for _, l := range links {
		out.Links = append(out.Links, wire.InviteLink{
			Code: l.Code, ChatID: l.ChatID, ExpiresAt: l.ExpiresAt, MaxUses: l.MaxUses, Uses: l.Uses,
		})
	}
	return c.reply(wire.MsgInvites, e.RequestID, out)
}

// handleJoin joins by invite code, or by public handle if the chat has one.
func (c *conn) handleJoin(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Invites == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "invites not enabled")
	}
	var body wire.JoinBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad join body")
	}
	var (
		ch  *model.Chat
		err error
	)
	switch {
	case body.Code != "":
		ch, err = c.gw.svc.Invites.Join(ctx, body.Code, c.userID, time.Now().UnixMilli())
	case body.Handle != "":
		ch, err = c.gw.svc.Invites.JoinPublic(ctx, body.Handle, c.userID, time.Now().UnixMilli())
	default:
		return c.replyError(e.RequestID, wire.ErrBadArg, "code or handle required")
	}
	if err != nil {
		return c.replyInviteErr(e.RequestID, err)
	}
	c.gw.audit(ctx, "chat.join", c.userID, ch.ID, "")
	return c.reply(wire.MsgInvites, e.RequestID, wire.InvitesBody{JoinedChat: ch.ID})
}

func (c *conn) handleSetRole(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Invites == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "invites not enabled")
	}
	var body wire.SetRoleBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad role body")
	}
	if !validID(body.ChatID) || !validID(body.UserID) {
		return c.replyError(e.RequestID, wire.ErrBadArg, "invalid chat or user id")
	}
	if err := c.gw.svc.Invites.SetRole(ctx, body.ChatID, c.userID, body.UserID, model.MemberRole(body.Role)); err != nil {
		return c.replyInviteErr(e.RequestID, err)
	}
	c.gw.audit(ctx, "chat.role", c.userID, body.ChatID, body.UserID+"="+body.Role)
	return c.reply(wire.MsgInvites, e.RequestID, wire.InvitesBody{})
}

func (c *conn) replyInviteErr(reqID uint64, err error) error {
	switch {
	case errors.Is(err, invite.ErrForbidden):
		return c.replyError(reqID, wire.ErrForbidden, "forbidden")
	case errors.Is(err, invite.ErrBadUsername):
		return c.replyError(reqID, wire.ErrBadArg, "invalid username")
	case errors.Is(err, invite.ErrTaken):
		return c.replyError(reqID, wire.ErrConflict, "username taken")
	case errors.Is(err, invite.ErrInvalidLink):
		return c.replyError(reqID, wire.ErrNotFound, "invite link is not usable")
	case errors.Is(err, invite.ErrLastOwner):
		return c.replyError(reqID, wire.ErrConflict, "cannot demote the last owner")
	}
	return c.replyForError(reqID, err)
}
