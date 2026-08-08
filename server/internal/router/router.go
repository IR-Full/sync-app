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
	"encoding/json"
)

// DeliverSubject is the per-node bus subject a gateway subscribes to.
func DeliverSubject(nodeID string) string { return "deliver." + nodeID }

// Encode/Decode serialize NodeDelivery for the bus (JSON; Body is base64 via the
// standard []byte JSON encoding).
func (n NodeDelivery) Encode() []byte { b, _ := json.Marshal(n); return b }

// DecodeNodeDelivery parses a NodeDelivery from bus bytes.
func DecodeNodeDelivery(b []byte) (NodeDelivery, error) {
	var n NodeDelivery
	err := json.Unmarshal(b, &n)
	return n, err
}

// NewMemory returns an in-process router.
func NewMemory() Router {
	return &memoryRouter{users: make(map[string]map[string]int)}
}

func (m *memoryRouter) Bind(_ context.Context, userID, _, nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.users[userID] == nil {
		m.users[userID] = make(map[string]int)
	}
	m.users[userID][nodeID]++
	return nil
}

func (m *memoryRouter) Unbind(_ context.Context, userID, _, nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	nodes := m.users[userID]
	if nodes == nil {
		return nil
	}
	if nodes[nodeID] > 0 {
		nodes[nodeID]--
		if nodes[nodeID] == 0 {
			delete(nodes, nodeID)
		}
	}
	if len(nodes) == 0 {
		delete(m.users, userID)
	}
	return nil
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

func (m *memoryRouter) Refresh(context.Context, string, string) error { return nil }
