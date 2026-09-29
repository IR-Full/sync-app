package gateway_test

import (
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/IR-Full/sync-app/server/pkg/wire"
)

// rawHello is a HELLO envelope, padded when a test needs it large.
func rawHello(pad int) []byte {
	return (&wire.Envelope{Type: wire.MsgHello, Body: wire.Marshal(wire.HelloBody{
		ClientVersion: "test" + strings.Repeat("x", pad), Platform: "cli",
	})}).Encode()
}

// expectHangUp reads until the server closes the socket. Seeing `forbidden`
// first, or the socket still open after the deadline, fails the test.
func expectHangUp(t *testing.T, c *wire.Conn, forbidden wire.MsgType) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		e, err := c.ReadEnvelope()
		if err == nil {
			if e.Type == forbidden {
				t.Fatalf("server answered with %s instead of hanging up", e.Type)
			}
			continue
		}
		if errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatal("server kept the connection open")
		}
		return // EOF / reset: hung up, as intended
	}
}

func dialRaw(t *testing.T, addr string) (net.Conn, *wire.Conn) {
	t.Helper()
	nc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nc.Close() })
	return nc, wire.NewConn(wire.NewTCPTransport(nc), false)
}

// The compression flag is chosen by whoever sends the frame. Before the
// handshake nothing has been negotiated, so a compressed first frame is a
// stranger asking us to run a decompressor — and a zstd one could be a bomb.
func TestCompressedFrameBeforeHandshakeIsRefused(t *testing.T) {
	addr := startGateway(t)
	for _, flag := range []byte{wire.FlagZstd, wire.FlagCompressed} {
		nc, c := dialRaw(t, addr)
		frame, err := wire.EncodeFrame(flag, rawHello(0))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := nc.Write(frame); err != nil {
			t.Fatal(err)
		}
		expectHangUp(t, c, wire.MsgWelcome)
	}
}

func TestOversizeFrameBeforeAuthIsRefused(t *testing.T) {
	addr := startGateway(t)
	nc, c := dialRaw(t, addr)
	frame, err := wire.EncodeFrame(0, rawHello(100<<10)) // past the 64 KiB pre-auth cap
	if err != nil {
		t.Fatal(err)
	}
	// The server may hang up before reading all of it; a failed write is fine.
	_, _ = nc.Write(frame)
	expectHangUp(t, c, wire.MsgWelcome)
}

// After authentication the peer may use exactly what it negotiated: gzip works
// for a client that offered CapCompression (Android does)…
func TestNegotiatedCompressionWorksAfterAuth(t *testing.T) {
	addr := startGateway(t)
	connect(t, addr, "wp-gzip", "secret123")
	cl := loginWithDeviceAndCaps(t, addr, "wp-gzip", "secret123", "dev-gzip", wire.CapCompression)
	cl.conn.SetCompression(true)
	cl.send(t, wire.MsgSend, 7, wire.SendBody{
		ChatID: "@wp-gzip", DedupKey: "g1", Text: strings.Repeat("long enough to compress ", 40),
	})
	cl.readUntil(t, wire.MsgSendAck)
}

// …and is refused for a client that never offered it.
func TestUnnegotiatedCompressionAfterAuthIsRefused(t *testing.T) {
	addr := startGateway(t)
	cl := connect(t, addr, "wp-plain", "secret123") // advertises no caps
	cl.conn.SetCompression(true)
	cl.send(t, wire.MsgSend, 7, wire.SendBody{
		ChatID: "@wp-plain", DedupKey: "p1", Text: strings.Repeat("long enough to compress ", 40),
	})
	expectHangUp(t, cl.conn, wire.MsgSendAck)
}
