package wire

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
