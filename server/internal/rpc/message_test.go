package rpc_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/synapse-chat/synapse/internal/chat"
	"github.com/synapse-chat/synapse/internal/message"
	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/rpc"
	"github.com/synapse-chat/synapse/internal/store/memory"
	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/id"
)

// startMessageService serves the message broker + reader over an in-process gRPC
// connection and returns a client adapter plus a chat both users belong to. The
// real client and server adapters are used, so anything the request/reply
// mapping drops is dropped here too.
func startMessageService(t *testing.T) (*rpc.MessageClient, string) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ids, _ := id.NewGenerator(1)
	st := memory.New().Stores()
	chatSvc := chat.New(st.Chats, ids)
	msgSvc := message.New(st.Messages, st.Reads, chatSvc, eventbus.NewMemory(), ids)

	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	rpc.RegisterMessage(srv, message.NewBroker(msgSvc, log), msgSvc)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ch, err := chatSvc.EnsureDirect(context.Background(), "1", "2")
	if err != nil {
		t.Fatal(err)
	}
	return rpc.NewMessageClient(conn), ch.ID
}

// TestSubmitCarriesAttachmentAndTTL pins the write half of the split path. The
// gateway hands the broker a command; on the monolith that command is applied
// directly, but here it crosses gRPC — and a field missing from SubmitRequest is
// silently dropped, so self-destruct and attachments would work on one
// deployment and not the other.
func TestSubmitCarriesAttachmentAndTTL(t *testing.T) {
	cl, chatID := startMessageService(t)

	res, err := cl.Submit(context.Background(), message.Command{
		Op: message.OpCreate, ActorID: "1", ChatID: chatID, DedupKey: "k1", Text: "voice note",
		TTLSeconds: 30,
		Attachment: &model.Attachment{
			Kind: model.AttachVoice, MediaRef: "m1", DurationMs: 4200, Waveform: []int32{1, 9, 3},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Message
	if m.ExpiresAt == 0 {
		t.Fatal("ttl dropped: message has no self-destruct deadline")
	}
	if m.Attachment == nil {
		t.Fatal("attachment dropped")
	}
	if m.Attachment.Kind != model.AttachVoice || m.Attachment.DurationMs != 4200 {
		t.Fatalf("attachment mangled: %+v", m.Attachment)
	}
	if len(m.Attachment.Waveform) != 3 {
		t.Fatalf("waveform lost: %+v", m.Attachment.Waveform)
	}
}

// TestReadPathCarriesEveryField pins the read half: history is what a client
// renders, so provenance and the self-destruct deadline have to survive the
// reply mapping as well as the request one.
func TestReadPathCarriesEveryField(t *testing.T) {
	ctx := context.Background()
	cl, chatID := startMessageService(t)

	orig, err := cl.Submit(ctx, message.Command{
		Op: message.OpCreate, ActorID: "1", ChatID: chatID, DedupKey: "k1", Text: "original", TTLSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cl.Forward(ctx, "1", chatID, orig.Message.ID, chatID, "k2"); err != nil {
		t.Fatal(err)
	}

	msgs, err := cl.History(ctx, "1", chatID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var sawTTL, sawForward bool
	for _, m := range msgs {
		sawTTL = sawTTL || m.ExpiresAt > 0
		if m.Forward != nil {
			sawForward = true
			if m.Forward.MessageID != orig.Message.ID || m.Forward.SenderID != "1" {
				t.Fatalf("wrong provenance: %+v", m.Forward)
			}
		}
	}
	if !sawTTL || !sawForward {
		t.Fatalf("history dropped fields: ttl=%v forward=%v", sawTTL, sawForward)
	}
}
