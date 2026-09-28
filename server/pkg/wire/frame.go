// Package wire implements the SyncApp custom binary application protocol
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
	flags, raw, err := decodeRawFrame(b, MaxPayloadSize)
	if err != nil {
		return nil, err
	}
	return decompress(flags, raw)
}

// decodeRawFrame validates an in-memory frame and returns its flags and a COPY
// of its still-compressed payload, refusing a declared length above max.
func decodeRawFrame(b []byte, max int) (flags byte, payload []byte, err error) {
	if len(b) < HeaderSize {
		return 0, nil, io.ErrUnexpectedEOF
	}
	flags, n, err := parseHeader(b[:HeaderSize], max)
	if err != nil {
		return 0, nil, err
	}
	if len(b) < HeaderSize+int(n) {
		return 0, nil, io.ErrUnexpectedEOF
	}
	// Copy so callers own the slice independent of the input buffer.
	out := make([]byte, n)
	copy(out, b[HeaderSize:HeaderSize+int(n)])
	return flags, out, nil
}

// parseHeader validates the fixed header and returns the flags and the declared
// payload length. The length is checked against max BEFORE anything is
// allocated for it: the prefix is attacker-controlled.
func parseHeader(hdr []byte, max int) (flags byte, n uint32, err error) {
	if hdr[0] != Magic0 || hdr[1] != Magic1 {
		return 0, 0, ErrBadMagic
	}
	if hdr[2] != Version {
		return 0, 0, fmt.Errorf("%w: got %d want %d", ErrBadVersion, hdr[2], Version)
	}
	n = binary.BigEndian.Uint32(hdr[4:8])
	if max > MaxPayloadSize || max <= 0 {
		max = MaxPayloadSize
	}
	if uint64(n) > uint64(max) {
		return 0, 0, ErrTooLarge
	}
	return hdr[3], n, nil
}

// decompress undoes whatever compression the flags name. Every path is bounded
// to MaxPayloadSize of OUTPUT: the input limit alone says nothing about how far
// a compressed payload expands.
func decompress(flags byte, payload []byte) ([]byte, error) {
	if flags&FlagZstd != 0 {
		return zstdDecompress(payload)
	}
	if flags&FlagCompressed != 0 {
		return gzipDecompress(payload)
	}
	return payload, nil
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
	flags, raw, err := readRawFrame(r, MaxPayloadSize)
	if err != nil {
		return nil, err
	}
	return decompress(flags, raw)
}

// readRawFrame reads one frame from a stream and returns its flags and its
// still-compressed payload, refusing a declared length above max before reading
// (or allocating) the body.
func readRawFrame(r io.Reader, max int) (flags byte, payload []byte, err error) {
	var hdr [HeaderSize]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	flags, n, err := parseHeader(hdr[:], max)
	if err != nil {
		return 0, nil, err
	}
	buf := make([]byte, n)
	if _, err = io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	return flags, buf, nil
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
	defer func() { _ = zr.Close() }() // read-only reader; a close error cannot affect the bytes already returned
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
