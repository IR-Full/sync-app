package wire

import (
	"net"
	"time"
)

// NewStreamTransport wraps any stream (TCP or QUIC) as a frame transport.
func NewStreamTransport(c StreamConn) Transport { return &streamTransport{conn: c} }

// NewTCPTransport wraps a net.Conn (which satisfies StreamConn).
func NewTCPTransport(c net.Conn) Transport { return NewStreamTransport(c) }

func (t *streamTransport) ReadFrame() ([]byte, error) { return ReadFrame(t.conn) }

func (t *streamTransport) WriteFrame(flags byte, payload []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return WriteFrame(t.conn, flags, payload)
}

func (t *streamTransport) SetReadDeadline(dl time.Time) error  { return t.conn.SetReadDeadline(dl) }
func (t *streamTransport) SetWriteDeadline(dl time.Time) error { return t.conn.SetWriteDeadline(dl) }

func (t *streamTransport) Close() error { return t.conn.Close() }

// NewConn wraps a Transport. If peerCompression is true, outbound frames larger
// than 1 KiB are gzip-compressed (unless zstd is enabled via SetZstd).
func NewConn(t Transport, peerCompression bool) *Conn {
	return &Conn{t: t, compress: peerCompression, compressMinLen: 256}
}

// ReadEnvelope reads and decodes the next envelope.
func (c *Conn) ReadEnvelope() (Envelope, error) {
	payload, err := c.t.ReadFrame()
	if err != nil {
		return Envelope{}, err
	}
	return DecodeEnvelope(payload)
}

// WriteEnvelope encodes and writes an envelope, compressing when worthwhile.
func (c *Conn) WriteEnvelope(e *Envelope) error {
	payload := e.Encode()
	var flags byte
	if len(payload) >= c.compressMinLen {
		switch {
		case c.zstd:
			flags |= FlagZstd
		case c.compress:
			flags |= FlagCompressed
		}
	}
	return c.t.WriteFrame(flags, payload)
}

// SetZstd enables zstd+dictionary compression for outbound frames (negotiated
// via CapZstd). Takes precedence over gzip.
func (c *Conn) SetZstd(on bool) { c.zstd = on }

// Send is a convenience constructor+writer for a typed message.
func (c *Conn) Send(t MsgType, seq, ack, reqID uint64, body any) error {
	e := Envelope{Type: t, Seq: seq, Ack: ack, RequestID: reqID}
	if body != nil {
		if b, ok := body.([]byte); ok {
			e.Body = b
		} else {
			e.Body = Marshal(body)
		}
	}
	return c.WriteEnvelope(&e)
}

// SetCompression toggles outbound compression (set after capability negotiation
// in the Hello/Welcome handshake).
func (c *Conn) SetCompression(on bool) { c.compress = on }

// WriteRaw writes a pre-encoded envelope payload as a frame (compressing per the
// connection policy). Used to replay buffered frames verbatim on session resume.
func (c *Conn) WriteRaw(payload []byte) error {
	var flags byte
	if c.compress && len(payload) >= c.compressMinLen {
		flags |= FlagCompressed
	}
	return c.t.WriteFrame(flags, payload)
}

// EncodeBody marshals a delivery body (a struct or raw []byte) to bytes.
func EncodeBody(body any) []byte {
	if body == nil {
		return nil
	}
	if b, ok := body.([]byte); ok {
		return b
	}
	return Marshal(body)
}

// SetReadDeadline bounds the next ReadEnvelope call (see Transport).
func (c *Conn) SetReadDeadline(t time.Time) error { return c.t.SetReadDeadline(t) }

// SetWriteDeadline bounds the next WriteEnvelope call (see Transport).
func (c *Conn) SetWriteDeadline(t time.Time) error { return c.t.SetWriteDeadline(t) }

// Close closes the transport.
func (c *Conn) Close() error { return c.t.Close() }
