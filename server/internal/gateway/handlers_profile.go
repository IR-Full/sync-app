// Handlers for the two things a client could not learn about itself or its
// world: WHICH chats it is in, and WHO a user is. Both existed in the store
// (ListUserChats, the users table) and neither had a message, so a fresh
// install started empty and a name could only be set once, at registration.
package gateway

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// --- Chat list ---

// handleChatList returns one page of the caller's chats, ordered by chat id.
//
// The page is charged to the USER, not the socket: building it costs a read per
// chat, so opening a second connection must not buy a second budget.
func (c *conn) handleChatList(ctx context.Context, e wire.Envelope) error {
	var body wire.ChatListBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad chat list body")
	}
	if body.After != "" && !validID(body.After) {
		return c.replyError(e.RequestID, wire.ErrBadArg, "invalid cursor")
	}
	if !c.allowUser(ctx, "chatlist") {
		return c.replyErrorRetry(e.RequestID, wire.ErrRateLimited, "chat list rate limited", 2000)
	}
	limit := body.Limit
	if limit <= 0 || limit > maxChatListLimit {
		limit = defaultChatListLimit
	}

	list, err := c.gw.svc.Chat.UserChats(ctx, c.userID, body.After, limit)
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}
	// A page shorter than asked for is the last one — the cursor is the id of the
	// final row, so paging needs nothing the client cannot see.
	out := wire.ChatsBody{Done: len(list) < limit}
	for _, s := range list {
		if s.Chat == nil {
			continue
		}
		out.Chats = append(out.Chats, wire.ChatSummary{
			ChatID: s.Chat.ID, Type: string(s.Chat.Type), Title: s.Chat.Title,
			OwnerID: s.Chat.OwnerID, Username: s.Chat.Username, LastSeq: s.Chat.LastSeq,
			MyRole: string(s.MyRole), PeerID: s.PeerID,
		})
	}
	if n := len(out.Chats); n > 0 && !out.Done {
		out.NextAfter = out.Chats[n-1].ChatID
	}
	return c.reply(wire.MsgChats, e.RequestID, out)
}

// --- Profiles ---

// handleProfileGet reads a user's public profile. Target is a user id or
// "@username", which makes this the handle lookup as well: there is no user
// directory and no prefix search, and an exact handle is the only thing a
// client can be expected to know about a stranger.
func (c *conn) handleProfileGet(ctx context.Context, e wire.Envelope) error {
	var body wire.ProfileGetBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad profile body")
	}
	target := strings.TrimSpace(body.Target)
	if target == "" {
		target = c.userID // no target means "me"
	}
	uid, err := c.resolveUser(ctx, target)
	if err != nil {
		return c.replyError(e.RequestID, wire.ErrNotFound, "no such user")
	}
	// A block cuts both ways here. Someone who blocked you should not be
	// lookup-able by you, and you should not be forced to keep showing your name
	// and picture to an account you blocked.
	if uid != c.userID && c.gw.svc.Contacts != nil {
		if blocked, err := c.gw.svc.Contacts.BlocksBetween(ctx, c.userID, uid); err == nil && blocked {
			return c.replyError(e.RequestID, wire.ErrForbidden, "blocked")
		}
	}
	u, err := c.gw.svc.Users.GetUser(ctx, uid)
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}
	return c.reply(wire.MsgProfile, e.RequestID, profileOf(u))
}

// handleProfileSet updates the CALLER's own profile — there is deliberately no
// way to address someone else's. Empty fields mean "leave as is", so the read
// happens here: the store is told the final values, and clearing the avatar is
// an explicit flag rather than an empty string it would have to interpret.
func (c *conn) handleProfileSet(ctx context.Context, e wire.Envelope) error {
	var body wire.ProfileSetBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad profile body")
	}
	name := strings.TrimSpace(body.DisplayName)
	if name != "" && !validDisplayName(name) {
		return c.replyError(e.RequestID, wire.ErrBadArg, "invalid display name")
	}
	ref := strings.TrimSpace(body.AvatarRef)
	if ref != "" && !validMediaRef(ref) {
		return c.replyError(e.RequestID, wire.ErrBadArg, "invalid avatar ref")
	}

	u, err := c.gw.svc.Users.GetUser(ctx, c.userID)
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}
	if name != "" {
		u.DisplayName = name
	}
	switch {
	case body.ClearAvatar:
		u.AvatarRef = ""
	case ref != "":
		u.AvatarRef = ref
	}
	if err := c.gw.svc.Users.UpdateProfile(ctx, u.ID, u.DisplayName, u.AvatarRef); err != nil {
		return c.replyForError(e.RequestID, err)
	}
	c.gw.audit(ctx, "user.profile", c.userID, u.ID, u.DisplayName)

	// Mirror to the user's OTHER devices: a name changed on the phone must not
	// stay stale on the desktop until it happens to reconnect.
	out := profileOf(u)
	c.gw.routeToUser(ctx, c.userID, "", wire.MsgProfile, wire.Marshal(out))
	return c.reply(wire.MsgProfile, e.RequestID, out)
}

func profileOf(u *model.User) wire.ProfileBody {
	return wire.ProfileBody{
		UserID: u.ID, Username: u.Username, DisplayName: u.DisplayName, AvatarRef: u.AvatarRef,
	}
}

// validDisplayName accepts a printable, single-line label. Control characters
// are rejected rather than stripped: a name that renders differently from what
// was sent (or spans two lines in a chat list) is a spoofing tool, not a typo.
func validDisplayName(s string) bool {
	if len(s) > maxDisplayName || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// validMediaRef checks the SHAPE of a media reference. Whether the blob exists
// is the media service's business — an avatar set before its upload finishes is
// a client bug that fixes itself, not a reason to couple the two paths.
func validMediaRef(s string) bool {
	if len(s) > maxAvatarRef {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		ok := ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_'
		if !ok {
			return false
		}
	}
	return true
}
