// Package delivery is the in-node routing table from user/device to live
// connections. The gateway registers a Sink per authenticated connection; the
// fanout worker looks up recipients and pushes events. In a multi-node
// deployment each gateway node owns a Hub for its locally-connected devices, and
// fanout is partitioned so a chat's events reach the nodes holding its members
// (via the event bus subject + a presence/routing lookup). For the MVP single
// node, one Hub holds everyone.
package delivery

import "hash/fnv"

// NewHub creates an empty routing table.
func NewHub() *Hub {
	h := &Hub{}
	for i := range h.shards {
		h.shards[i].users = make(map[string]map[string]Sink)
	}
	return h
}

// shardFor picks the shard owning a user.
//
// FNV-1a rather than something cryptographic: the input is an id the client
// cannot choose (a server-minted snowflake), so there is no adversary to
// distribute against — only the natural clustering of sequential ids, which any
// avalanching hash breaks up. It is also the same hash the sharded message store
// uses, so there is one bucketing idiom in the codebase rather than two.
func (h *Hub) shardFor(userID string) *hubShard {
	f := fnv.New32a()
	_, _ = f.Write([]byte(userID))
	return &h.shards[f.Sum32()%hubShards]
}

// Register adds a sink and returns an unregister func to call on disconnect.
func (h *Hub) Register(userID string, s Sink) func() {
	sh := h.shardFor(userID)
	sh.mu.Lock()
	if sh.users[userID] == nil {
		sh.users[userID] = make(map[string]Sink)
	}
	sh.users[userID][s.DeviceID()] = s
	sh.mu.Unlock()

	return func() {
		sh.mu.Lock()
		if devs := sh.users[userID]; devs != nil {
			// Only remove if it is still the same sink (guards against a
			// reconnect having replaced it).
			if cur, ok := devs[s.DeviceID()]; ok && cur == s {
				delete(devs, s.DeviceID())
			}
			if len(devs) == 0 {
				delete(sh.users, userID)
			}
		}
		sh.mu.Unlock()
	}
}

// Route pushes d to every connected device of userID. Returns the number of
// sinks reached; 0 means the user is offline on this node (caller may enqueue a
// push notification).
func (h *Hub) Route(userID string, d Delivery) int {
	sh := h.shardFor(userID)
	sh.mu.RLock()
	devs := sh.users[userID]
	sinks := make([]Sink, 0, len(devs))
	for _, s := range devs {
		sinks = append(sinks, s)
	}
	sh.mu.RUnlock()

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
	sh := h.shardFor(userID)
	sh.mu.RLock()
	var s Sink
	if devs := sh.users[userID]; devs != nil {
		s = devs[deviceID]
	}
	sh.mu.RUnlock()
	if s == nil {
		return false
	}
	return s.Send(d)
}

// IsOnline reports whether the user has any connected device on this node.
func (h *Hub) IsOnline(userID string) bool {
	sh := h.shardFor(userID)
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	return len(sh.users[userID]) > 0
}
