// Package delivery is the in-node routing table from user/device to live
// connections. The gateway registers a Sink per authenticated connection; the
// fanout worker looks up recipients and pushes events. In a multi-node
// deployment each gateway node owns a Hub for its locally-connected devices, and
// fanout is partitioned so a chat's events reach the nodes holding its members
// (via the event bus subject + a presence/routing lookup). For the MVP single
// node, one Hub holds everyone.
package delivery

// NewHub creates an empty routing table.
func NewHub() *Hub {
	return &Hub{users: make(map[string]map[string]Sink)}
}

// Register adds a sink and returns an unregister func to call on disconnect.
func (h *Hub) Register(userID string, s Sink) func() {
	h.mu.Lock()
	if h.users[userID] == nil {
		h.users[userID] = make(map[string]Sink)
	}
	h.users[userID][s.DeviceID()] = s
	h.mu.Unlock()

	return func() {
		h.mu.Lock()
		if devs := h.users[userID]; devs != nil {
			// Only remove if it is still the same sink (guards against a
			// reconnect having replaced it).
			if cur, ok := devs[s.DeviceID()]; ok && cur == s {
				delete(devs, s.DeviceID())
			}
			if len(devs) == 0 {
				delete(h.users, userID)
			}
		}
		h.mu.Unlock()
	}
}

// Route pushes d to every connected device of userID. Returns the number of
// sinks reached; 0 means the user is offline on this node (caller may enqueue a
// push notification).
func (h *Hub) Route(userID string, d Delivery) int {
	h.mu.RLock()
	devs := h.users[userID]
	sinks := make([]Sink, 0, len(devs))
	for _, s := range devs {
		sinks = append(sinks, s)
	}
	h.mu.RUnlock()

	n := 0
	for _, s := range sinks {
		if s.Send(d) {
			n++
		}
	}
	return n
}

// RouteDevice pushes d to one specific device of a user (used for E2E secret
// messages, which are addressed to a single device, not all of a user's
// devices). Returns true if that device was connected on this node.
func (h *Hub) RouteDevice(userID, deviceID string, d Delivery) bool {
	h.mu.RLock()
	var s Sink
	if devs := h.users[userID]; devs != nil {
		s = devs[deviceID]
	}
	h.mu.RUnlock()
	if s == nil {
		return false
	}
	return s.Send(d)
}

// IsOnline reports whether the user has any connected device on this node.
func (h *Hub) IsOnline(userID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.users[userID]) > 0
}
