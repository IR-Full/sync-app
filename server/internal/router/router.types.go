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
	// Refresh extends the liveness of a user's bindings on node (heartbeat), so a
	// crashed node's entries expire instead of leaking (Redis backend).
	Refresh(ctx context.Context, userID, nodeID string) error
}

// NodeDelivery is the payload published to a node's deliver subject. Body is the
// already-encoded envelope body bytes; the receiving node wraps it in a frame.
type NodeDelivery struct {
	UserID   string `json:"u"`
	DeviceID string `json:"d,omitempty"` // set for device-targeted (secret) delivery
	Type     uint16 `json:"t"`
	Body     []byte `json:"b,omitempty"`
}

// --- in-memory router (single-node dev / tests) ---

type memoryRouter struct {
	mu    sync.RWMutex
	users map[string]map[string]int // userID -> nodeID -> device refcount
}
