package wire

import (
	"bytes"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// pair wires two Conns together over an in-memory socket, which exercises the
// real framing path (deadlines, partial reads, close semantics) without a port.
func pair(t *testing.T, compress bool) (client, server *Conn) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	return NewConn(NewTCPTransport(a), compress), NewConn(NewTCPTransport(b), compress)
}

// send writes from one side while the other reads, because net.Pipe is
// unbuffered: a write blocks until someone reads it.
func exchange(t *testing.T, from, to *Conn, e *Envelope) Envelope {
	t.Helper()
	var got Envelope
	var readErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		got, readErr = to.ReadEnvelope()
	}()
	if err := from.WriteEnvelope(e); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("read timed out")
	}
	if readErr != nil {
		t.Fatalf("read: %v", readErr)
	}
	return got
}

func TestConnRoundTripsAnEnvelope(t *testing.T) {
	client, server := pair(t, false)
	sent := &Envelope{Type: MsgSend, Seq: 7, Ack: 3, RequestID: 42, Body: []byte("hello")}

	got := exchange(t, client, server, sent)

	if got.Type != MsgSend || got.Seq != 7 || got.Ack != 3 || got.RequestID != 42 {
		t.Fatalf("header changed in transit: %+v", got)
	}
	if !bytes.Equal(got.Body, []byte("hello")) {
		t.Fatalf("body changed in transit: %q", got.Body)
	}
}

func TestConnRoundTripsAnEmptyBody(t *testing.T) {
	client, server := pair(t, false)
	// A protobuf message whose fields are all zero encodes to no bytes at all, so
	// "empty" has to survive the trip as a legitimate answer rather than an error.
	got := exchange(t, client, server, &Envelope{Type: MsgPong, Seq: 1})
	if got.Type != MsgPong || len(got.Body) != 0 {
		t.Fatalf("empty envelope did not survive: %+v", got)
	}
}

// Compression is negotiated, and only pays for itself above a size threshold —
// so a small frame must stay uncompressed while a large one is compressed, and
// both must decode identically.
func TestConnCompressesOnlyLargeFrames(t *testing.T) {
	client, server := pair(t, true)

	small := &Envelope{Type: MsgSend, Seq: 1, Body: []byte("tiny")}
	if got := exchange(t, client, server, small); !bytes.Equal(got.Body, small.Body) {
		t.Fatalf("small frame corrupted: %q", got.Body)
	}

	big := &Envelope{Type: MsgSend, Seq: 2, Body: []byte(strings.Repeat("compress me ", 200))}
	got := exchange(t, client, server, big)
	if !bytes.Equal(got.Body, big.Body) {
		t.Fatalf("compressed frame did not round trip (%d vs %d bytes)", len(got.Body), len(big.Body))
	}
}

func TestConnZstdRoundTrip(t *testing.T) {
	client, server := pair(t, false)
	client.SetZstd(true)

	big := &Envelope{Type: MsgSend, Seq: 1, Body: []byte(strings.Repeat("zstd payload ", 200))}
	got := exchange(t, client, server, big)
	if !bytes.Equal(got.Body, big.Body) {
		t.Fatal("zstd frame did not round trip")
	}
}

// Compression is decided per frame and advertised in the flags, so a compressing
// sender and a non-compressing one can share a connection in either direction.
func TestCompressionIsPerFrameNotPerConnection(t *testing.T) {
	client, server := pair(t, false)
	client.SetCompression(true)

	payload := []byte(strings.Repeat("asymmetric ", 200))
	got := exchange(t, client, server, &Envelope{Type: MsgSend, Seq: 1, Body: payload})
	if !bytes.Equal(got.Body, payload) {
		t.Fatal("compressed→uncompressed direction failed")
	}
	back := exchange(t, server, client, &Envelope{Type: MsgNew, Seq: 1, Body: payload})
	if !bytes.Equal(back.Body, payload) {
		t.Fatal("uncompressed→compressed direction failed")
	}
}

func TestSendMarshalsATypedBody(t *testing.T) {
	client, server := pair(t, false)

	var got Envelope
	done := make(chan struct{})
	go func() {
		defer close(done)
		got, _ = server.ReadEnvelope()
	}()
	if err := client.Send(MsgSend, 5, 2, 9, SendBody{ChatID: "c1", Text: "typed"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	<-done

	if got.Type != MsgSend || got.Seq != 5 || got.RequestID != 9 {
		t.Fatalf("header: %+v", got)
	}
	var body SendBody
	if err := Unmarshal(got.Body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Text != "typed" || body.ChatID != "c1" {
		t.Fatalf("body: %+v", body)
	}
}

func TestSendAcceptsRawBytes(t *testing.T) {
	client, server := pair(t, false)
	// Pre-encoded bytes pass through untouched — that is what lets fanout forward
	// a body it never decoded.
	raw := []byte{0x01, 0x02, 0x03}
	var got Envelope
	done := make(chan struct{})
	go func() { defer close(done); got, _ = server.ReadEnvelope() }()
	if err := client.Send(MsgNew, 1, 0, 0, raw); err != nil {
		t.Fatalf("Send: %v", err)
	}
	<-done
	if !bytes.Equal(got.Body, raw) {
		t.Fatalf("raw body altered: %v", got.Body)
	}
}

func TestSendWithNilBody(t *testing.T) {
	client, server := pair(t, false)
	var got Envelope
	done := make(chan struct{})
	go func() { defer close(done); got, _ = server.ReadEnvelope() }()
	if err := client.Send(MsgPing, 1, 0, 0, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	<-done
	if got.Type != MsgPing || len(got.Body) != 0 {
		t.Fatalf("nil body produced %+v", got)
	}
}

// WriteRaw replays a buffered envelope verbatim — the resume path depends on the
// bytes being identical, because the client is re-reading frames it half saw.
func TestWriteRawReplaysVerbatim(t *testing.T) {
	client, server := pair(t, false)
	original := Envelope{Type: MsgNew, Seq: 11, RequestID: 0, Body: []byte("replayed")}
	payload := original.Encode()

	var got Envelope
	done := make(chan struct{})
	go func() { defer close(done); got, _ = server.ReadEnvelope() }()
	if err := client.WriteRaw(payload); err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}
	<-done

	if got.Type != original.Type || got.Seq != original.Seq {
		t.Fatalf("replayed frame changed: %+v", got)
	}
	if !bytes.Equal(got.Body, original.Body) {
		t.Fatalf("replayed body changed: %q", got.Body)
	}
}

func TestWriteRawCompressesLargePayloads(t *testing.T) {
	client, server := pair(t, true)
	big := Envelope{Type: MsgNew, Seq: 1, Body: []byte(strings.Repeat("x", 2000))}

	var got Envelope
	done := make(chan struct{})
	go func() { defer close(done); got, _ = server.ReadEnvelope() }()
	if err := client.WriteRaw(big.Encode()); err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}
	<-done
	if len(got.Body) != 2000 {
		t.Fatalf("replayed body is %d bytes, want 2000", len(got.Body))
	}
}

func TestEncodeBody(t *testing.T) {
	if EncodeBody(nil) != nil {
		t.Fatal("nil body did not encode to nil")
	}
	raw := []byte{9, 8, 7}
	if got := EncodeBody(raw); !bytes.Equal(got, raw) {
		t.Fatalf("raw bytes altered: %v", got)
	}
	encoded := EncodeBody(SendBody{ChatID: "c1", Text: "hi"})
	var back SendBody
	if err := Unmarshal(encoded, &back); err != nil || back.Text != "hi" {
		t.Fatalf("typed body did not round trip: %v / %+v", err, back)
	}
}

func TestReadEnvelopeReportsTransportErrors(t *testing.T) {
	a, b := net.Pipe()
	client := NewConn(NewTCPTransport(a), false)
	_ = b.Close()
	_ = a.Close()

	if _, err := client.ReadEnvelope(); err == nil {
		t.Fatal("read on a closed connection returned no error")
	}
}

func TestDeadlinesAreHonoured(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	client := NewConn(NewTCPTransport(a), false)

	// The idle deadline is what reclaims a connection whose peer stopped talking
	// without closing — the slow-loris case.
	if err := client.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	start := time.Now()
	_, err := client.ReadEnvelope()
	if err == nil {
		t.Fatal("read past its deadline returned no error")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("read deadline was not enforced")
	}

	if err := client.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetWriteDeadline: %v", err)
	}
}

func TestCloseIsIdempotentOnTheTransport(t *testing.T) {
	a, b := net.Pipe()
	_ = b.Close()
	client := NewConn(NewTCPTransport(a), false)
	if err := client.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	// A second close must not panic; the gateway closes on several paths.
	_ = client.Close()
}

// The writer is guarded because a connection has one write loop but several
// producers can reach it during teardown.
func TestConcurrentWritesAreSerialised(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	client := NewConn(NewTCPTransport(a), false)
	server := NewConn(NewTCPTransport(b), false)

	const n = 20
	go func() {
		for i := 0; i < n; i++ {
			_, _ = server.ReadEnvelope()
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = client.Send(MsgTyping, uint64(i), 0, 0, TypingBody{ChatID: "c", UserID: "u", Active: true})
		}(i)
	}
	wg.Wait() // the assertion is the race detector staying quiet
}

func TestStreamTransportReadWrite(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	client := NewStreamTransport(a)
	server := NewStreamTransport(b)

	var got []byte
	var readErr error
	done := make(chan struct{})
	go func() { defer close(done); got, readErr = server.ReadFrame() }()

	if err := client.WriteFrame(0, []byte("frame")); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	<-done
	if readErr != nil {
		t.Fatalf("ReadFrame: %v", readErr)
	}
	if !bytes.Equal(got, []byte("frame")) {
		t.Fatalf("frame altered: %q", got)
	}

	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if err := client.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetWriteDeadline: %v", err)
	}
	if err := client.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Close: %v", err)
	}
}
