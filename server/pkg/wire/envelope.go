package wire

import (
	"encoding/binary"
	"errors"
)

// ErrShortEnvelope is returned when the byte slice is truncated.
var ErrShortEnvelope = errors.New("wire: short envelope")

// Envelope is the payload of a frame. It carries the routing/correlation header
// plus an opaque body. The header is a compact varint-packed structure (not
// JSON) so it stays small and cheap to parse on the hot path; the body is
// type-specific and, in this MVP, JSON-encoded (see messages.go). Production
// would switch bodies to protobuf without touching this header.
//
// Header field order on the wire (all LEB128 unsigned varints):
//
//	Type      — MsgType
//	Seq       — per-connection monotonic sender sequence (0 for stateless ctrl)
//	Ack       — highest contiguous Seq the sender has processed (piggybacked)
//	RequestID — correlation id linking a reply to its request (0 = none)
//	len(Body) — body length
//	Body      — raw bytes
//
// Seq/Ack give us in-order, gap-detecting, resumable delivery per connection.
// RequestID gives request/response correlation independent of ordering, so many
// requests can be in flight (multiplexing) over one connection.
type Envelope struct {
	Type      MsgType
	Seq       uint64
	Ack       uint64
	RequestID uint64
	Body      []byte
}

// Encode serializes the envelope into a new byte slice.
func (e *Envelope) Encode() []byte {
	buf := make([]byte, 0, 16+len(e.Body))
	buf = binary.AppendUvarint(buf, uint64(e.Type))
	buf = binary.AppendUvarint(buf, e.Seq)
	buf = binary.AppendUvarint(buf, e.Ack)
	buf = binary.AppendUvarint(buf, e.RequestID)
	buf = binary.AppendUvarint(buf, uint64(len(e.Body)))
	buf = append(buf, e.Body...)
	return buf
}

// DecodeEnvelope parses an envelope from b.
func DecodeEnvelope(b []byte) (Envelope, error) {
	var e Envelope
	off := 0

	read := func() (uint64, bool) {
		v, n := binary.Uvarint(b[off:])
		if n <= 0 {
			return 0, false
		}
		off += n
		return v, true
	}

	t, ok := read()
	if !ok {
		return e, ErrShortEnvelope
	}
	e.Type = MsgType(t)
	if e.Seq, ok = read(); !ok {
		return e, ErrShortEnvelope
	}
	if e.Ack, ok = read(); !ok {
		return e, ErrShortEnvelope
	}
	if e.RequestID, ok = read(); !ok {
		return e, ErrShortEnvelope
	}
	blen, ok := read()
	if !ok {
		return e, ErrShortEnvelope
	}
	if uint64(len(b)-off) < blen {
		return e, ErrShortEnvelope
	}
	if blen > 0 {
		e.Body = make([]byte, blen)
		copy(e.Body, b[off:off+int(blen)])
	}
	return e, nil
}
