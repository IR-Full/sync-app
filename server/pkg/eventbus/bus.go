// Package eventbus is the async backbone between services. The gateway and the
// message-write path publish domain events (message.created, message.read,
// typing, presence); the fanout and notification workers consume them. This
// decouples the write path (must be fast + durable) from delivery (best-effort,
// high fan-out), which is the core of a Telegram-class architecture.
//
// The interface is deliberately tiny so we can back it with an in-memory
// implementation for tests/single-node dev and NATS JetStream in production
// without touching call sites.
package eventbus

import "context"

// Subjects are the well-known topic names. We keep them centralized so
// producers and consumers cannot drift. Wildcards (NATS style) are used by the
// fanout worker to receive all per-chat message events.
const (
	SubjMessageCreated = "message.created" // emitted after durable write
	SubjMessageEdited  = "message.edited"
	SubjMessageDeleted = "message.deleted"
	SubjMessageRead    = "message.read"
	SubjReaction       = "message.reaction" // emitted after a reaction toggle
	SubjCallState      = "call.state"       // call room lifecycle/roster change
	SubjPollState      = "poll.state"       // poll created/voted/closed
	SubjPinned         = "chat.pinned"      // a chat.s pinned-message set changed
	SubjTyping         = "chat.typing"
	SubjPresence       = "user.presence"
	SubjNotifyPush     = "notify.push" // consumed by the notification worker
)

// Event is one published record. Data is the payload; Key is used for partition
// affinity so events for a chat preserve order; Headers carry cross-cutting
// metadata such as the W3C trace context, so a trace continues across the bus
// into async consumers (fanout, notification).
type Event struct {
	Subject string
	Key     string // e.g. chat id — used as partition/ordering key
	Data    []byte
	Headers map[string]string
}

// Handler processes one event. Returning an error signals the transport to
// redeliver (at-least-once). Handlers MUST be idempotent.
type Handler func(ctx context.Context, e Event) error

// Bus is the publish/subscribe abstraction.
type Bus interface {
	// Publish sends an event. It returns once the event is accepted by the
	// transport (durably, for JetStream).
	Publish(ctx context.Context, e Event) error
	// Subscribe registers a handler for a subject (may contain a trailing "*"
	// wildcard). The queue argument enables competing consumers: handlers in the
	// same queue group share the load; different groups each get a copy.
	Subscribe(subject, queue string, h Handler) error
	// Close releases resources.
	Close() error
}
