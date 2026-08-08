package wire

import (
	"io"
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
}
