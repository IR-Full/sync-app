package gateway

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/synapse-chat/synapse/internal/auth"
	"github.com/synapse-chat/synapse/internal/chat"
	"github.com/synapse-chat/synapse/internal/delivery"
	"github.com/synapse-chat/synapse/internal/message"
	"github.com/synapse-chat/synapse/internal/presence"
	"github.com/synapse-chat/synapse/internal/router"
	"github.com/synapse-chat/synapse/internal/store/memory"
	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/id"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// scriptedTransport feeds a connection a fixed sequence of inbound frames and
// reports every outbound one to a hook — synchronously, on the connection's own
// goroutine.
//
// That synchrony is the whole point. The property under test is an ORDERING
// between two things the connection does one after another (register for
// delivery, then answer AUTH_OK), and observing it from outside over a socket
// can only ever sample it: the window is microseconds wide and a sampling test
// passes with the bug present, which is worse than having no test. Watching the
// write itself turns the question into a deterministic one.
type scriptedTransport struct {
	mu      sync.Mutex
	inbound [][]byte
	next    int
	onWrite func(e wire.Envelope)
	drained chan struct{}
	once    sync.Once
}

func (s *scriptedTransport) ReadFrame() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next >= len(s.inbound) {
		s.once.Do(func() { close(s.drained) })
		return nil, io.EOF // script exhausted: the peer "hung up"
	}
	f := s.inbound[s.next]
	s.next++
	return f, nil
}

func (s *scriptedTransport) WriteFrame(_ byte, payload []byte) error {
	e, err := wire.DecodeEnvelope(payload)
	if err != nil {
		return err
	}
	if s.onWrite != nil {
		s.onWrite(e)
	}
	return nil
}

func (s *scriptedTransport) SetReadDeadline(time.Time) error  { return nil }
func (s *scriptedTransport) SetWriteDeadline(time.Time) error { return nil }
func (s *scriptedTransport) Close() error                     { return nil }

func frame(t *testing.T, typ wire.MsgType, seq, reqID uint64, body any) []byte {
	t.Helper()
	e := wire.Envelope{Type: typ, Seq: seq, RequestID: reqID}
	if body != nil {
		e.Body = wire.Marshal(body)
	}
	return e.Encode()
}

// TestConnectionIsRoutableBeforeAuthOK pins the ordering that makes AUTH_OK mean
// what every client assumes it means.
//
// A client that has AUTH_OK believes it is connected, and its peers may be told
// so immediately. If the server registered the connection for delivery AFTER
// sending that reply, anything addressed into the gap would find no route. A
// chat message survives it — history backfills — but the E2E relay is
// fire-and-forget by design, so a ciphertext dropped there is gone for good and
// the sender never learns it. That is not a data race the detector can find; it
// is an ordering the code has to get right.
func TestConnectionIsRoutableBeforeAuthOK(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ids, _ := id.NewGenerator(1)
	st := memory.New().Stores()
	bus := eventbus.NewMemory()
	hub := delivery.NewHub()
	rtr := router.NewMemory()

	authSvc := auth.New(st.Users, st.Sessions, ids)
	chatSvc := chat.New(st.Chats, ids)
	msgSvc := message.New(st.Messages, st.Reads, chatSvc, bus, ids)

	// Create the account up front so the test knows the user id it must look for
	// at the instant of the reply.
	ctx := context.Background()
	_, user, err := authSvc.Register(ctx, "orderly", "secret123", "", "", "cli")
	if err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	cfg.Heartbeat = time.Hour
	cfg.NodeID = "1"
	gw := New(Services{
		Auth: authSvc, Chat: chatSvc, Msg: msgSvc, Broker: message.NewBroker(msgSvc, log),
		Presence: presence.New(presence.NewMemoryBackend(), bus, time.Minute),
		Users:    st.Users, Hub: hub, Bus: bus, Router: rtr,
	}, cfg, log)

	var (
		mu             sync.Mutex
		sawAuthOK      bool
		onlineAtReply  bool
		routableAtRepl bool
	)
	tr := &scriptedTransport{
		drained: make(chan struct{}),
		inbound: [][]byte{
			frame(t, wire.MsgHello, 1, 1, wire.HelloBody{ClientVersion: "test", Platform: "cli"}),
			frame(t, wire.MsgAuth, 2, 2, wire.AuthBody{Username: "orderly", Password: "secret123"}),
		},
	}
	tr.onWrite = func(e wire.Envelope) {
		if e.Type != wire.MsgAuthOK {
			return
		}
		// Observed ON the connection's goroutine, between the handler deciding to
		// reply and the bytes leaving: whatever is true here is what a client that
		// has AUTH_OK can rely on.
		nodes, _ := rtr.NodesFor(context.Background(), user.ID)
		mu.Lock()
		sawAuthOK = true
		onlineAtReply = hub.IsOnline(user.ID)
		routableAtRepl = len(nodes) > 0
		mu.Unlock()
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		gw.serve(context.Background(), tr, "test:1")
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("connection lifecycle did not finish")
	}

	mu.Lock()
	defer mu.Unlock()
	if !sawAuthOK {
		t.Fatal("the connection never authenticated; the test observed nothing")
	}
	if !onlineAtReply {
		t.Fatal("AUTH_OK was sent before the connection was reachable in the hub — " +
			"a message addressed in that window is dropped, and for the E2E relay it is lost for good")
	}
	if !routableAtRepl {
		t.Fatal("AUTH_OK was sent before the routing registry knew the connection — " +
			"another node's fanout would find no route to it")
	}
}
