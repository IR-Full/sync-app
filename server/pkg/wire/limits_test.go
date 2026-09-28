package wire

import (
	"bytes"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/klauspost/compress/zstd"
)

// zstdBomb compresses `size` zero bytes without ever holding them: the input is
// streamed through the encoder one small buffer at a time.
func zstdBomb(t *testing.T, size int) []byte {
	t.Helper()
	var out bytes.Buffer
	enc, err := zstd.NewWriter(&out, zstd.WithEncoderLevel(zstd.SpeedFastest))
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	for written := 0; written < size; written += len(chunk) {
		if _, err := enc.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func frameOf(flags byte, payload []byte) []byte {
	f := make([]byte, HeaderSize+len(payload))
	f[0], f[1], f[2], f[3] = Magic0, Magic1, Version, flags
	n := len(payload)
	f[4], f[5], f[6], f[7] = byte(n>>24), byte(n>>16), byte(n>>8), byte(n)
	copy(f[HeaderSize:], payload)
	return f
}

// A tiny zstd frame that expands far past MaxPayloadSize must be refused while
// decoding, not after: DecodeAll used to materialise the whole output first, so
// a 112 KB frame cost 1 GiB before the size check ran.
func TestZstdBombIsRefusedWithoutMaterialising(t *testing.T) {
	bomb := zstdBomb(t, 256<<20) // 16x the payload limit
	frame := frameOf(FlagZstd, bomb)

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := ReadFrame(bytes.NewReader(frame))
	runtime.ReadMemStats(&after)

	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	// Decoding may legitimately fill up to the limit (plus the decoder's window
	// and slice growth — about 6x MaxPayloadSize in total, the same for a 256 MiB
	// bomb as for a 1 GiB one) before it notices. What it must not do is keep
	// going: before the fix this allocated the full 256 MiB.
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 8*MaxPayloadSize {
		t.Fatalf("decoding a %d-byte bomb allocated %d MiB", len(bomb), grew>>20)
	}
}

func TestZstdAtTheLimitStillDecodes(t *testing.T) {
	payload := bytes.Repeat([]byte("hello "), 1000)
	got, err := zstdDecompress(zstdCompress(payload))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("round trip failed: err=%v equal=%v", err, bytes.Equal(got, payload))
	}
}

// pipeConn returns a Conn reading from one end of an in-memory pipe and the
// other end to write raw frames into.
func pipeConn(t *testing.T) (*Conn, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	return NewConn(NewStreamTransport(a), false), b
}

func writeAsync(w net.Conn, frame []byte) {
	go func() { _, _ = w.Write(frame) }()
}

func TestInboundPolicyRefusesUnnegotiatedCompression(t *testing.T) {
	env := (&Envelope{Type: MsgHello, Body: bytes.Repeat([]byte("x"), 512)}).Encode()
	for _, tc := range []struct {
		name    string
		flags   byte
		payload []byte
		allowed byte
		ok      bool
	}{
		{"zstd before negotiation", FlagZstd, zstdCompress(env), 0, false},
		{"gzip before negotiation", FlagCompressed, mustGzip(t, env), 0, false},
		{"zstd when only gzip was agreed", FlagZstd, zstdCompress(env), FlagCompressed, false},
		{"gzip when agreed", FlagCompressed, mustGzip(t, env), FlagCompressed, true},
		{"zstd when agreed", FlagZstd, zstdCompress(env), FlagZstd, true},
		{"plain is always fine", 0, env, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, w := pipeConn(t)
			c.SetInboundPolicy(64<<10, tc.allowed)
			writeAsync(w, frameOf(tc.flags, tc.payload))
			e, err := c.ReadEnvelope()
			if tc.ok {
				if err != nil || e.Type != MsgHello {
					t.Fatalf("want HELLO, got %v / %v", e.Type, err)
				}
				return
			}
			if !errors.Is(err, ErrCompressionNotNegotiated) {
				t.Fatalf("err = %v, want ErrCompressionNotNegotiated", err)
			}
		})
	}
}

// The declared length is refused before the body is read: the writer below
// sends only the header, so a reader that trusted the prefix would block.
func TestInboundPolicyRefusesOversizeBeforeReadingIt(t *testing.T) {
	c, w := pipeConn(t)
	c.SetInboundPolicy(1024, 0)
	hdr := frameOf(0, nil)
	hdr[4], hdr[5], hdr[6], hdr[7] = 0, 0, 0x10, 0 // declares 4096 bytes
	writeAsync(w, hdr)
	if _, err := c.ReadEnvelope(); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

// Without a policy a Conn behaves exactly as before, which is what clients and
// tools built on this package rely on.
func TestUnrestrictedConnStillDecompresses(t *testing.T) {
	c, w := pipeConn(t)
	env := (&Envelope{Type: MsgPing}).Encode()
	writeAsync(w, frameOf(FlagZstd, zstdCompress(env)))
	if e, err := c.ReadEnvelope(); err != nil || e.Type != MsgPing {
		t.Fatalf("got %v / %v", e.Type, err)
	}
}

// WriteRaw carries every post-handshake frame on the gateway, so it must use
// the negotiated algorithm; it used to know only gzip.
func TestWriteRawUsesNegotiatedZstd(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	c := NewConn(NewStreamTransport(a), false)
	c.SetZstd(true)
	payload := (&Envelope{Type: MsgNew, Body: bytes.Repeat([]byte("hello "), 200)}).Encode()
	go func() { _ = c.WriteRaw(payload) }()

	flags, raw, err := readRawFrame(b, MaxPayloadSize)
	if err != nil {
		t.Fatal(err)
	}
	if flags&FlagZstd == 0 {
		t.Fatalf("flags = %08b, want FlagZstd", flags)
	}
	if got, err := decompress(flags, raw); err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("round trip failed: %v", err)
	}
}

func TestWebSocketReadLimit(t *testing.T) {
	up := websocket.Upgrader{}
	got := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			got <- err
			return
		}
		c := NewConn(NewWSTransport(ws), false)
		c.SetInboundPolicy(1024, 0)
		_, err = c.ReadEnvelope()
		got <- err
	}))
	defer srv.Close()

	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	big := frameOf(0, make([]byte, 8<<10))
	_ = ws.WriteMessage(websocket.BinaryMessage, big)

	select {
	case err := <-got:
		if err == nil {
			t.Fatal("an 8 KiB message was accepted under a 1 KiB limit")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server never returned from ReadEnvelope")
	}
}

func mustGzip(t *testing.T, b []byte) []byte {
	t.Helper()
	out, err := gzipCompress(b)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
