package wire

import (
	"sync"

	"github.com/gorilla/websocket"
)

// wsTransport frames the custom protocol over a WebSocket connection. Each frame
// is sent as exactly one binary WebSocket message, so we reuse the same
// EncodeFrame/DecodeFrame codec as TCP — the framing bytes ride inside the WS
// binary payload. This keeps a single protocol definition across both
// transports (browsers get WS, native clients get raw TCP).
type wsTransport struct {
	conn *websocket.Conn
	mu   sync.Mutex
}
