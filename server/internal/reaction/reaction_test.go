package reaction

import (
	"context"
	"errors"
	"testing"

	"github.com/synapse-chat/synapse/internal/store/memory"
	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/wire"
)

type allowChats struct{ member bool }

func (a allowChats) IsMember(context.Context, string, string) (bool, error) { return a.member, nil }

// countingBus records published reaction events.
type countingBus struct{ events []eventbus.Event }

func (b *countingBus) Publish(_ context.Context, e eventbus.Event) error {
	b.events = append(b.events, e)
	return nil
}
func (b *countingBus) Subscribe(string, string, eventbus.Handler) error { return nil }
func (b *countingBus) Close() error                                     { return nil }

func newSvc(member bool) (*Service, *countingBus) {
	bus := &countingBus{}
	return New(memory.New().Stores().Reactions, allowChats{member: member}, bus), bus
}

func TestReactionToggle(t *testing.T) {
	s, bus := newSvc(true)
	ctx := context.Background()

	// First tap adds.
	added, counts, err := s.Toggle(ctx, "c1", "m1", "u1", "👍", 1)
	if err != nil || !added {
		t.Fatalf("add: added=%v err=%v", added, err)
	}
	if counts["👍"] != 1 {
		t.Fatalf("counts after add: %v", counts)
	}

	// Same emoji again removes (toggle).
	added, counts, err = s.Toggle(ctx, "c1", "m1", "u1", "👍", 2)
	if err != nil || added {
		t.Fatalf("toggle off: added=%v err=%v", added, err)
	}
	if counts["👍"] != 0 {
		t.Fatalf("counts after toggle off: %v", counts)
	}

	// Every change published an event for fanout.
	if len(bus.events) != 2 {
		t.Fatalf("expected 2 published events, got %d", len(bus.events))
	}
	if bus.events[0].Subject != eventbus.SubjReaction {
		t.Fatalf("wrong subject %q", bus.events[0].Subject)
	}
	var upd wire.ReactUpdateBody
	if err := wire.Unmarshal(bus.events[0].Data, &upd); err != nil {
		t.Fatalf("event body: %v", err)
	}
	if upd.MessageID != "m1" || upd.UserID != "u1" || !upd.Added {
		t.Fatalf("bad event body: %+v", upd)
	}
}

func TestReactionReplacesPrevious(t *testing.T) {
	s, _ := newSvc(true)
	ctx := context.Background()
	if _, _, err := s.Toggle(ctx, "c1", "m1", "u1", "👍", 1); err != nil {
		t.Fatal(err)
	}
	// A different emoji replaces — one reaction per (message, user).
	added, counts, err := s.Toggle(ctx, "c1", "m1", "u1", "🎉", 2)
	if err != nil || !added {
		t.Fatalf("replace: added=%v err=%v", added, err)
	}
	if counts["👍"] != 0 || counts["🎉"] != 1 {
		t.Fatalf("expected only 🎉, got %v", counts)
	}
}

func TestReactionTallyAcrossUsers(t *testing.T) {
	s, _ := newSvc(true)
	ctx := context.Background()
	for _, u := range []string{"u1", "u2", "u3"} {
		if _, _, err := s.Toggle(ctx, "c1", "m1", u, "👍", 1); err != nil {
			t.Fatal(err)
		}
	}
	_, counts, err := s.Toggle(ctx, "c1", "m1", "u4", "🎉", 1)
	if err != nil {
		t.Fatal(err)
	}
	if counts["👍"] != 3 || counts["🎉"] != 1 {
		t.Fatalf("tally: %v", counts)
	}
}

func TestReactionRequiresMembership(t *testing.T) {
	s, bus := newSvc(false) // not a member
	_, _, err := s.Toggle(context.Background(), "c1", "m1", "stranger", "👍", 1)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
	if len(bus.events) != 0 {
		t.Fatal("must not publish when unauthorized")
	}
}

func TestReactionRejectsBadEmoji(t *testing.T) {
	s, _ := newSvc(true)
	ctx := context.Background()
	for _, bad := range []string{"", "this is a long free text reaction"} {
		if _, _, err := s.Toggle(ctx, "c1", "m1", "u1", bad, 1); !errors.Is(err, ErrBadEmoji) {
			t.Fatalf("emoji %q: expected ErrBadEmoji, got %v", bad, err)
		}
	}
}
