package eventbus

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestNATSJetStream exercises the real bus: a durable (JetStream) subject and an
// ephemeral (core) subject both round-trip. Runs only when SYNAPSE_TEST_NATS_URL
// is set to a JetStream-enabled server (nats -js).
func TestNATSJetStream(t *testing.T) {
	url := os.Getenv("SYNAPSE_TEST_NATS_URL")
	if url == "" {
		t.Skip("set SYNAPSE_TEST_NATS_URL to run the NATS JetStream test")
	}
	bus, err := NewNATS(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer bus.Close()

	// Durable path: message.created via JetStream with ack.
	got := make(chan Event, 1)
	if err := bus.Subscribe(SubjMessageCreated, "testfanout", func(_ context.Context, e Event) error {
		got <- e
		return nil
	}); err != nil {
		t.Fatalf("subscribe durable: %v", err)
	}
	time.Sleep(200 * time.Millisecond) // let the consumer establish
	if err := bus.Publish(context.Background(), Event{
		Subject: SubjMessageCreated, Key: "chat1", Data: []byte("hello"),
		Headers: map[string]string{"traceparent": "abc"},
	}); err != nil {
		t.Fatalf("publish durable: %v", err)
	}
	select {
	case e := <-got:
		if string(e.Data) != "hello" || e.Key != "chat1" || e.Headers["traceparent"] != "abc" {
			t.Fatalf("durable event mismatch: %+v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("durable event not received")
	}

	// Ephemeral path: typing via core NATS.
	gotT := make(chan Event, 1)
	if err := bus.Subscribe(SubjTyping, "", func(_ context.Context, e Event) error {
		gotT <- e
		return nil
	}); err != nil {
		t.Fatalf("subscribe core: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	_ = bus.Publish(context.Background(), Event{Subject: SubjTyping, Data: []byte("typing")})
	select {
	case <-gotT:
	case <-time.After(3 * time.Second):
		t.Fatal("ephemeral event not received")
	}
}
