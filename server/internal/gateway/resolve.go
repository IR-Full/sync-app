// Chat-target resolution and the id validation around it.
//
// Every handler that takes a chat comes through here, which is exactly why it is
// its own file: resolveChat is where "@username" becomes a chat id, and that
// makes it a username-existence oracle for the whole protocol rather than for
// the one handler someone remembered to meter. Keeping it, its rate limit and
// the id validators together is what stops the next handler with a chat target
// from quietly getting an unmetered one.
package gateway

import (
	"context"
	"errors"
	"strings"

	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
)

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
		// The block has to hold here too. Checking it only on the "@username" path
		// below meant a blocked user who already knew the chat id — which every
		// earlier message had given them — kept writing to and reading from the
		// conversation as if nothing had happened.
		if err := c.refuseIfBlocked(ctx, target); err != nil {
			return "", err
		}
		return target, nil
	}
	uname := strings.ToLower(strings.TrimPrefix(target, "@"))
	u, err := c.gw.svc.Users.GetUserByUsername(ctx, uname)
	if errors.Is(err, store.ErrNotFound) {
		// A miss is the enumeration signal, so it is charged — and charged to the
		// USER rather than the socket, because a second connection would otherwise
		// buy a second budget. Reported as "no such user" either way: swapping the
		// message for "slow down" would still answer the attacker's question.
		if !c.allowUser(ctx, "lookup") {
			return "", errLookupThrottled
		}
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
	//
	// This is also where the enumeration budget stops applying. Resolving the
	// handle of someone you already have a conversation with tells you nothing you
	// did not know, and charging for it would throttle ordinary sending: a client
	// may legitimately address every message as "@name", so a budget here would
	// cap the send rate at the lookup rate.
	if ch, err := c.gw.svc.Chat.FindDirect(ctx, c.userID, u.ID); err == nil {
		return ch.ID, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	// Resolving a handle you have no chat with IS discovery — the same act as a
	// PROFILE_GET, which is metered for exactly this reason — so it pays both
	// budgets: the lookup one for learning the account exists, and the new-chat
	// one for the conversation it is about to open.
	if !c.allowUser(ctx, "lookup") {
		return "", errLookupThrottled
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
// validDeviceID bounds a client-asserted device id used as a routing address.
//
// Unlike validID this must NOT require digits: the device id comes from the
// client's HELLO and is whatever that client chose — "web-3f2a", a UUID, an
// installation id. Only its size and character set are the server's business,
// because it ends up in a routing key and in log lines. An empty id is valid and
// means "all of that user's devices".
func validDeviceID(s string) bool {
	if len(s) > maxDeviceIDLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		ok := ch == '-' || ch == '_' || ch == '.' ||
			(ch >= '0' && ch <= '9') ||
			(ch >= 'a' && ch <= 'z') ||
			(ch >= 'A' && ch <= 'Z')
		if !ok {
			return false
		}
	}
	return true
}

// maxDeviceIDLen is generous next to a UUID (36) and still bounds what a client
// can push into a routing key.
const maxDeviceIDLen = 64

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
	if errors.Is(err, errLookupThrottled) {
		return c.replyErrorRetry(reqID, wire.ErrRateLimited, "too many handle lookups", 2000)
	}
	if errors.Is(err, errBlocked) {
		return c.replyError(reqID, wire.ErrForbidden, "blocked")
	}
	return c.replyError(reqID, wire.ErrNotFound, err.Error())
}

// directPeerFinder is the cached peer lookup the in-process chat service offers
// (chat.Service.DirectPeer). The RPC client does not, and falls back to Get plus
// Members — correct, one round trip dearer.
type directPeerFinder interface {
	DirectPeer(ctx context.Context, chatID, userID string) (peer string, ok bool, err error)
}

// refuseIfBlocked returns errBlocked when chatID is a 1:1 chat (direct or
// secret) and either participant has blocked the other. Every other kind of
// chat passes: a block is between two people, not a ban from a group they share.
func (c *conn) refuseIfBlocked(ctx context.Context, chatID string) error {
	if c.gw.svc.Contacts == nil || c.gw.svc.Chat == nil {
		return nil
	}
	peer, ok, err := c.directPeer(ctx, chatID)
	if err != nil || !ok {
		// Not a 1:1 (or not ours): membership is checked where it is enforced.
		// A lookup error is left to that check too rather than reported as a block.
		return nil
	}
	blocked, err := c.gw.svc.Contacts.BlocksBetween(ctx, c.userID, peer)
	if err != nil {
		return err
	}
	if blocked {
		return errBlocked
	}
	return nil
}

func (c *conn) directPeer(ctx context.Context, chatID string) (string, bool, error) {
	if f, ok := c.gw.svc.Chat.(directPeerFinder); ok {
		return f.DirectPeer(ctx, chatID, c.userID)
	}
	ch, err := c.gw.svc.Chat.Get(ctx, chatID)
	if err != nil || !ch.Type.Is1To1() {
		return "", false, err
	}
	members, err := c.gw.svc.Chat.Members(ctx, chatID)
	if err != nil {
		return "", false, err
	}
	var peer string
	mine := false
	for _, m := range members {
		if m.UserID == c.userID {
			mine = true
		} else {
			peer = m.UserID
		}
	}
	return peer, mine && peer != "", nil
}

// replyBlocked answers a refuseIfBlocked failure: Forbidden for the block itself,
// the ordinary error mapping for anything else.
func (c *conn) replyBlocked(reqID uint64, err error) error {
	if errors.Is(err, errBlocked) {
		return c.replyError(reqID, wire.ErrForbidden, "blocked")
	}
	return c.replyForError(reqID, err)
}
