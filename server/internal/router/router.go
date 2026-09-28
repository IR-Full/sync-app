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
)

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
