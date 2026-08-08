package message

import (
	"log/slog"

	"github.com/synapse-chat/synapse/internal/model"
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
