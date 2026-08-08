package delivery

import (
	"sync"

	"github.com/synapse-chat/synapse/pkg/wire"
)

// Delivery is one server→client push (a message, read receipt, typing, etc.) or
// a correlated response to a client request. RequestID links a response to its
// request (0 for unsolicited pushes). All outbound frames go through the single
// writer that assigns the connection Seq, so on-wire order always matches Seq.
type Delivery struct {
	Type      wire.MsgType
	RequestID uint64
	Body      any // marshaled to the envelope body by the connection
}

// Sink is a live connection able to receive pushes. Send must be non-blocking
// (enqueue then return) so one slow client cannot stall fanout; implementations
// drop or disconnect on overflow (backpressure policy lives in the gateway).
type Sink interface {
	Send(d Delivery) bool // returns false if the connection's queue is full
	DeviceID() string
}

// Hub maps userID → set of connected device sinks.
type Hub struct {
	mu    sync.RWMutex
	users map[string]map[string]Sink // userID -> deviceID -> sink
}
