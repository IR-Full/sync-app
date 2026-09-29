package message

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/IR-Full/sync-app/server/internal/metrics"
	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/tracing"
)

// The Broker is the single write-side entry point for message mutations. It
// lives in the message service (not a separate microservice) on purpose: create,
// edit, and delete all act on the same aggregate and must share one transaction
// (per-chat seq allocation + insert + transactional-outbox event are atomic).
// Fronting that with a network service would mean a distributed transaction for
// no benefit. The Broker instead centralizes the cross-cutting write concerns —
// validation, tracing, metrics, and uniform error handling — behind one typed
// command interface, giving a clean seam if the write path is ever peeled off.

// MaxTextLen bounds a message's text (generous vs Telegram's 4096). The frame
// cap (16 MiB) is a transport guard; this is the domain rule.
const MaxTextLen = 8192

var (
	// ErrEmptyMessage means a create/edit carried neither text nor media.
	ErrEmptyMessage = errors.New("message: empty (no text or media)")
	// ErrTooLong means the text exceeds MaxTextLen.
	ErrTooLong = errors.New("message: text too long")
	// ErrBadCommand means an unknown or malformed command.
	ErrBadCommand = errors.New("message: bad command")
)

const (
	OpCreate Op = "create"
	OpEdit   Op = "edit"
	OpDelete Op = "delete"
)

// Op is a message-mutation kind.
type Op string

// Command is a single message mutation request. Fields are used per Op:
// create uses ChatID/DedupKey/Text/MediaRef/ReplyTo; edit uses ChatID/MessageID/
// Text; delete uses ChatID/MessageID.
type Command struct {
	Op         Op
	ActorID    string // the user performing the mutation
	ChatID     string
	MessageID  string
	DedupKey   string
	Text       string
	MediaRef   string
	ReplyTo    string
	Attachment *model.Attachment
	// TTLSeconds self-destructs the created message that many seconds after it
	// lands (0 = never). Carried through the broker rather than applied at the
	// edge so every write path gets the same deadline arithmetic.
	TTLSeconds int32
}

// Result is the outcome of a mutation.
type Result struct {
	Message   *model.Message
	Duplicate bool // create only: resolved to an existing message via DedupKey
}

// Broker validates and dispatches message-mutation commands to the write path.
type Broker struct {
	svc *Service
	log *slog.Logger
}

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
