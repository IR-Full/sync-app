// Package router is the cross-node delivery registry that makes the gateway
// horizontally scalable. Each gateway node registers which users have live
// connections on it; fanout looks a recipient up and publishes a node-targeted
// delivery on the event bus, which the owning node consumes and hands to its
// local Hub. Without this, a message would only reach recipients connected to
// the same node that happened to process the event — the single-node ceiling.
//
// Flow:
//
//	conn established on node N  → Router.Bind(user, device, N)
//	message.created (any node)  → fanout: nodes = Router.NodesFor(user)
//	                              for each node → bus.Publish("deliver."+node, NodeDelivery)
//	node N subscribes "deliver.N" → Hub.Route(user, ...) to local connections
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

// DeliverSubject is the per-node bus subject a gateway subscribes to.
func DeliverSubject(nodeID string) string { return "deliver." + nodeID }

// NewMemory returns an in-process router.
func NewMemory() Router {
	return &memoryRouter{
		users:   make(map[string]map[string]int),
		devices: make(map[string]map[string]int),
	}
}

// deviceKey addresses the per-device index. A device id is client-asserted and
// may repeat across accounts, so it is only meaningful paired with its owner.
func deviceKey(userID, deviceID string) string { return userID + "|" + deviceID }

func (m *memoryRouter) Bind(_ context.Context, userID, deviceID, nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.users[userID] == nil {
		m.users[userID] = make(map[string]int)
	}
	m.users[userID][nodeID]++
	if deviceID == "" {
		return nil
	}
	k := deviceKey(userID, deviceID)
	if m.devices[k] == nil {
		m.devices[k] = make(map[string]int)
	}
	m.devices[k][nodeID]++
	return nil
}

func (m *memoryRouter) Unbind(_ context.Context, userID, deviceID, nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	decRef(m.users, userID, nodeID)
	if deviceID != "" {
		decRef(m.devices, deviceKey(userID, deviceID), nodeID)
	}
	return nil
}

// decRef drops one reference to a node under key, removing the node at zero and
// the key when it holds none.
func decRef(index map[string]map[string]int, key, nodeID string) {
	nodes := index[key]
	if nodes == nil {
		return
	}
	if nodes[nodeID] > 0 {
		nodes[nodeID]--
		if nodes[nodeID] == 0 {
			delete(nodes, nodeID)
		}
	}
	if len(nodes) == 0 {
		delete(index, key)
	}
}

func (m *memoryRouter) NodesFor(_ context.Context, userID string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	nodes := m.users[userID]
	out := make([]string, 0, len(nodes))
	for n := range nodes {
		out = append(out, n)
	}
	return out, nil
}

func (m *memoryRouter) NodesForMany(_ context.Context, userIDs []string) (map[string][]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string][]string, len(userIDs))
	for _, uid := range userIDs {
		nodes := m.users[uid]
		if len(nodes) == 0 {
			continue // absent means offline; no entry rather than an empty slice
		}
		list := make([]string, 0, len(nodes))
		for n := range nodes {
			list = append(list, n)
		}
		out[uid] = list
	}
	return out, nil
}

func (m *memoryRouter) NodesForDevice(_ context.Context, userID, deviceID string) ([]string, error) {
	if deviceID == "" {
		return m.NodesFor(context.Background(), userID)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	nodes := m.devices[deviceKey(userID, deviceID)]
	out := make([]string, 0, len(nodes))
	for n := range nodes {
		out = append(out, n)
	}
	return out, nil
}

func (m *memoryRouter) Refresh(context.Context, string, string) error { return nil }
