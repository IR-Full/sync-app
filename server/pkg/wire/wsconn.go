package wire

import (
	"time"

	"github.com/gorilla/websocket"
)

// NewWSTransport wraps a gorilla WebSocket connection.
//
// It caps the message size at one maximal frame. gorilla has no limit by
// default and buffers a whole message before returning it, so without this the
// frame's own length check ran only after an arbitrarily large message had
// already been read into memory.
func NewWSTransport(c *websocket.Conn) Transport {
	c.SetReadLimit(HeaderSize + MaxPayloadSize)
	return &wsTransport{conn: c}
}

func (t *wsTransport) ReadFrame() ([]byte, error) {
	flags, raw, err := t.readRawFrame(MaxPayloadSize)
	if err != nil {
		return nil, err
	}
	return decompress(flags, raw)
}

func (t *wsTransport) readRawFrame(max int) (byte, []byte, error) {
	// Applied per read so a policy change (e.g. widening after auth) takes effect
	// on the next message; gorilla closes with 1009 when it is exceeded.
	if max <= 0 || max > MaxPayloadSize {
		max = MaxPayloadSize
	}
	t.conn.SetReadLimit(int64(HeaderSize + max))
	for {
		mt, data, err := t.conn.ReadMessage()
		if err != nil {
			return 0, nil, err
		}
		// Ignore text/control frames; the protocol only uses binary messages.
		if mt != websocket.BinaryMessage {
			continue
		}
		return decodeRawFrame(data, max)
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
