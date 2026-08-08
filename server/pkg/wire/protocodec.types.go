package wire

// protoCodec is the default BodyCodec: it encodes envelope bodies as protobuf.
// The public API stays the hand-written wire.*Body structs (so call sites are
// unchanged); this codec converts each struct to its generated protobuf twin and
// back. Framing and envelope are untouched — only body BYTES change from JSON to
// protobuf. Switch back to JSON with SetBodyCodec(JSONCodec{}) if ever needed.
type protoCodec struct{}

// JSONCodec exposes the legacy JSON body encoding (useful for debugging / tools).
type JSONCodec = jsonCodec
