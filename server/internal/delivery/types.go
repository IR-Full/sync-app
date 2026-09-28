package delivery

import (
	"sync"

	"github.com/SyncApp-chat/SyncApp/pkg/wire"
)

// Delivery is one server→client push (a message, read receipt, typing, etc.) or
// a correlated response to a client request. RequestID links a response to its
// request (0 for unsolicited pushes). All outbound frames go through the single
// writer that assigns the connection Seq, so on-wire order always matches Seq.
type Delivery struct {
	Type      wire.MsgType
	RequestID uint64
	Body      any // marshaled to the envelope body by the connection
	// OnWritten, if set, runs after this frame has been written to the socket —
	// not when it was enqueued. That distinction is the whole point for delivery
	// receipts: everything before the write is a promise, and a queued frame on a
	// connection that dies is never delivered at all.
	//
	// It runs on the connection's single writer goroutine and MUST NOT block:
	// anything slow (a registry lookup, a publish) belongs off that path.
	OnWritten func()

	// BodyFor overrides Body when the frame has to differ PER CONNECTION.
	//
	// Exactly one thing needs it, and it is worth naming: a secret message carries
	// its ratchet payload either as raw bytes or as base64, and which one depends
	// on what the destination socket negotiated — not on what the sender sent, and
	// not on anything the routing layer knows. Everything else about a delivery is
	// identical for every recipient, so Body stays the normal path and this is the
	// exception.
	//
	// Called on the writer goroutine with that connection's negotiated capabilities.
	// Like OnWritten it must not block. Nil means use Body.
	BodyFor func(caps wire.Cap) any
}

// Sink is a live connection able to receive pushes. Send must be non-blocking
// (enqueue then return) so one slow client cannot stall fanout; implementations
// drop or disconnect on overflow (backpressure policy lives in the gateway).
type Sink interface {
	Send(d Delivery) bool // returns false if the connection's queue is full
	DeviceID() string
}

// hubShards is how many independent maps the Hub is split across.
//
// The Hub had one RWMutex over one map holding every connection on the node. A
// read (Route) takes it shared, which is fine; Register and its unregister take
// it exclusively, and those are not rare — they happen on every connect and every
// disconnect. A node holding hundreds of thousands of connections goes through a
// reconnect storm after any deploy, and during one, every exclusive lock blocks
// every delivery on the node.
//
// 256 is chosen to be comfortably more than the core count of any machine this
// runs on, so contention is bounded by the hash distribution rather than by the
// shard count, while staying small enough that the shards themselves are a
// rounding error in memory.
const hubShards = 256

// Hub maps userID → set of connected device sinks, sharded by user.
//
// Sharding by USER (not by connection) is what keeps the per-user operations
// atomic: every method here addresses one user, so each falls entirely inside one
// shard and needs exactly one lock. A Hub sharded by connection would have to
// take several to answer "is this user online".
type Hub struct {
	shards [hubShards]hubShard
}

type hubShard struct {
	mu    sync.RWMutex
	users map[string]map[string]Sink // userID -> deviceID -> sink
}
