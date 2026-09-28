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
		ServerVersion: "SyncApp/0.1",
		// The name an authenticator app shows next to the account. A default rather
		// than a required setting, because an empty issuer produces an entry called
		// nothing, and a user with three of those cannot tell them apart.
		TOTPIssuer:       "SyncApp",
		Heartbeat:        20 * time.Second,
		IdleTimeout:      60 * time.Second,
		HandshakeTimeout: 10 * time.Second,
		WriteTimeout:     15 * time.Second,
		MaxInflight:      256,
		SendRate:         20,
		SendBurst:        40,
		// Reads are cheaper per request than writes but amplify far more, so the
		// sustained rate is generous and the burst is what actually bounds a
		// scroll: opening a chat fires a handful of pages back to back, then goes
		// quiet. 60/120 lets a client page through six thousand messages a minute
		// and still refuses a loop.
		ReadRate:        60,
		ReadBurst:       120,
		TypingRate:      2,
		TypingBurst:     5,
		TypingChatRate:  0.5,
		TypingChatBurst: 2,
		SignalRate:      20,
		SignalBurst:     60,
		AcceptLoops:     4,
	}
}
