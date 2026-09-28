package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// capture returns a sink writing JSON records into buf, so the test can assert
// on the fields a log pipeline would route on rather than on formatting.
func capture() (*LogSink, *bytes.Buffer) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return NewLogSink(log), &buf
}

func decode(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("nothing was recorded")
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("record is not valid JSON (%v): %s", err, line)
	}
	return got
}

func TestRecordEmitsEveryField(t *testing.T) {
	sink, buf := capture()
	sink.Record(context.Background(), Event{
		At: 1700000000000, Action: "chat.export", Actor: "42", Target: "99", Detail: "exported",
	})

	got := decode(t, buf)
	for field, want := range map[string]any{
		"action": "chat.export",
		"actor":  "42",
		"target": "99",
		"detail": "exported",
	} {
		if got[field] != want {
			t.Fatalf("%s = %v, want %v", field, got[field], want)
		}
	}
	if got["at"] != float64(1700000000000) {
		t.Fatalf("at = %v, want the supplied timestamp", got["at"])
	}
}

// The `audit` marker is what lets the log pipeline split this stream off to a
// WORM store; without it an audit record is indistinguishable from an info line.
func TestRecordIsTaggedForRouting(t *testing.T) {
	sink, buf := capture()
	sink.Record(context.Background(), Event{Action: "login", Actor: "1"})

	got := decode(t, buf)
	if got["audit"] != true {
		t.Fatalf("record not tagged for the audit channel: %v", got)
	}
	if got["msg"] != "AUDIT" {
		t.Fatalf("msg = %v, want AUDIT", got["msg"])
	}
}

// A record with no timestamp still has to be orderable, so the sink stamps one
// rather than writing a zero that would sort before every real event.
func TestRecordStampsAMissingTimestamp(t *testing.T) {
	sink, buf := capture()
	before := time.Now().UnixMilli()
	sink.Record(context.Background(), Event{Action: "login", Actor: "1"})
	after := time.Now().UnixMilli()

	got := decode(t, buf)
	at, ok := got["at"].(float64)
	if !ok {
		t.Fatalf("at is %T, want a number", got["at"])
	}
	if int64(at) < before || int64(at) > after {
		t.Fatalf("stamped at %d, outside the [%d, %d] window", int64(at), before, after)
	}
}

func TestRecordAppendsRatherThanReplaces(t *testing.T) {
	sink, buf := capture()
	// Append-only is the defining property: a later event must never overwrite an
	// earlier one.
	sink.Record(context.Background(), Event{Action: "login", Actor: "1"})
	sink.Record(context.Background(), Event{Action: "logout", Actor: "1"})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d records, want 2", len(lines))
	}
	if !strings.Contains(lines[0], "login") || !strings.Contains(lines[1], "logout") {
		t.Fatalf("records out of order or altered: %v", lines)
	}
}

func TestLogSinkSatisfiesTheSinkInterface(t *testing.T) {
	// The interface is what a DB-backed compliance sink will implement later, so
	// the default must keep satisfying it.
	var _ Sink = NewLogSink(slog.Default())
}

func TestRecordHandlesEmptyFields(t *testing.T) {
	// A permission denial may have no target and no detail; that must produce a
	// record, not a panic or a dropped event.
	sink, buf := capture()
	sink.Record(context.Background(), Event{Action: "access.denied"})

	got := decode(t, buf)
	if got["action"] != "access.denied" {
		t.Fatalf("action lost: %v", got)
	}
}
