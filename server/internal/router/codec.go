package router

import (
	"encoding/binary"
	"encoding/json"
	"errors"
)

/*
The wire format for a node-targeted delivery.

This path carries EVERY delivered message in the system, which rules out
encoding/json on two counts, the second the expensive one:

  - reflection and a fresh allocation per publish, on the hottest path there is;
  - `Body []byte` in JSON is base64: +33% in size for every message body crossing
    the bus, plus an encode on the way out and a decode on the way in.

The format below is the same shape as pkg/wire's: fixed-width big-endian headers,
explicit lengths, bounds checked on the way in. It is deliberately NOT a protobuf
message — this is an internal, single-hop, same-version envelope between nodes of
one deployment, so the value protobuf adds (cross-language, schema evolution) is
value nobody here collects, and the codegen step would be real.

Layout:

	+------+--------+----------+=========+-------+========...========+--------+======+
	| 0x01 | TYPE(2)| DEVLEN(2)| DEVICE  | N (2) | (LEN(2) USER) × N  | BLEN(4)| BODY |
	+------+--------+----------+=========+-------+========...========+--------+======+

Byte 0 is a format marker and it is load-bearing for the upgrade: a JSON object
starts with '{' (0x7B), so a node running the new code can read a frame published
by a node running the old code and vice versa is a deploy-order question rather
than an outage. Decode dispatches on it.
*/

// nodeDeliveryV1 is the format marker. Chosen as 0x01 rather than something
// mnemonic precisely because it cannot collide with '{' — the only other thing
// this decoder will ever be handed.
const nodeDeliveryV1 = 0x01

var errBadNodeDelivery = errors.New("router: malformed node delivery")

// Encode serializes a NodeDelivery for the bus.
func (n NodeDelivery) Encode() []byte {
	size := 1 + 2 + 2 + len(n.DeviceID) + 2 + 4 + len(n.Body)
	for _, u := range n.Users {
		size += 2 + len(u)
	}
	b := make([]byte, 0, size)
	b = append(b, nodeDeliveryV1)
	b = binary.BigEndian.AppendUint16(b, n.Type)
	b = appendLenPrefixed(b, n.DeviceID)
	b = binary.BigEndian.AppendUint16(b, uint16(len(n.Users)))
	for _, u := range n.Users {
		b = appendLenPrefixed(b, u)
	}
	b = binary.BigEndian.AppendUint32(b, uint32(len(n.Body)))
	return append(b, n.Body...)
}

func appendLenPrefixed(b []byte, s string) []byte {
	b = binary.BigEndian.AppendUint16(b, uint16(len(s)))
	return append(b, s...)
}

// DecodeNodeDelivery parses a NodeDelivery from bus bytes.
//
// It accepts the legacy JSON form as well, so a rolling deploy does not have to
// be ordered: during the window where both versions are publishing, either node
// understands either frame. The branch can go once no node is running the old
// build.
func DecodeNodeDelivery(b []byte) (NodeDelivery, error) {
	if len(b) == 0 {
		return NodeDelivery{}, errBadNodeDelivery
	}
	if b[0] == '{' {
		return decodeLegacyJSON(b)
	}
	if b[0] != nodeDeliveryV1 {
		return NodeDelivery{}, errBadNodeDelivery
	}
	r := reader{b: b, i: 1}
	var n NodeDelivery
	n.Type = r.uint16()
	n.DeviceID = r.str()
	count := int(r.uint16())
	// Bound the allocation by what is actually left in the buffer: a forged count
	// of 65535 would otherwise reserve a slice for recipients that cannot be
	// there. Each user costs at least its 2-byte length prefix.
	if count > 0 && count*2 > r.remaining() {
		return NodeDelivery{}, errBadNodeDelivery
	}
	if count > 0 {
		n.Users = make([]string, count)
		for i := range n.Users {
			n.Users[i] = r.str()
		}
	}
	bodyLen := int(r.uint32())
	if r.err || bodyLen > r.remaining() {
		return NodeDelivery{}, errBadNodeDelivery
	}
	// Copied rather than aliased: the caller outlives the bus's buffer, and the
	// NATS client is free to reuse it once the handler returns.
	n.Body = append([]byte(nil), r.b[r.i:r.i+bodyLen]...)
	return n, nil
}

// decodeLegacyJSON reads the pre-binary form, which named exactly one recipient.
func decodeLegacyJSON(b []byte) (NodeDelivery, error) {
	var legacy struct {
		UserID   string `json:"u"`
		DeviceID string `json:"d"`
		Type     uint16 `json:"t"`
		Body     []byte `json:"b"`
	}
	if err := json.Unmarshal(b, &legacy); err != nil {
		return NodeDelivery{}, err
	}
	n := NodeDelivery{DeviceID: legacy.DeviceID, Type: legacy.Type, Body: legacy.Body}
	if legacy.UserID != "" {
		n.Users = []string{legacy.UserID}
	}
	return n, nil
}

// reader is a bounds-checked cursor. It latches an error rather than returning
// one per read: a malformed frame fails the whole decode, and threading an error
// through six reads would bury the format in error handling.
type reader struct {
	b   []byte
	i   int
	err bool
}

func (r *reader) remaining() int {
	if r.err || r.i > len(r.b) {
		return 0
	}
	return len(r.b) - r.i
}

func (r *reader) uint16() uint16 {
	if r.remaining() < 2 {
		r.err = true
		return 0
	}
	v := binary.BigEndian.Uint16(r.b[r.i:])
	r.i += 2
	return v
}

func (r *reader) uint32() uint32 {
	if r.remaining() < 4 {
		r.err = true
		return 0
	}
	v := binary.BigEndian.Uint32(r.b[r.i:])
	r.i += 4
	return v
}

func (r *reader) str() string {
	n := int(r.uint16())
	if r.err || n > r.remaining() {
		r.err = true
		return ""
	}
	s := string(r.b[r.i : r.i+n])
	r.i += n
	return s
}
