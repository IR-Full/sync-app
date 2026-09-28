package gateway

import (
	"context"
	"errors"

	"github.com/SyncApp-chat/SyncApp/internal/chat"
	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
)

/*
A secret chat as a CHAT, not as a side channel.

End-to-end chats used to exist only as a relay with no chat row behind them. That
had a consequence in every client: with no row there was no entry in the chat
list, no title, no unread count, no mute setting and no history — so each client
put secret chats in a modal window beside the product, which is exactly what the
data model said they were.

Making it a chat type means the ordinary screens work unchanged and only the
GUARANTEES differ. The row holds what a chat needs: membership, ordering, flags,
the peer. It does not hold messages — those stay ciphertext in the secret queue
until the peer device collects them, and nothing in the message log ever refers to
this chat.

What that costs is a set of refusals, because several server features assume they
can read a chat's content. They are collected in secretRefusal below rather than
scattered, so they cannot drift apart: each one is a place where doing the ordinary
thing would silently break the guarantee.
*/

// createSecretChat is the ChatType == secret branch of handleChatCreate.
//
// Two-party and titleless, so it takes exactly one member and ignores the title
// rather than validating one. The canonical row means asking twice returns the
// same chat — which is what makes it safe for a client to call this whenever the
// user taps "secret chat" without first checking whether one exists.
func (c *conn) createSecretChat(ctx context.Context, e wire.Envelope, body wire.ChatCreateBody) error {
	if len(body.Members) != 1 {
		// Not "at most one": a secret chat with nobody has no session to run, and a
		// secret chat with several has no session that all of them share. Both are
		// the same mistake and it is worth naming precisely.
		return c.replyError(e.RequestID, wire.ErrBadArg,
			"a secret chat takes exactly one other member")
	}
	if c.gw.svc.KeyDir == nil {
		// No key directory means no prekey bundles, which means no client could
		// start a session in the chat even if the row existed. Refusing here is
		// honest; creating an unusable chat is not.
		return c.replyError(e.RequestID, wire.ErrUnsupported, "secret chats are not available")
	}
	// The Premium gate.
	//
	// Checked HERE — at creation — and not inside the crypto or the relay. Gating a
	// feature is a product decision, and putting it next to the cipher would make
	// the cipher depend on the billing service; putting it in the relay would let a
	// client keep using a chat it could no longer create, which is a worse kind of
	// inconsistency than a clear refusal.
	//
	// ErrPremiumRequired rather than ErrForbidden: forbidden is final, this has an
	// answer, and a client that shows the wrong one makes a purchasable feature look
	// broken.
	if c.requirePremium(e.RequestID, c.entitlements(ctx).SecretChats, "secret chats") {
		return nil
	}
	if !c.gw.newChatLimiter.Allow(c.userID) {
		return c.replyErrorRetry(e.RequestID, wire.ErrRateLimited, "too many new chats", 1000)
	}

	ctx, cancel := context.WithTimeout(ctx, maxCreateResolveIn)
	defer cancel()
	peer, err := c.resolveUser(ctx, body.Members[0])
	if err != nil {
		return c.replyError(e.RequestID, wire.ErrNotFound, "no such user: "+body.Members[0])
	}
	// Blocking applies. Every other way to reach a person is gated on it, and a
	// secret chat must not be the one door left open — the ciphertext being
	// unreadable to us is not the point, delivery is.
	if c.gw.svc.Contacts != nil {
		blocked, err := c.gw.svc.Contacts.BlocksBetween(ctx, c.userID, peer)
		if err != nil {
			return c.replyForError(e.RequestID, err)
		}
		if blocked {
			return c.replyError(e.RequestID, wire.ErrForbidden, "blocked")
		}
	}

	ch, err := c.gw.svc.Chat.EnsureSecret(ctx, c.userID, peer)
	switch {
	case errors.Is(err, chat.ErrSecretSelfChat):
		return c.replyError(e.RequestID, wire.ErrBadArg,
			"a secret chat needs another party; there is no session with yourself")
	case err != nil:
		return c.replyForError(e.RequestID, err)
	}
	c.gw.audit(ctx, "chat.secret.created", c.userID, ch.ID, "")
	return c.reply(wire.MsgChatInfo, e.RequestID, wire.ChatInfoBody{
		ChatID: ch.ID, Type: string(ch.Type), Title: ch.Title, OwnerID: ch.OwnerID,
	})
}

/*
secretRefusal reports why an operation cannot be performed on a secret chat, or ""
when it can.

Collected in one function because the refusals have to AGREE. Each is a place
where the ordinary behaviour would quietly break the guarantee, and four separate
inline checks written months apart would not stay consistent:

  - SEND through the cloud path would store the plaintext, which is the entire
    thing a secret chat exists to prevent. Secret messages go through
    SECRET_SEND; a SEND naming a secret chat is a client bug and must not be
    accepted as "nearly right".
  - HISTORY has nothing to return. The message log holds no rows for this chat, so
    an empty page would be indistinguishable from a chat that has been quiet — and
    a client would draw "no messages" over a conversation the device has locally.
  - FORWARD out of a secret chat would copy content into a chat where it IS
    stored. The user chose a mode with no server-side copy, and a single forward
    silently undoes that for the message they forwarded.
  - MEMBERSHIP changes have no meaning: a third member has no ratchet session with
    anyone, so they would sit in a chat whose every message is undecryptable to
    them.

Search is handled elsewhere (the indexer never sees these messages, because they
never become message rows), which is the one case the model handles for free.
*/
func secretRefusal(op string, typ model.ChatType) string {
	if !typ.IsSecret() {
		return ""
	}
	switch op {
	case "send":
		return "this is a secret chat: use SECRET_SEND, which the server cannot read"
	case "history":
		return "a secret chat has no server-side history; its messages live only on the devices"
	case "forward":
		return "a message cannot be forwarded out of a secret chat"
	case "membership":
		return "a secret chat has exactly two members"
	case "invite":
		return "a secret chat cannot be joined by link"
	case "pin", "poll", "react":
		// These are chat-wide state the server maintains BY READING messages, and it
		// has no messages here to attach them to.
		return "not available in a secret chat"
	default:
		return "not available in a secret chat"
	}
}

// refuseIfSecret replies with the refusal and reports whether it did.
//
// A helper rather than an inline pattern because the shape — look up the chat,
// check the type, reply, return — is the same at every call site, and the one that
// forgets to return is the one that proceeds anyway.
func (c *conn) refuseIfSecret(ctx context.Context, reqID uint64, chatID, op string) bool {
	if chatID == "" || c.gw.svc.Chat == nil {
		return false
	}
	ch, err := c.gw.svc.Chat.Get(ctx, chatID)
	if err != nil || ch == nil {
		// Not found is somebody else's error to report; this function only answers
		// the secret question, and a chat it cannot read the type of is not one it
		// should refuse on.
		return false
	}
	if msg := secretRefusal(op, ch.Type); msg != "" {
		_ = c.replyError(reqID, wire.ErrForbidden, msg)
		return true
	}
	return false
}
