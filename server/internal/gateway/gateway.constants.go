package gateway

import "time"

const (
	RoleAdmin     Role = "admin"
	RoleModerator Role = "moderator"
)

// deliveredQueueDepth bounds the delivery-receipt backlog per node. Deep enough
// that a normal burst is absorbed, shallow enough that a node which cannot keep
// up drops decorations instead of growing a queue nobody is draining.
const deliveredQueueDepth = 4096

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		ServerVersion:    "synapse/0.1",
		Heartbeat:        20 * time.Second,
		IdleTimeout:      60 * time.Second,
		HandshakeTimeout: 10 * time.Second,
		WriteTimeout:     15 * time.Second,
		MaxInflight:      256,
		SendRate:         20,
		SendBurst:        40,
		TypingRate:       2,
		TypingBurst:      5,
		TypingChatRate:   0.5,
		TypingChatBurst:  2,
		SignalRate:       20,
		SignalBurst:      60,
		AcceptLoops:      4,
	}
}
