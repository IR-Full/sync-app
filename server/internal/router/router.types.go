package router

import (
	"context"
	"sync"
)

// Router maps users to the gateway nodes holding their live connections.
type Router interface {
	// Bind records that (user, device) is connected on node. Idempotent per call;
	// multiple devices of a user on one node are refcounted.
	Bind(ctx context.Context, userID, deviceID, nodeID string) error
	// Unbind removes a (user, device) binding on node.
	Unbind(ctx context.Context, userID, deviceID, nodeID string) error
	// NodesFor returns the distinct nodes with a live connection for the user.
	NodesFor(ctx context.Context, userID string) ([]string, error)
	// NodesForMany answers NodesFor for a whole page of users in ONE round trip.
	//
	// It exists because fanout asked per recipient, serially. A group of 200
	// people cost 200 sequential Redis round trips for a single message — and the
	// same 200 for every typing indicator, read receipt, reaction and poll tally,
	// which is the highest-frequency traffic in the system. The answer for one
	// user is a handful of bytes; the expense was entirely the round trip, so the
	// fix is to stop making 200 of them.
	//
	// Users absent from the result (or present with an empty slice) are offline.
	NodesForMany(ctx context.Context, userIDs []string) (map[string][]string, error)
	// NodesForDevice narrows that to ONE device of the user.
	//
	// It exists because "is this user online" and "is this device reachable" are
	// different questions, and the E2E relay asks the second one: a secret
	// message is encrypted to a single device's ratchet session, and no other
	// device of the same account can read it. Bind took a deviceID from the very
	// beginning and both backends discarded it, so the only answer available was
	// the user-level one — which reports success when someone's phone is online
	// and the ciphertext was addressed to their laptop.
	NodesForDevice(ctx context.Context, userID, deviceID string) ([]string, error)
	// Refresh extends the liveness of a connection's bindings (heartbeat), so a
	// crashed node's entries expire instead of leaking (Redis backend).
	//
	// The second parameter is the DEVICE, not the node. Every implementation
	// ignored the node id — the Redis backend refreshes the whole key regardless
	// of which node asked, and the in-memory one has no TTL to extend — while the
	// per-device index added for NodesForDevice genuinely needs refreshing, or it
	// expires under a connection that is still open and the relay starts queueing
	// ciphertext for a device that is holding the socket.
	Refresh(ctx context.Context, userID, deviceID string) error
}

// NodeDelivery is the payload published to a node's deliver subject. Body is the
// already-encoded envelope body bytes; the receiving node wraps it in a frame.
//
// Users is a LIST, and that is the whole point of the type's current shape. It
// used to name one recipient, so a message to a group produced one publish per
// member — each carrying a full copy of the body. A thousand-member chat spread
// over ten nodes sent a thousand copies of the payload across the bus to deliver
// ten distinct frames' worth of information. One delivery per NODE, naming the
// recipients that live there, sends the body once per node instead.
//
// See codec.go for the wire format; it is hand-rolled binary rather than JSON
// because this path carries every delivered message in the system.
type NodeDelivery struct {
	Users    []string
	DeviceID string // set for device-targeted (secret) delivery
	Type     uint16
	Body     []byte
}

// --- in-memory router (single-node dev / tests) ---

type memoryRouter struct {
	mu      sync.RWMutex
	users   map[string]map[string]int // userID -> nodeID -> device refcount
	devices map[string]map[string]int // "userID|deviceID" -> nodeID -> conn refcount
}
