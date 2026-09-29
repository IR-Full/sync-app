// Handlers for end-to-end encrypted chats: the prekey directory and the
// ciphertext relay. The server stores only public keys and forwards opaque
// bytes — it can never derive a shared secret or read a secret message.
package gateway

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/IR-Full/sync-app/server/internal/keydir"
	"github.com/IR-Full/sync-app/server/pkg/e2e"
	"github.com/IR-Full/sync-app/server/pkg/wire"
)

// x25519KeyLen is the byte length of an X25519 public key. There is no exported
// constant for it in crypto/ecdh, and ed25519's own sizes are named, so only
// this one is spelled out.
const x25519KeyLen = 32

// decodeKey parses one base64 field of a prekey bundle and asserts its length.
//
// The encoding is standard base64 WITH padding (`base64.StdEncoding`) — the same
// as every client and the interop test use. Rejecting here rather than at the
// store is what keeps the directory holding only keys that can be used: a
// wrongly-sized "key" is not a key, and discovering that during someone else's
// handshake turns one client's bug into their peer's broken chat.
func decodeKey(field, value string, want int) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%s is not valid base64", field)
	}
	if len(b) != want {
		return nil, fmt.Errorf("%s must be %d bytes, got %d", field, want, len(b))
	}
	return b, nil
}

// validateKeyBundle checks a KEY_PUBLISH body before it reaches the directory.
//
// Two separate jobs, and both matter:
//
//  1. **Shape.** Every field is bounded to its exact key length. Without this the
//     only ceiling on a published bundle was the 16 MiB frame cap times the
//     prekey count, in a directory that has no expiry — so one device could park
//     megabytes of junk in Redis and keep it there.
//
//  2. **Self-consistency.** The signature is verified against the signing key the
//     same bundle advertises. This is NOT the client's MITM defense — the server
//     could always lie about what it stores, which is exactly why X3DHInitiator
//     verifies again and unconditionally. What it buys is that the directory
//     never serves a bundle that every honest peer will reject: a client that
//     signs wrongly learns at publish time instead of having its chats silently
//     fail to start.
func validateKeyBundle(b wire.KeyPublishBody) error {
	if _, err := decodeKey("identity_key", b.IdentityKey, x25519KeyLen); err != nil {
		return err
	}
	signingKey, err := decodeKey("signing_key", b.SigningKey, ed25519.PublicKeySize)
	if err != nil {
		return err
	}
	signedPreKey, err := decodeKey("signed_prekey", b.SignedPreKey, x25519KeyLen)
	if err != nil {
		return err
	}
	sig, err := decodeKey("signed_prekey_sig", b.SignedPreKeySig, ed25519.SignatureSize)
	if err != nil {
		return err
	}
	if !e2e.VerifyPreKey(signingKey, signedPreKey, sig) {
		return errors.New("signed_prekey_sig does not verify against signing_key")
	}
	for i, pk := range b.PreKeys {
		if _, err := decodeKey(fmt.Sprintf("prekeys[%d]", i), pk, x25519KeyLen); err != nil {
			return err
		}
	}
	return nil
}

// --- E2E secret chats: the server relays opaque bytes and stores public keys. ---

// keyFetchAction is the per-USER budget key for reading the directory.
//
// KEY_FETCH was charged only to the connection, and a one-time prekey is CONSUMED
// by the fetch: a caller who opens several sockets gets several per-connection
// buckets and can pop a target's whole batch of 256 in the time it takes to make
// them. Draining it does not reveal anything, but it forces every later session
// with that device down to the weaker three-DH handshake until the owner next
// publishes - an availability attack on somebody else's forward secrecy, from an
// account that need only be unblocked.
const keyFetchAction = "keyfetch"

// keyFetchRetryAfterMs matches the other directory-adjacent limits: long enough to
// make draining slow, short enough that a legitimate multi-device send (one fetch
// per peer device) is not visibly delayed.
const keyFetchRetryAfterMs = 2000

func (c *conn) handleKeyPublish(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.KeyDir == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "secret chats disabled")
	}
	var body wire.KeyPublishBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad key publish")
	}
	if err := validateKeyBundle(body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad prekey bundle: "+err.Error())
	}
	st := c.gw.svc.KeyDir.Publish(ctx, c.userID, c.deviceID, body)
	// Answer with what the directory now holds. Only the owner can refill one-time
	// prekeys and only PEERS consume them, so this reply is the single place a device
	// can learn its own balance; without it a popular device runs its batch down and
	// silently falls back to the weaker three-DH handshake, with nobody informed.
	//
	// Sent even when RequestID is 0. A client that still publishes fire-and-forget
	// drops it as an unsolicited frame, which is the same as today's behaviour, while
	// one that switched to a request gets its answer; gating the reply on a non-zero
	// id instead would leave no way to tell "server too old" from "reply never came".
	return c.reply(wire.MsgKeyState, e.RequestID, keyStateBody(st))
}

// keyStateBody turns a directory state into the wire reply.
//
// The age is computed HERE rather than carried as a timestamp, because the client
// has no trustworthy clock relationship with the server: it needs "how old is the
// key you hold", and a wall-clock instant makes it subtract two clocks that may be
// minutes apart. A zero FirstSeen means the directory does not know (a failed
// publish, or a remote backend that could not answer) and stays 0 — which is also
// what "introduced by this very publish" reports, and both mean the same thing to a
// client deciding whether to rotate: nothing to rotate yet.
func keyStateBody(st keydir.State) wire.KeyStateBody {
	var ageMs int64
	if !st.SignedPreKeyFirstSeen.IsZero() {
		if age := time.Since(st.SignedPreKeyFirstSeen); age > 0 {
			ageMs = age.Milliseconds()
		}
	}
	return wire.KeyStateBody{
		OneTimePreKeysLeft: st.OneTimePreKeysLeft,
		SignedPreKeyAgeMs:  ageMs,
		Accepted:           st.Accepted,
	}
}

// mayReadKeysOf reports whether this connection may look up another account's
// devices in the directory.
//
// Blocking has to reach here. Every other way to address a person is gated —
// resolveChat refuses when either side blocked the other, PROFILE_GET refuses,
// SECRET_SEND refuses — and the key directory was the one door left open: a
// blocked user could still enumerate their target's devices and pull a fresh
// prekey bundle for each. The keys are public, so this is not a confidentiality
// break; it is the blocked party still being able to watch the other's devices
// come and go, which is exactly what a block is supposed to stop.
//
// A user's own devices are always readable — multi-device sync depends on
// fetching them.
func (c *conn) mayReadKeysOf(ctx context.Context, userID string) (bool, error) {
	if userID == c.userID || c.gw.svc.Contacts == nil {
		return true, nil
	}
	blocked, err := c.gw.svc.Contacts.BlocksBetween(ctx, c.userID, userID)
	if err != nil {
		return false, err
	}
	return !blocked, nil
}

// errKeysHidden means the directory will not answer for this target. It is
// deliberately not a distinct wire error: each handler reports it as its own
// version of "there is nothing here", because a specific "you are blocked" would
// confirm both that the account exists and that it blocked you — turning the
// directory into the oracle the block exists to close.
var errKeysHidden = errors.New("gateway: key lookup not permitted")

// checkKeyTarget validates the requested user id and applies the block check.
func (c *conn) checkKeyTarget(ctx context.Context, userID string) error {
	if !validID(userID) {
		return fmt.Errorf("%w: invalid user id", errBadKeyTarget)
	}
	allowed, err := c.mayReadKeysOf(ctx, userID)
	if err != nil {
		return err
	}
	if !allowed {
		return errKeysHidden
	}
	return nil
}

// errBadKeyTarget is a malformed id — the client's mistake, reported as one.
var errBadKeyTarget = errors.New("gateway: bad key target")

func (c *conn) handleKeyFetch(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.KeyDir == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "secret chats disabled")
	}
	if !c.allowUser(ctx, keyFetchAction) {
		return c.replyErrorRetry(e.RequestID, wire.ErrRateLimited, "key fetch rate limited", keyFetchRetryAfterMs)
	}
	var body wire.KeyFetchBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad key fetch")
	}
	if !validDeviceID(body.DeviceID) {
		return c.replyError(e.RequestID, wire.ErrBadArg, "invalid device id")
	}
	switch err := c.checkKeyTarget(ctx, body.UserID); {
	case errors.Is(err, errBadKeyTarget):
		return c.replyError(e.RequestID, wire.ErrBadArg, "invalid user id")
	case errors.Is(err, errKeysHidden):
		// Same answer as a device that never published: "no keys for device".
		return c.replyError(e.RequestID, wire.ErrNotFound, "no keys for device")
	case err != nil:
		return c.replyForError(e.RequestID, err)
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
	// Same budget as the single fetch, deliberately shared: FETCH_ALL is the cheaper
	// way to drain a target (one frame per device instead of one per key), so giving
	// it a bucket of its own would hand the drainer twice the allowance.
	if !c.allowUser(ctx, keyFetchAction) {
		return c.replyErrorRetry(e.RequestID, wire.ErrRateLimited, "key fetch rate limited", keyFetchRetryAfterMs)
	}
	var body wire.KeyFetchBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad key fetch")
	}
	// This is the enumeration primitive of the pair — it hands back every device
	// id an account has — so it gets the same gate. Its version of "nothing here"
	// is an EMPTY LIST, which is exactly what an account with no published
	// devices returns; an error would single the blocked caller out.
	var bundles []wire.KeyBundleBody
	switch err := c.checkKeyTarget(ctx, body.UserID); {
	case errors.Is(err, errBadKeyTarget):
		return c.replyError(e.RequestID, wire.ErrBadArg, "invalid user id")
	case errors.Is(err, errKeysHidden):
		return c.reply(wire.MsgKeyBundles, e.RequestID, wire.KeyBundlesBody{UserID: body.UserID})
	case err != nil:
		return c.replyForError(e.RequestID, err)
	}
	bundles = c.gw.svc.KeyDir.FetchAll(ctx, body.UserID)
	return c.reply(wire.MsgKeyBundles, e.RequestID, wire.KeyBundlesBody{UserID: body.UserID, Bundles: bundles})
}

func (c *conn) handleSecretSend(ctx context.Context, e wire.Envelope) error {
	var body wire.SecretMsgBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad secret message")
	}
	// The relay is addressed by user id rather than by chat, so it is the one send
	// path with no chat membership behind it. Validate the address like any other
	// boundary id: an unchecked one reaches the router and the node registry as
	// whatever the client typed.
	if !validID(body.ToUserID) {
		return c.replyError(e.RequestID, wire.ErrBadArg, "invalid recipient")
	}
	// A device id is NOT a snowflake: it is asserted by the client in HELLO and is
	// routinely a string like "web-3f2a" or a UUID, so it is bounded rather than
	// parsed. An empty one means "every device of that user".
	if !validDeviceID(body.ToDeviceID) {
		return c.replyError(e.RequestID, wire.ErrBadArg, "invalid recipient device")
	}
	// And apply the block. Every other way to reach a person goes through
	// resolveChat, which refuses when either side blocked the other; this one does
	// not, so without this check blocking someone still left them a channel that
	// arrives as an ordinary secret message. The ciphertext stays unreadable to us
	// either way — that is not the point. Delivery is.
	if body.ToUserID != c.userID && c.gw.svc.Contacts != nil {
		blocked, err := c.gw.svc.Contacts.BlocksBetween(ctx, c.userID, body.ToUserID)
		if err != nil {
			return c.replyForError(e.RequestID, err)
		}
		if blocked {
			return c.replyError(e.RequestID, wire.ErrForbidden, "blocked")
		}
	}
	// The server cannot read ciphertext; it only stamps the sender and relays to
	// the addressed device on whatever node holds it (cross-node capable).
	body.FromUserID = c.userID
	body.FromDeviceID = c.deviceID
	// QueueID is a server-assigned replay marker. A client that sets one is
	// either confused or trying to make a live message look like a redelivery,
	// and either way the field is not theirs to fill.
	body.QueueID = ""

	// Normalise the payload to bytes once, here, whatever shape it arrived in.
	//
	// The sender's encoding is the sender's business and has nothing to do with
	// what the recipient can read — they are different clients, possibly different
	// versions. Decoding at the boundary means the relay, the queue and the
	// delivery all handle one representation, and the encoding choice happens
	// exactly once, at the point where the recipient is known.
	header, cipher, ok := wire.SecretPayload(body)
	if !ok {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad ratchet payload")
	}

	// Ask whether the addressed DEVICE is reachable, not whether its owner is
	// online somewhere. A secret message is encrypted to one device's ratchet
	// session, so "their phone is connected" says nothing about the laptop the
	// ciphertext was addressed to.
	//
	// Between nodes the payload travels in ONE canonical form: raw bytes. The
	// receiving node re-encodes it for the destination socket, which is the only
	// place that knows what that socket negotiated.
	//
	// Normalising here rather than forwarding the sender's shape is what keeps the
	// two client generations independent. Forwarding it would mean a new client
	// sending binary to an old one that reads nothing from those fields — a silent
	// delivery of an empty message.
	relayed := body
	wire.SetSecretPayloadBinary(&relayed, header, cipher)
	delivered := c.gw.routeToDevice(ctx, body.ToUserID, body.ToDeviceID, wire.MsgSecretRecv, wire.Marshal(relayed))

	queued := false
	if delivered == 0 {
		// Nobody was holding the socket. Hold the ciphertext instead of dropping
		// it, and wake the device without saying anything about what arrived —
		// there is no preview to send even if we wanted one.
		queued = c.gw.queueSecret(ctx, body, header, cipher)
		if queued {
			c.gw.pushSecretWake(ctx, body.ToUserID)
		}
	}
	// Answer, but only to a peer that asked for the answer (CapSecretQueue): an
	// older client has no handler for a SECRET_ACK and would be handed a frame it
	// does not expect.
	if c.peerCaps&wire.CapSecretQueue == 0 {
		return nil
	}
	return c.reply(wire.MsgSecretAck, e.RequestID, wire.SecretAckBody{
		ToUserID:   body.ToUserID,
		ToDeviceID: body.ToDeviceID,
		Devices:    int32(delivered),
		Queued:     queued,
	})
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
	// Export is a bulk read of the conversation, which a block takes away just as
	// it takes away HISTORY (through resolveChat).
	if err := c.refuseIfBlocked(ctx, body.ChatID); err != nil {
		return c.replyBlocked(e.RequestID, err)
	}
	ch, err := c.gw.svc.Chat.Get(ctx, body.ChatID)
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}
	// Authorization: chat owner, or a platform admin/moderator (RBAC).
	if ch.OwnerID != c.userID && !c.gw.canExportAny(ctx, c.userID) {
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
