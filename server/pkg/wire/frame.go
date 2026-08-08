// Package wire implements the Synapse custom binary application protocol
// spoken between clients and the realtime gateway over raw TCP and over
// WebSocket (each WS binary message carries exactly one frame).
//
// Frame layout (network byte order / big-endian):
//
//	+--------+--------+--------+--------+--------------------+==================+
//	| MAGIC(2)        | VER(1) | FLAGS  | LENGTH (4)         | PAYLOAD (LENGTH) |
//	+--------+--------+--------+--------+--------------------+==================+
//	  0x53 0x43         0x01     bits     uint32 payload len   envelope bytes
//
// MAGIC  = "SC" (0x53 0x43) — cheap sync word to reject garbage / port scans.
// VER    = protocol version. Bumped only on incompatible framing changes.
// FLAGS  = bitfield (see Flag* constants): compression, etc.
// LENGTH = payload byte count, capped at MaxPayloadSize to bound allocations.
//
// The payload is an Envelope (see envelope.go). Keeping framing and envelope
// separate lets us evolve message semantics without touching the transport
// parser, and lets the fuzzer target each layer independently.
package wire

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
)

// EncodeFrame serializes a single frame carrying payload. If FlagCompressed is
// set in flags the payload is gzip-compressed before framing.
func EncodeFrame(flags byte, payload []byte) ([]byte, error) {
	if flags&FlagZstd != 0 {
		payload = zstdCompress(payload)
	} else if flags&FlagCompressed != 0 {
		c, err := gzipCompress(payload)
		if err != nil {
			return nil, err
		}
		payload = c
	}
	if len(payload) > MaxPayloadSize {
		return nil, ErrTooLarge
	}
	buf := make([]byte, HeaderSize+len(payload))
	buf[0] = Magic0
	buf[1] = Magic1
	buf[2] = Version
	buf[3] = flags
	binary.BigEndian.PutUint32(buf[4:8], uint32(len(payload)))
	copy(buf[HeaderSize:], payload)
	return buf, nil
}

// DecodeFrame parses one complete in-memory frame (used by the WebSocket path,
// where each binary message is exactly one frame). It returns the decompressed
// payload.
func DecodeFrame(b []byte) (payload []byte, err error) {
	if len(b) < HeaderSize {
		return nil, io.ErrUnexpectedEOF
	}
	if b[0] != Magic0 || b[1] != Magic1 {
		return nil, ErrBadMagic
	}
	if b[2] != Version {
		return nil, fmt.Errorf("%w: got %d want %d", ErrBadVersion, b[2], Version)
	}
	flags := b[3]
	n := binary.BigEndian.Uint32(b[4:8])
	if n > MaxPayloadSize {
		return nil, ErrTooLarge
	}
	if len(b) < HeaderSize+int(n) {
		return nil, io.ErrUnexpectedEOF
	}
	payload = b[HeaderSize : HeaderSize+int(n)]
	if flags&FlagZstd != 0 {
		return zstdDecompress(payload)
	}
	if flags&FlagCompressed != 0 {
		return gzipDecompress(payload)
	}
	// Copy so callers own the slice independent of the input buffer.
	out := make([]byte, len(payload))
	copy(out, payload)
	return out, nil
}

// WriteFrame encodes and writes a frame to w in a single Write call, reusing a
// pooled assembly buffer to avoid a per-frame allocation.
func WriteFrame(w io.Writer, flags byte, payload []byte) error {
	if flags&FlagZstd != 0 {
		payload = zstdCompress(payload)
	} else if flags&FlagCompressed != 0 {
		c, err := gzipCompress(payload)
		if err != nil {
			return err
		}
		payload = c
	}
	if len(payload) > MaxPayloadSize {
		return ErrTooLarge
	}
	bp := frameBufPool.Get().(*[]byte)
	buf := append((*bp)[:0], Magic0, Magic1, Version, flags, 0, 0, 0, 0)
	binary.BigEndian.PutUint32(buf[4:8], uint32(len(payload)))
	buf = append(buf, payload...)
	_, err := w.Write(buf)
	if cap(buf) <= poolBufCap {
		*bp = buf
		frameBufPool.Put(bp)
	}
	return err
}

// ReadFrame reads exactly one frame from a byte stream (the TCP path). It reads
// the fixed header, validates it, then reads the declared payload. The returned
// payload is decompressed if the frame's compressed flag was set.
func ReadFrame(r io.Reader) (payload []byte, err error) {
	var hdr [HeaderSize]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	if hdr[0] != Magic0 || hdr[1] != Magic1 {
		return nil, ErrBadMagic
	}
	if hdr[2] != Version {
		return nil, fmt.Errorf("%w: got %d want %d", ErrBadVersion, hdr[2], Version)
	}
	flags := hdr[3]
	n := binary.BigEndian.Uint32(hdr[4:8])
	if n > MaxPayloadSize {
		return nil, ErrTooLarge
	}
	buf := make([]byte, n)
	if _, err = io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	if flags&FlagZstd != 0 {
		return zstdDecompress(buf)
	}
	if flags&FlagCompressed != 0 {
		return gzipDecompress(buf)
	}
	return buf, nil
}

func gzipCompress(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func gzipDecompress(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	// Bound decompression output to guard against zip bombs.
	lr := io.LimitReader(zr, MaxPayloadSize+1)
	out, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if len(out) > MaxPayloadSize {
		return nil, ErrTooLarge
	}
	return out, nil
}
