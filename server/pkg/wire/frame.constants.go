package wire

import (
	"errors"
	"sync"
)

const (
	// Magic0/Magic1 are the two sync bytes at the start of every frame.
	Magic0 byte = 0x53 // 'S'
	Magic1 byte = 0x43 // 'C'

	// Version is the current framing version.
	Version byte = 0x01

	// HeaderSize is the fixed frame header length in bytes.
	HeaderSize = 8

	// MaxPayloadSize bounds a single frame payload (16 MiB). Large media never
	// travels as a protocol frame — it goes through the media HTTP pipeline and
	// only a reference travels in-band. This cap protects the parser from
	// hostile length prefixes.
	MaxPayloadSize = 16 << 20
)

// Frame flag bits.
const (
	FlagCompressed byte = 1 << 0 // payload is gzip-compressed
	FlagZstd       byte = 1 << 1 // payload is zstd-compressed (with shared dict)
	// bits 2..7 reserved for future use (e.g. per-frame encryption marker).
)

var (
	// ErrBadMagic means the first two bytes were not the sync word.
	ErrBadMagic = errors.New("wire: bad magic")
	// ErrBadVersion means the framing version is unsupported.
	ErrBadVersion = errors.New("wire: unsupported version")
	// ErrTooLarge means the length prefix exceeds MaxPayloadSize.
	ErrTooLarge = errors.New("wire: payload too large")
)

// frameBufPool recycles the header+payload assembly buffer used by WriteFrame.
// Every outbound frame on the TCP/QUIC path assembles one contiguous buffer for a
// single Write; pooling it removes that per-frame allocation on the hottest path
// (acks, receipts, fanout pushes), cutting GC pressure under load. Safe because
// WriteFrame's Write is synchronous — the socket has copied the bytes out before
// the buffer returns to the pool.
var frameBufPool = sync.Pool{New: func() any { b := make([]byte, 0, 2048); return &b }}

// poolBufCap bounds which buffers go back to the pool: an occasional large frame
// must not pin megabytes in the pool for the process lifetime.
const poolBufCap = 64 << 10
