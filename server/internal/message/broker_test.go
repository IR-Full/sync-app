package message

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/synapse-chat/synapse/internal/chat"
	"github.com/synapse-chat/synapse/internal/store/memory"
	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/id"
)

func newTestBroker(t *testing.T) (*Broker, string, string) {
	t.Helper()
	ids, _ := id.NewGenerator(1)
	st := memory.New().Stores()
	chatSvc := chat.New(st.Chats, ids)
	svc := New(st.Messages, st.Reads, chatSvc, eventbus.NewMemory(), ids)
	user := ids.NextString()
	ch, err := chatSvc.EnsureDirect(context.Background(), user, user) // self-chat
	if err != nil {
		t.Fatal(err)
	}
	return NewBroker(svc, slog.New(slog.NewTextHandler(io.Discard, nil))), ch.ID, user
}

func TestBrokerCreateEditDelete(t *testing.T) {
	b, chatID, user := newTestBroker(t)
	ctx := context.Background()

	// Create.
	res, err := b.Submit(ctx, Command{Op: OpCreate, ActorID: user, ChatID: chatID, DedupKey: "k1", Text: "hi"})
	if err != nil || res.Duplicate || res.Message.Seq != 1 {
		t.Fatalf("create: seq=%v dup=%v err=%v", res.Message, res.Duplicate, err)
	}
	mid := res.Message.ID

	// Idempotent create.
	res2, err := b.Submit(ctx, Command{Op: OpCreate, ActorID: user, ChatID: chatID, DedupKey: "k1", Text: "hi"})
	if err != nil || !res2.Duplicate || res2.Message.ID != mid {
		t.Fatalf("dedup: dup=%v id=%s err=%v", res2.Duplicate, res2.Message.ID, err)
	}

	// Edit.
	if _, err := b.Submit(ctx, Command{Op: OpEdit, ActorID: user, ChatID: chatID, MessageID: mid, Text: "edited"}); err != nil {
		t.Fatalf("edit: %v", err)
	}

	// Delete.
	if _, err := b.Submit(ctx, Command{Op: OpDelete, ActorID: user, ChatID: chatID, MessageID: mid}); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestBrokerValidation(t *testing.T) {
	b, chatID, user := newTestBroker(t)
	ctx := context.Background()

	cases := []struct {
		name string
		cmd  Command
		want error
	}{
		{"empty create", Command{Op: OpCreate, ActorID: user, ChatID: chatID, DedupKey: "e"}, ErrEmptyMessage},
		{"too long", Command{Op: OpCreate, ActorID: user, ChatID: chatID, DedupKey: "l", Text: strings.Repeat("x", MaxTextLen+1)}, ErrTooLong},
		{"empty edit", Command{Op: OpEdit, ActorID: user, ChatID: chatID, MessageID: "1", Text: ""}, ErrEmptyMessage},
		{"delete no id", Command{Op: OpDelete, ActorID: user, ChatID: chatID}, ErrBadCommand},
		{"unknown op", Command{Op: "frobnicate", ActorID: user, ChatID: chatID}, ErrBadCommand},
	}
	for _, tc := range cases {
		if _, err := b.Submit(ctx, tc.cmd); err != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, err, tc.want)
		}
	}

	// A valid media-only message (no text) is accepted.
	if _, err := b.Submit(ctx, Command{Op: OpCreate, ActorID: user, ChatID: chatID, DedupKey: "m", MediaRef: "mref"}); err != nil {
		t.Fatalf("media-only create: %v", err)
	}
}
