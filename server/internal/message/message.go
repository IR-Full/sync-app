// Package message is the Message Write/Read Service (Sections 6, 9). The write
// path authorizes, persists durably+idempotently (seq allocated atomically in
// the store), then emits message.created to the event bus. Fanout/delivery is a
// separate concern (package fanout) driven off that event — this keeps the write
// path fast and the delivery path independently scalable.
package message

import (
	"context"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/internal/tracing"
	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/id"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// New builds the message service.
func New(msgs store.MessageStore, reads store.ReadStore, chats Chats, bus eventbus.Bus, ids *id.Generator) *Service {
	return &Service{msgs: msgs, reads: reads, chats: chats, bus: bus, ids: ids}
}

// Send authorizes, persists (idempotently), and emits the created event. It
// returns the stored message and whether this call resolved to an existing
// message (dup) — the gateway reports both back to the client in the SendAck.
func (s *Service) Send(ctx context.Context, in SendInput) (*model.Message, bool, error) {
	ctx, span := tracing.Start(ctx, "message.Send")
	defer span.End()
	ok, err := s.chats.CanPost(ctx, in.ChatID, in.SenderID)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, ErrForbidden
	}
	m := &model.Message{
		ID:         s.ids.NextString(),
		ChatID:     in.ChatID,
		SenderID:   in.SenderID,
		Text:       in.Text,
		MediaRef:   in.MediaRef,
		Attachment: in.Attachment,
		ReplyTo:    in.ReplyTo,
		Forward:    in.Forward,
		CreatedAt:  nowMs(),
	}
	if in.TTLSeconds > 0 {
		m.ExpiresAt = m.CreatedAt + int64(in.TTLSeconds)*1000
	}
	// Resolve the thread root: replying to a top-level message starts a thread
	// rooted at it; replying to something already in a thread joins that thread.
	// Computing this server-side keeps a whole branch under ONE root, so a thread
	// is a single indexed read instead of a recursive walk — and a client cannot
	// forge a root it has no access to.
	if in.ReplyTo != "" {
		if parent, err := s.msgs.GetMessage(ctx, in.ChatID, in.ReplyTo); err == nil {
			if parent.ThreadRoot != "" {
				m.ThreadRoot = parent.ThreadRoot
			} else {
				m.ThreadRoot = parent.ID
			}
		}
	}
	// The message.created event is staged in the SAME transaction as the insert
	// (transactional outbox) and published later by the relay — so a crash between
	// commit and publish cannot lose the event.
	stored, dup, err := s.msgs.InsertMessage(ctx, m, in.DedupKey, s.outboxFor(ctx, eventbus.SubjMessageCreated))
	if err != nil {
		return nil, false, err
	}
	return stored, dup, nil
}

// Edit changes a message's text. Only the original sender may edit.
func (s *Service) Edit(ctx context.Context, userID, chatID, msgID, text string) (*model.Message, error) {
	cur, err := s.msgs.GetMessage(ctx, chatID, msgID)
	if err != nil {
		return nil, err
	}
	if cur.SenderID != userID {
		return nil, ErrForbidden
	}
	m, err := s.msgs.EditMessage(ctx, chatID, msgID, text, nowMs(), s.outboxFor(ctx, eventbus.SubjMessageEdited))
	if err != nil {
		return nil, err
	}
	return m, nil
}

// WithMedia attaches the collector used when a message is deleted.
func (s *Service) WithMedia(c MediaCollector) *Service { s.media = c; return s }

// mediaRefsOf returns every blob a message points at.
func mediaRefsOf(m *model.Message) []string {
	if m == nil {
		return nil
	}
	refs := []string{}
	if m.MediaRef != "" {
		refs = append(refs, m.MediaRef)
	}
	if m.Attachment != nil {
		if m.Attachment.MediaRef != "" {
			refs = append(refs, m.Attachment.MediaRef)
		}
		if m.Attachment.ThumbRef != "" {
			refs = append(refs, m.Attachment.ThumbRef)
		}
	}
	return refs
}

// Delete tombstones a message. Sender may delete their own; chat admins/owner
// may delete for all (authorization simplified for MVP to sender-only + members).
func (s *Service) Delete(ctx context.Context, userID, chatID, msgID string) (*model.Message, error) {
	cur, err := s.msgs.GetMessage(ctx, chatID, msgID)
	if err != nil {
		return nil, err
	}
	if cur.SenderID != userID {
		// Allow admins/owner too.
		can, err := s.chats.CanPost(ctx, chatID, userID)
		if err != nil || !can {
			return nil, ErrForbidden
		}
	}
	// Capture the refs BEFORE the tombstone: the store clears them on the way out,
	// and the returned row no longer knows what it used to point at.
	refs := mediaRefsOf(cur)
	m, err := s.msgs.DeleteMessage(ctx, chatID, msgID, nowMs(), s.outboxFor(ctx, eventbus.SubjMessageDeleted))
	if err != nil {
		return nil, err
	}
	if s.media != nil && len(refs) > 0 {
		s.media.DeleteIfUnreferenced(ctx, "message", refs...)
	}
	return m, nil
}

// History returns a page of messages (newest first) for a member of the chat.
func (s *Service) History(ctx context.Context, userID, chatID string, beforeSeq uint64, limit int) ([]*model.Message, error) {
	member, err := s.chats.IsMember(ctx, chatID, userID)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, ErrForbidden
	}
	return s.msgs.History(ctx, chatID, beforeSeq, limit)
}

// MarkRead advances a user's read cursor and emits a read event so other members
// see the receipt.
func (s *Service) MarkRead(ctx context.Context, userID, chatID string, upToSeq uint64) error {
	member, err := s.chats.IsMember(ctx, chatID, userID)
	if err != nil {
		return err
	}
	if !member {
		return ErrForbidden
	}
	rs := &model.ReadState{ChatID: chatID, UserID: userID, UpToSeq: upToSeq, UpdatedAt: nowMs()}
	if err := s.reads.SetRead(ctx, rs); err != nil {
		return err
	}
	s.bus.Publish(ctx, eventbus.Event{
		Subject: eventbus.SubjMessageRead,
		Key:     chatID,
		Headers: tracing.Inject(ctx), // propagate trace context to fanout
		Data: wire.Marshal(wire.ReadUpdateBody{
			ChatID: chatID, UserID: userID, UpToChatSeq: upToSeq,
		}),
	})
	return nil
}

// outboxFor returns a MakeOutbox that serializes the given subject's event from
// the just-persisted message. The store calls it inside the write transaction,
// so the event body carries the final Seq and is committed atomically.
func (s *Service) outboxFor(ctx context.Context, subject string) store.MakeOutbox {
	trace := tracing.Inject(ctx) // capture the current span for the async consumer
	return func(m *model.Message) *store.OutboxRecord {
		body := wire.NewMessageBody{
			MessageID:  m.ID,
			ChatID:     m.ChatID,
			SenderID:   m.SenderID,
			ChatSeq:    m.Seq,
			Text:       m.Text,
			MediaRef:   m.MediaRef,
			Attachment: WireAttachment(m.Attachment),
			ReplyTo:    m.ReplyTo,
			Forward:    WireForward(m.Forward),
			ExpiresAt:  m.ExpiresAt,
			ThreadRoot: m.ThreadRoot,
			ReplyCount: m.ReplyCount,
			Edited:     m.Edited,
			Deleted:    m.Deleted,
			Timestamp:  m.CreatedAt,
		}
		return &store.OutboxRecord{
			ID:      s.ids.NextString(),
			Subject: subject,
			Key:     m.ChatID,
			Data:    wire.Marshal(body),
			Trace:   trace,
		}
	}
}

// Thread returns the replies under a thread root, oldest first, for a member of
// the chat. Threads read forward (a conversation branch grows downward), unlike
// history which pages backward from the newest message.
func (s *Service) Thread(ctx context.Context, userID, chatID, rootID string, afterSeq uint64, limit int) ([]*model.Message, error) {
	member, err := s.chats.IsMember(ctx, chatID, userID)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, ErrForbidden
	}
	tr, ok := s.msgs.(store.ThreadReader)
	if !ok {
		return nil, store.ErrNotFound // backend without thread support
	}
	return tr.Thread(ctx, chatID, rootID, afterSeq, limit)
}

// WireAttachment converts the domain attachment to its wire form (nil-safe).
// The two types are deliberately separate so the protocol can evolve without
// forcing a storage migration and vice versa.
func WireAttachment(a *model.Attachment) *wire.Attachment {
	if a == nil {
		return nil
	}
	return &wire.Attachment{
		Kind: string(a.Kind), MediaRef: a.MediaRef, Filename: a.Filename, MIME: a.MIME,
		Size: a.Size, DurationMs: a.DurationMs, Waveform: a.Waveform,
		Width: a.Width, Height: a.Height, ThumbRef: a.ThumbRef,
	}
}

// WireForward converts forward provenance to its wire form (nil-safe). It is a
// snapshot of where the copy came from, so it travels with every delivery of
// that message — live, history, or export — and never resolves the original.
func WireForward(f *model.ForwardOrigin) *wire.ForwardOrigin {
	if f == nil {
		return nil
	}
	return &wire.ForwardOrigin{ChatID: f.ChatID, MessageID: f.MessageID, SenderID: f.SenderID}
}

// ModelAttachment converts a wire attachment to its domain form (nil-safe).
func ModelAttachment(a *wire.Attachment) *model.Attachment {
	if a == nil {
		return nil
	}
	return &model.Attachment{
		Kind: model.AttachmentKind(a.Kind), MediaRef: a.MediaRef, Filename: a.Filename, MIME: a.MIME,
		Size: a.Size, DurationMs: a.DurationMs, Waveform: a.Waveform,
		Width: a.Width, Height: a.Height, ThumbRef: a.ThumbRef,
	}
}

// Forward copies a message into another chat. The COPY is independent content
// that only remembers its origin, so it survives the source being deleted; the
// forwarder must be able to read the source and post to the destination.
func (s *Service) Forward(ctx context.Context, userID, srcChatID, srcMsgID, dstChatID, dedupKey string) (*model.Message, bool, error) {
	ctx, span := tracing.Start(ctx, "message.Forward")
	defer span.End()

	// Reading the source requires membership there…
	member, err := s.chats.IsMember(ctx, srcChatID, userID)
	if err != nil {
		return nil, false, err
	}
	if !member {
		return nil, false, ErrForbidden
	}
	src, err := s.msgs.GetMessage(ctx, srcChatID, srcMsgID)
	if err != nil {
		return nil, false, err
	}
	if src.Deleted {
		return nil, false, store.ErrNotFound
	}
	// Provenance points at the ORIGINAL author, even when forwarding a forward —
	// otherwise a chain of forwards would credit the wrong person.
	origin := &model.ForwardOrigin{ChatID: srcChatID, MessageID: src.ID, SenderID: src.SenderID}
	if src.Forward != nil {
		origin = src.Forward
	}
	// …and posting requires permission in the destination (checked by Send).
	return s.Send(ctx, SendInput{
		SenderID: userID, ChatID: dstChatID, DedupKey: dedupKey,
		Text: src.Text, MediaRef: src.MediaRef, Attachment: src.Attachment,
		Forward: origin,
	})
}

// ExpireDue tombstones self-destructed messages whose deadline has passed and
// emits a deleted event for each, so live clients drop the content immediately
// instead of only on next fetch. Safe to run on several nodes (the store claims
// rows with SKIP LOCKED).
func (s *Service) ExpireDue(ctx context.Context, now int64, limit int) (int, error) {
	ex, ok := s.msgs.(store.Expirer)
	if !ok {
		return 0, nil
	}
	expired, err := ex.ExpireMessages(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	for _, m := range expired {
		s.bus.Publish(ctx, eventbus.Event{
			Subject: eventbus.SubjMessageDeleted,
			Key:     m.ChatID,
			Headers: tracing.Inject(ctx),
			Data:    wire.Marshal(wire.NewMessageBody{MessageID: m.ID, ChatID: m.ChatID, SenderID: m.SenderID, ChatSeq: m.Seq, Deleted: true, Timestamp: now}),
		})
	}
	return len(expired), nil
}
