package wire

import (
	"encoding/binary"
)

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
