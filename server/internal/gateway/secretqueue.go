package gateway

import (
	"context"
	"encoding/json"
	"time"

	"github.com/IR-Full/sync-app/server/internal/metrics"
	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/pkg/eventbus"
	"github.com/IR-Full/sync-app/server/pkg/wire"
)

/*
The durable half of secret chats.

A pure relay delivers nothing when the recipient's device is offline: the
ciphertext goes nowhere, no push is queued, and the sender cannot tell. A secret
chat between two people who are not online at the same moment is the mode the
whole X3DH/ratchet/pinning stack exists to serve, so the relay is backed by a
queue.

Three things work together, and none of them works alone:

  - The relay has to know whether the addressed DEVICE is reachable, not whether
    its owner is online somewhere. That is router.NodesForDevice; before it,
    "delivered" meant a phone was connected while the ciphertext was addressed
    to a laptop.
  - Undelivered ciphertext has to be held. That is store.SecretQueueStore.
  - The sender has to be told which of the two happened. That is SECRET_ACK.

The queue is at-least-once and the receiver acknowledges explicitly (SECRET_SYNC
→ SECRET_ACKED). Deleting on send would lose the message when the socket dies
between the write and the client persisting it — the same failure this queue
exists to fix, moved one step later.
*/

// secretQueueTTL is how long undelivered ciphertext is held.
//
// The value is a privacy decision before it is a storage one. Every queued row
// is a record of who messaged whom and when — metadata the stateless relay never
// wrote down — so the question is not "how long might this still be wanted" but
// "how long is it worth keeping a conversation graph to answer that". Two weeks
// covers a lost phone and a holiday; past it, the peer's ratchet session has
// usually moved on anyway and the plaintext would not appear even if the bytes
// did.
const secretQueueTTL = 14 * 24 * time.Hour

// maxQueuedPerDevice caps one device's backlog.
//
// A device id is asserted by the client, so anyone who can name a (user, device)
// pair can address envelopes at it. Without a cap that is a way to fill the
// database on somebody else's behalf, and the per-connection flood bucket does
// not help: it limits one socket, while the cost here is paid per row and
// persists after the socket is gone.
const maxQueuedPerDevice = 1000

// maxSecretSyncPage bounds one SECRET_SYNC reply. A device that has been away
// for a fortnight pages through its backlog rather than receiving all of it in
// one burst that its outbound lane cannot absorb.
const maxSecretSyncPage = 100

// queueSecret stores one envelope for a device that was not reachable.
//
// Returns whether it was actually stored: a deployment with no SecretQ
// configured keeps the old relay-only behaviour, and the sender is told that
// rather than left to assume otherwise.
func (g *Gateway) queueSecret(ctx context.Context, body wire.SecretMsgBody, header, cipher []byte) bool {
	if g.svc.SecretQ == nil || g.svc.IDs == nil {
		return false
	}
	now := time.Now().UnixMilli()
	// Stored as BYTES, decoded once at the boundary by the caller.
	//
	// The queue deliberately does not keep the sender's encoding. A stored base64
	// string would mean the column is 33% larger than it needs to be and, worse,
	// that the row remembers which client version wrote it — so delivering to a
	// peer with different capabilities would need a conversion nobody would
	// remember to make. One representation at rest, encoded per connection on the
	// way out.
	env := &model.SecretEnvelope{
		ID:           g.svc.IDs.NextString(),
		ToUserID:     body.ToUserID,
		ToDeviceID:   body.ToDeviceID,
		FromUserID:   body.FromUserID,
		FromDeviceID: body.FromDeviceID,
		Header:       header,
		Ciphertext:   cipher,
		CreatedAt:    now,
		ExpiresAt:    now + secretQueueTTL.Milliseconds(),
	}
	if err := g.svc.SecretQ.EnqueueSecret(ctx, env, maxQueuedPerDevice); err != nil {
		g.log.Warn("secret queue write failed", "user", logUser(body.ToUserID), "err", err)
		return false
	}
	metrics.SecretQueued.Inc()
	return true
}

// handleSecretSync hands a reconnecting device the ciphertext it missed.
//
// Envelopes are NOT deleted here. The device confirms with SECRET_ACKED once it
// has them stored, and until then a dropped connection costs a redelivery rather
// than a message. Ratchet decryption is idempotent for a key that was already
// consumed only in the sense that it fails — so the duplicate is the receiver's
// to discard by queue id, which is exactly what it has.
func (c *conn) handleSecretSync(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.SecretQ == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "secret queue disabled")
	}
	var body wire.SecretSyncBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad secret sync")
	}
	if body.After != "" && !validID(body.After) {
		return c.replyError(e.RequestID, wire.ErrBadArg, "invalid cursor")
	}
	limit := int(body.Limit)
	if limit <= 0 || limit > maxSecretSyncPage {
		limit = maxSecretSyncPage
	}
	// Scoped to the CALLER's own (user, device). The queue is addressed by the
	// pair, and the pair here comes from the authenticated session rather than
	// from the request — there is no field a client could use to read another
	// device's backlog, which is the only property that matters.
	envs, err := c.gw.svc.SecretQ.PendingSecrets(ctx, c.userID, c.deviceID, body.After, limit)
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}
	for _, env := range envs {
		out := wire.SecretMsgBody{
			ToUserID:     env.ToUserID,
			ToDeviceID:   env.ToDeviceID,
			FromUserID:   env.FromUserID,
			FromDeviceID: env.FromDeviceID,
			QueueID:      env.ID,
		}
		// Encoded for THIS connection. The row holds bytes; whether they go out raw
		// or base64-wrapped depends on what this peer negotiated, and a sync is the
		// one place where the stored form and the wire form can differ — a device
		// that was offline for a fortnight may well have been upgraded since.
		wire.SetSecretPayloadFor(&out, c.peerCaps, env.Header, env.Ciphertext)
		_ = c.reply(wire.MsgSecretRecv, e.RequestID, out)
	}
	out := wire.SecretSyncedBody{Count: int32(len(envs)), Done: len(envs) < limit}
	if len(envs) > 0 {
		out.NextAfter = envs[len(envs)-1].ID
	}
	return c.reply(wire.MsgSecretSynced, e.RequestID, out)
}

// handleSecretAcked drops envelopes the device says it has stored.
func (c *conn) handleSecretAcked(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.SecretQ == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "secret queue disabled")
	}
	var body wire.SecretAckedBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad secret ack")
	}
	if len(body.IDs) > maxSecretSyncPage {
		return c.replyError(e.RequestID, wire.ErrBadArg, "too many ids")
	}
	for _, id := range body.IDs {
		if !validID(id) {
			return c.replyError(e.RequestID, wire.ErrBadArg, "invalid id")
		}
	}
	// The store scopes the delete by (user, device) as well. Both checks exist on
	// purpose: an envelope id travels to the client, so the only thing standing
	// between "I saw an id" and "I deleted someone else's mail" is that the
	// predicate names the caller — and that belongs in the query, not only here.
	n, err := c.gw.svc.SecretQ.AckSecrets(ctx, c.userID, c.deviceID, body.IDs)
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}
	metrics.SecretDequeued.Add(float64(n))
	return c.reply(wire.MsgSecretSynced, e.RequestID, wire.SecretSyncedBody{Count: int32(n), Done: true})
}

// secretPushJob mirrors notify.PushJob's JSON shape.
//
// Duplicated rather than imported for the same reason internal/fanout duplicates
// it: the gateway has no business depending on the notification worker. A named
// struct rather than a map because wire.Marshal has no mapping for one and
// discards the error — the failure mode that once made every push on the bus an
// empty payload with nothing reporting it.
//
// Preview is absent, and not merely left empty: there is no plaintext here to
// preview. The whole point of a secret chat is that the server holds ciphertext,
// so the notification can only ever say that something arrived.
type secretPushJob struct {
	UserID   string `json:"user_id"`
	SenderID string `json:"sender_id"`
	Secret   bool   `json:"secret"`
}

// pushSecretWake nudges a device that has queued ciphertext waiting.
//
// SenderID is deliberately omitted. A push provider is a third party, and
// telling it who is messaging whom hands it the conversation graph the secret
// chat exists to keep off other people's servers — which would be a strange
// thing to protect the contents of and then annotate.
func (g *Gateway) pushSecretWake(ctx context.Context, userID string) {
	if g.svc.Bus == nil {
		return
	}
	data, err := json.Marshal(secretPushJob{UserID: userID, Secret: true})
	if err != nil {
		return
	}
	if err := g.svc.Bus.Publish(ctx, eventbus.Event{
		Subject: eventbus.SubjNotifyPush, Key: userID, Data: data,
	}); err != nil {
		g.log.Warn("secret wake push failed", "user", logUser(userID), "err", err)
	}
}

// secretPurgeEvery is how often the collector runs, and secretPurgeBatch bounds
// one pass so a large backlog is cleared in chunks rather than in one statement
// that holds locks for as long as it takes.
const (
	secretPurgeEvery = 10 * time.Minute
	secretPurgeBatch = 500
)

// RunSecretQueueCollector drops envelopes nobody came back for, until ctx ends.
//
// This is not optional housekeeping. Each queued row records that one account
// messaged another at a particular moment — a conversation graph the stateless
// relay never wrote down and that secret chats exist to keep from accumulating.
// The TTL is the promise; this loop is what keeps it.
//
// Safe to run on every node: the delete is bounded and idempotent, so several
// collectors racing cost duplicate work rather than wrong results.
func (g *Gateway) RunSecretQueueCollector(ctx context.Context) {
	if g.svc.SecretQ == nil {
		return
	}
	t := time.NewTicker(secretPurgeEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			g.purgeSecretQueue(ctx)
		}
	}
}

func (g *Gateway) purgeSecretQueue(ctx context.Context) {
	now := time.Now().UnixMilli()
	for {
		n, err := g.svc.SecretQ.PurgeExpiredSecrets(ctx, now, secretPurgeBatch)
		if err != nil {
			g.log.Warn("secret queue purge failed", "err", err)
			return
		}
		if n > 0 {
			metrics.SecretExpired.Add(float64(n))
		}
		if n < secretPurgeBatch {
			return // caught up
		}
		select { // a large backlog must not pin this goroutine past shutdown
		case <-ctx.Done():
			return
		default:
		}
	}
}
