package e2e

import (
	"sync"
	"time"
)

// TrustStore records the identity key first seen for each peer device. A client
// persists it; losing it is not fatal, it only means every peer is trusted on
// first use again.
type TrustStore struct {
	mu     sync.RWMutex
	pinned map[string]PinnedIdentity
}

// PinnedIdentity is what was recorded for one peer device.
type PinnedIdentity struct {
	UserID      string
	DeviceID    string
	IdentityKey []byte
	SigningKey  []byte
	FirstSeen   time.Time
}
