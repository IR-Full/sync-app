package wire

import (
	"bytes"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	cases := [][]byte{
		nil,
		[]byte("hello"),
		bytes.Repeat([]byte("x"), 10000),
	}
	for _, payload := range cases {
		var buf bytes.Buffer
		if err := WriteFrame(&buf, 0, payload); err != nil {
			t.Fatalf("write: %v", err)
		}
		got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("round trip mismatch: got %d bytes want %d", len(got), len(payload))
		}
	}
}

func TestFrameCompression(t *testing.T) {
	payload := bytes.Repeat([]byte("compress me "), 500)
	enc, err := EncodeFrame(FlagCompressed, payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(enc) >= len(payload) {
		t.Fatalf("expected compression to shrink payload: %d >= %d", len(enc), len(payload))
	}
	got, err := DecodeFrame(enc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("compressed round trip mismatch")
	}
}

func TestZstdFrameRoundTrip(t *testing.T) {
	// Repetitive chat-like payload — the dictionary should shrink it well.
	payload := []byte(`{"chat_id":"123","sender_id":"456","text":"hi hey hello thanks ok"}`)
	enc, err := EncodeFrame(FlagZstd, payload)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeFrame(enc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("zstd round trip mismatch")
	}
	// Compressed frame of a repetitive body should not be larger than the input.
	big := bytes.Repeat(payload, 20)
	encBig, _ := EncodeFrame(FlagZstd, big)
	if len(encBig) >= len(big) {
		t.Fatalf("zstd did not shrink repetitive payload: %d >= %d", len(encBig), len(big))
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	e := Envelope{
		Type:      MsgSend,
		Seq:       42,
		Ack:       17,
		RequestID: 9001,
		Body:      Marshal(SendBody{ChatID: "c1", DedupKey: "k1", Text: "hi"}),
	}
	got, err := DecodeEnvelope(e.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != e.Type || got.Seq != e.Seq || got.Ack != e.Ack || got.RequestID != e.RequestID {
		t.Fatalf("header mismatch: %+v vs %+v", got, e)
	}
	var sb SendBody
	if err := Unmarshal(got.Body, &sb); err != nil {
		t.Fatal(err)
	}
	if sb.Text != "hi" || sb.ChatID != "c1" {
		t.Fatalf("body mismatch: %+v", sb)
	}
}

func TestDecodeFrameRejectsGarbage(t *testing.T) {
	if _, err := DecodeFrame([]byte{0x00, 0x00, 0x00}); err == nil {
		t.Fatal("expected error on garbage")
	}
	if _, err := DecodeFrame([]byte{Magic0, Magic1, 0xFF, 0, 0, 0, 0, 0}); err == nil {
		t.Fatal("expected version error")
	}
}

// FuzzParser hardens the two parsers (Section 16 requirement). It must never
// panic regardless of input; malformed data must return an error, not crash.
func FuzzParser(f *testing.F) {
	f.Add([]byte{Magic0, Magic1, Version, 0, 0, 0, 0, 3, 'a', 'b', 'c'})
	f.Add([]byte("random garbage that is not a frame"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if payload, err := DecodeFrame(data); err == nil {
			// If it decoded as a frame, the payload must also parse or error
			// cleanly as an envelope — never panic.
			_, _ = DecodeEnvelope(payload)
		}
		_, _ = DecodeEnvelope(data)
	})
}

// discardWriter is an io.Writer that ignores all writes, for allocation
// benchmarks of the frame write path (no syscall noise).
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// BenchmarkWriteFrame guards the pooled frame-assembly buffer: an uncompressed
// small frame (the hot path — acks, receipts, fanout pushes) should assemble and
// write with zero heap allocations, so per-frame GC pressure stays flat under load.
func BenchmarkWriteFrame(b *testing.B) {
	payload := []byte("a typical small envelope payload ~48 bytes long!!")
	w := discardWriter{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := WriteFrame(w, 0, payload); err != nil {
			b.Fatal(err)
		}
	}
}
