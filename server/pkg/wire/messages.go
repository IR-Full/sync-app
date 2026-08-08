package wire

import "encoding/json"

func (jsonCodec) Marshal(v any) ([]byte, error)   { return json.Marshal(v) }
func (jsonCodec) Unmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// SetBodyCodec swaps the body encoding (e.g. to protobuf). Call once at startup
// before any connection is served.
func SetBodyCodec(c BodyCodec) { bodyCodec = c }

// Marshal encodes a body with the active codec (panic-free).
func Marshal(v any) []byte {
	b, _ := bodyCodec.Marshal(v)
	return b
}

// Unmarshal decodes a body with the active codec.
func Unmarshal(b []byte, v any) error {
	return bodyCodec.Unmarshal(b, v)
}
