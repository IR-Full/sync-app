package wire

import (
	"time"

	"github.com/gorilla/websocket"
)

// NewWSTransport wraps a gorilla WebSocket connection.
func NewWSTransport(c *websocket.Conn) Transport { return &wsTransport{conn: c} }

func (t *wsTransport) ReadFrame() ([]byte, error) {
	for {
		mt, data, err := t.conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		// Ignore text/control frames; the protocol only uses binary messages.
		if mt != websocket.BinaryMessage {
			continue
		}
		return DecodeFrame(data)
	}
}

func (t *wsTransport) WriteFrame(flags byte, payload []byte) error {
	b, err := EncodeFrame(flags, payload)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.conn.WriteMessage(websocket.BinaryMessage, b)
}

func (t *wsTransport) SetReadDeadline(dl time.Time) error  { return t.conn.SetReadDeadline(dl) }
func (t *wsTransport) SetWriteDeadline(dl time.Time) error { return t.conn.SetWriteDeadline(dl) }

func (t *wsTransport) Close() error { return t.conn.Close() }
