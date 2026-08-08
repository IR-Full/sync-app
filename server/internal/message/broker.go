package message

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/synapse-chat/synapse/internal/metrics"
	"github.com/synapse-chat/synapse/internal/tracing"
)

// NewBroker builds the message broker over the message service.
func NewBroker(svc *Service, log *slog.Logger) *Broker {
	return &Broker{svc: svc, log: log}
}

// Submit validates a command and applies it, returning the resulting message.
// All message writes (from the gateway) flow through here.
func (b *Broker) Submit(ctx context.Context, cmd Command) (Result, error) {
	ctx, span := tracing.Start(ctx, "broker.Submit."+string(cmd.Op))
	defer span.End()

	if err := validate(cmd); err != nil {
		return Result{}, err
	}
	switch cmd.Op {
	case OpCreate:
		m, dup, err := b.svc.Send(ctx, SendInput{
			SenderID: cmd.ActorID, ChatID: cmd.ChatID, DedupKey: cmd.DedupKey,
			Text: cmd.Text, MediaRef: cmd.MediaRef, ReplyTo: cmd.ReplyTo, Attachment: cmd.Attachment,
			TTLSeconds: cmd.TTLSeconds,
		})
		if err == nil && !dup {
			metrics.MessageOps.WithLabelValues(string(OpCreate)).Inc()
		}
		return Result{Message: m, Duplicate: dup}, err
	case OpEdit:
		m, err := b.svc.Edit(ctx, cmd.ActorID, cmd.ChatID, cmd.MessageID, cmd.Text)
		if err == nil {
			metrics.MessageOps.WithLabelValues(string(OpEdit)).Inc()
		}
		return Result{Message: m}, err
	case OpDelete:
		m, err := b.svc.Delete(ctx, cmd.ActorID, cmd.ChatID, cmd.MessageID)
		if err == nil {
			metrics.MessageOps.WithLabelValues(string(OpDelete)).Inc()
		}
		return Result{Message: m}, err
	default:
		return Result{}, fmt.Errorf("%w: %q", ErrBadCommand, cmd.Op)
	}
}

// validate enforces the domain rules shared by all mutation paths.
func validate(cmd Command) error {
	switch cmd.Op {
	case OpCreate:
		if len(cmd.Text) > MaxTextLen {
			return ErrTooLong
		}
		if cmd.Text == "" && cmd.MediaRef == "" && cmd.Attachment == nil {
			return ErrEmptyMessage
		}
	case OpEdit:
		if len(cmd.Text) > MaxTextLen {
			return ErrTooLong
		}
		if cmd.Text == "" {
			return ErrEmptyMessage // an edit to empty is a delete; keep them distinct
		}
	case OpDelete:
		if cmd.MessageID == "" {
			return ErrBadCommand
		}
	default:
		return ErrBadCommand
	}
	return nil
}
