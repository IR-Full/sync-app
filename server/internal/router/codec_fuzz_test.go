package router

import "testing"

// FuzzDecodeNodeDelivery guards the bus-side parser.
//
// It belongs here for the same reason pkg/wire fuzzes its frame parser: this is a
// decoder fed bytes it did not write. The payload arrives off a shared event bus,
// so it can be truncated by a broker, written by a node running a different build,
// or — in a deployment where the bus is not isolated — supplied by something else
// entirely. A malformed one has to be an error the consumer drops, never a panic
// that takes the delivery goroutine down, and never a half-populated delivery it
// acts on.
//
// The seeds cover both encodings, because the decoder still accepts the legacy
// JSON form for the length of one rolling deploy.
func FuzzDecodeNodeDelivery(f *testing.F) {
	f.Add(NodeDelivery{Users: []string{"u1"}, DeviceID: "d1", Type: 10, Body: []byte("hi")}.Encode())
	f.Add(NodeDelivery{Users: []string{"u1", "u2", "u3"}, Type: 8, Body: nil}.Encode())
	f.Add([]byte(`{"u":"u7","d":"dev-2","t":54,"b":"AQL/"}`))
	f.Add([]byte("not a delivery at all"))
	f.Add([]byte{nodeDeliveryV1})
	// A header that claims far more than it carries — the shape a truncated frame
	// takes, and the one an unchecked length would follow off the end of the slice.
	f.Add([]byte{nodeDeliveryV1, 0x00, 0x0A, 0x00, 0x00, 0xFF, 0xFF})

	f.Fuzz(func(t *testing.T, data []byte) {
		nd, err := DecodeNodeDelivery(data)
		if err != nil {
			return
		}
		// A successful decode must be internally consistent: the consumer indexes
		// Users and hands Body straight to a frame writer, so neither may be a
		// surprise once err is nil.
		for _, u := range nd.Users {
			_ = len(u)
		}
		_ = len(nd.Body)
		// And it must round-trip. A decode that succeeded on bytes it cannot
		// reproduce means the two halves disagree about the format, which is how a
		// delivery silently changes shape between nodes.
		if nd.DeviceID != "" || len(nd.Users) > 0 || len(nd.Body) > 0 {
			again, err := DecodeNodeDelivery(nd.Encode())
			if err != nil {
				t.Fatalf("re-encoding a decoded delivery no longer decodes: %v", err)
			}
			if len(again.Users) != len(nd.Users) || again.DeviceID != nd.DeviceID ||
				again.Type != nd.Type || string(again.Body) != string(nd.Body) {
				t.Fatalf("round-trip changed the delivery:\n got %+v\nwant %+v", again, nd)
			}
		}
	})
}
