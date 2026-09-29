package wire

import (
	"io"
	"net"
	"sync"
	"time"
)

// Transport is the byte-frame abstraction the gateway and client share. It
// hides whether the underlying connection is raw TCP or WebSocket: both deliver
// and accept whole envelope payloads. Implementations must be safe for one
// reader goroutine and one writer goroutine used concurrently.
type Transport interface {
	// ReadFrame blocks for the next frame and returns its (decompressed)
	// envelope payload.
	ReadFrame() ([]byte, error)
	// WriteFrame writes one frame. Callers set flags (e.g. FlagCompressed).
	WriteFrame(flags byte, payload []byte) error
	// SetReadDeadline bounds how long the next ReadFrame may block. A zero time
	// disables the deadline. Used to defend against slow-loris style attacks
	// where a peer connects but never completes the handshake.
	SetReadDeadline(t time.Time) error
	// SetWriteDeadline bounds how long a WriteFrame may block. Guards against a
	// wedged client whose TCP receive window is full, which would otherwise pin
	// the writer goroutine indefinitely.
	SetWriteDeadline(t time.Time) error
	// Close closes the underlying connection.
	Close() error
}

// rawTransport is implemented by this package's transports. It hands back a
// frame's flags and still-compressed payload, so Conn can refuse compression the
// peer never negotiated and cap the frame size BEFORE the body is read — neither
// of which ReadFrame can do, because it decompresses as it goes.
type rawTransport interface {
	readRawFrame(max int) (flags byte, payload []byte, err error)
}

// StreamConn is any reliable, ordered byte stream with deadlines: a TCP
// net.Conn, or a QUIC bidirectional stream. The frame codec is stream-oriented,
// so the same transport works for both — TCP and QUIC differ only in how the
// stream is obtained.
type StreamConn interface {
	io.Reader
	io.Writer
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
	Close() error
}

// streamTransport frames the custom protocol over any StreamConn.
type streamTransport struct {
	conn StreamConn
	mu   sync.Mutex // serializes writes; a frame must not interleave on the wire
}

// Conn is a higher-level envelope-oriented connection built on a Transport. It
// owns the compression policy and gives the gateway/client a clean
// ReadEnvelope/WriteEnvelope API.
type Conn struct {
	t              Transport
	compress       bool // send FlagCompressed (gzip) when body is large enough
	zstd           bool // prefer zstd+dictionary over gzip when negotiated
	compressMinLen int

	// Inbound policy (see SetInboundPolicy). Unrestricted by default so clients,
	// tools and tests built on this package keep working unchanged.
	restrictIn bool
	maxIn      int  // largest accepted payload as sent (before decompression)
	allowIn    byte // compression flags the peer may use (FlagCompressed|FlagZstd)
}

// NewStreamTransport wraps any stream (TCP or QUIC) as a frame transport.
func NewStreamTransport(c StreamConn) Transport { return &streamTransport{conn: c} }

// NewTCPTransport wraps a net.Conn (which satisfies StreamConn).
func NewTCPTransport(c net.Conn) Transport { return NewStreamTransport(c) }

func (t *streamTransport) ReadFrame() ([]byte, error) { return ReadFrame(t.conn) }

func (t *streamTransport) readRawFrame(max int) (byte, []byte, error) {
	return readRawFrame(t.conn, max)
}

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
	payload, err := c.readPayload()
	if err != nil {
		return Envelope{}, err
	}
	return DecodeEnvelope(payload)
}

func (c *Conn) readPayload() ([]byte, error) {
	rt, ok := c.t.(rawTransport)
	if !c.restrictIn || !ok {
		return c.t.ReadFrame()
	}
	flags, raw, err := rt.readRawFrame(c.maxIn)
	if err != nil {
		return nil, err
	}
	if flags&(FlagCompressed|FlagZstd)&^c.allowIn != 0 {
		return nil, ErrCompressionNotNegotiated
	}
	return decompress(flags, raw)
}

// SetInboundPolicy restricts what this connection will read from here on:
// frames whose payload (as sent) exceeds maxPayload are refused before their body
// is read, and a frame compressed with anything outside allowed is refused
// before it is decompressed. allowed is a mask of FlagCompressed and FlagZstd.
//
// A server calls this before the handshake with a small limit and no
// compression, then widens it as the peer earns it: the flag on a frame is
// chosen by whoever sent it, so without this a stranger's very first frame gets
// to make us run a decompressor.
//
// Call it from the goroutine that reads; it is not synchronised with
// ReadEnvelope.
func (c *Conn) SetInboundPolicy(maxPayload int, allowed byte) {
	c.restrictIn = true
	c.maxIn = maxPayload
	c.allowIn = allowed & (FlagCompressed | FlagZstd)
}

// WriteEnvelope encodes and writes an envelope, compressing when worthwhile.
func (c *Conn) WriteEnvelope(e *Envelope) error {
	return c.WriteRaw(e.Encode())
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
//
// Every post-handshake frame on the gateway goes through here, so this is where
// the negotiated algorithm — zstd or gzip — has to be honoured.
func (c *Conn) WriteRaw(payload []byte) error {
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
