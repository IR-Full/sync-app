package pin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/synapse-chat/synapse/internal/store/memory"
	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// roleChats models "who may pin" and "who is a member" independently, which is
// exactly the distinction the service must respect.
type roleChats struct {
	canPin   map[string]bool
	isMember map[string]bool
}

func (r roleChats) CanPin(_ context.Context, _, userID string) (bool, error) {
	return r.canPin[userID], nil
}
func (r roleChats) IsMember(_ context.Context, _, userID string) (bool, error) {
	return r.isMember[userID], nil
}

type capBus struct{ events []eventbus.Event }

func (b *capBus) Publish(_ context.Context, e eventbus.Event) error {
	b.events = append(b.events, e)
	return nil
}
func (b *capBus) Subscribe(string, string, eventbus.Handler) error { return nil }
func (b *capBus) Close() error                                     { return nil }

func newSvc() (*Service, *capBus) {
	st := memory.New().Stores()
	bus := &capBus{}
	chats := roleChats{
		// admin may pin; member may not, but both are members.
		canPin:   map[string]bool{"admin": true},
		isMember: map[string]bool{"admin": true, "member": true},
	}
	return New(st.Pins, st.Drafts, chats, bus), bus
}

func TestPinRequiresPermission(t *testing.T) {
	s, bus := newSvc()
	ctx := context.Background()

	// A plain member cannot pin — a pin is visible to the whole chat.
	if err := s.Pin(ctx, "c1", "m1", "member", 100); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member pin: got %v, want ErrForbidden", err)
	}
	if len(bus.events) != 0 {
		t.Fatal("unauthorized pin still broadcast")
	}
	// An admin can.
	if err := s.Pin(ctx, "c1", "m1", "admin", 100); err != nil {
		t.Fatal(err)
	}
	pins, err := s.ListPins(ctx, "c1", "member") // any member may READ pins
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 1 || pins[0].MessageID != "m1" || pins[0].PinnedBy != "admin" {
		t.Fatalf("pins: %+v", pins)
	}
}

func TestPinBroadcastsFullSet(t *testing.T) {
	s, bus := newSvc()
	ctx := context.Background()
	if err := s.Pin(ctx, "c1", "m1", "admin", 100); err != nil {
		t.Fatal(err)
	}
	if err := s.Pin(ctx, "c1", "m2", "admin", 200); err != nil {
		t.Fatal(err)
	}
	// The last broadcast carries the WHOLE set, so a client never has to merge
	// deltas to know what is pinned.
	var last wire.PinnedBody
	for _, e := range bus.events {
		if e.Subject == eventbus.SubjPinned {
			_ = wire.Unmarshal(e.Data, &last)
		}
	}
	if len(last.Pins) != 2 {
		t.Fatalf("broadcast carried %d pins, want the full set of 2", len(last.Pins))
	}
}

func TestUnpin(t *testing.T) {
	s, _ := newSvc()
	ctx := context.Background()
	if err := s.Pin(ctx, "c1", "m1", "admin", 100); err != nil {
		t.Fatal(err)
	}
	if err := s.Unpin(ctx, "c1", "m1", "member"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member unpin: got %v, want ErrForbidden", err)
	}
	if err := s.Unpin(ctx, "c1", "m1", "admin"); err != nil {
		t.Fatal(err)
	}
	pins, _ := s.ListPins(ctx, "c1", "admin")
	if len(pins) != 0 {
		t.Fatalf("still pinned: %+v", pins)
	}
}

func TestNonMemberCannotReadPins(t *testing.T) {
	s, _ := newSvc()
	if _, err := s.ListPins(context.Background(), "c1", "stranger"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("got %v, want ErrForbidden", err)
	}
}

func TestDraftSyncIsIncremental(t *testing.T) {
	s, _ := newSvc()
	ctx := context.Background()

	if err := s.SetDraft(ctx, "member", "c1", "hello", "", 100); err != nil {
		t.Fatal(err)
	}
	list, cursor, err := s.SyncDrafts(ctx, "member", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Text != "hello" || cursor != 100 {
		t.Fatalf("full sync: %+v cursor=%d", list, cursor)
	}
	// Nothing changed since the cursor.
	list, _, _ = s.SyncDrafts(ctx, "member", cursor)
	if len(list) != 0 {
		t.Fatalf("incremental sync returned %d unchanged drafts", len(list))
	}
	// Editing bumps it back into the window.
	if err := s.SetDraft(ctx, "member", "c1", "hello world", "", 200); err != nil {
		t.Fatal(err)
	}
	list, cursor, _ = s.SyncDrafts(ctx, "member", cursor)
	if len(list) != 1 || list[0].Text != "hello world" || cursor != 200 {
		t.Fatalf("after edit: %+v cursor=%d", list, cursor)
	}
}

func TestClearingDraftDeletesIt(t *testing.T) {
	s, _ := newSvc()
	ctx := context.Background()
	if err := s.SetDraft(ctx, "member", "c1", "typing…", "", 100); err != nil {
		t.Fatal(err)
	}
	// Erasing the text must REMOVE the draft, otherwise an empty box on one
	// device would leave stale text on the others.
	if err := s.SetDraft(ctx, "member", "c1", "", "", 200); err != nil {
		t.Fatal(err)
	}
	list, _, _ := s.SyncDrafts(ctx, "member", 0)
	if len(list) != 0 {
		t.Fatalf("cleared draft still present: %+v", list)
	}
}

func TestDraftsArePrivate(t *testing.T) {
	s, _ := newSvc()
	ctx := context.Background()
	if err := s.SetDraft(ctx, "member", "c1", "secret", "", 100); err != nil {
		t.Fatal(err)
	}
	// Another user in the same chat must not see it.
	list, _, err := s.SyncDrafts(ctx, "admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("draft leaked to another user: %+v", list)
	}
}

func TestDraftRequiresMembershipAndBounds(t *testing.T) {
	s, _ := newSvc()
	ctx := context.Background()
	if err := s.SetDraft(ctx, "stranger", "c1", "hi", "", 1); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-member draft: got %v, want ErrForbidden", err)
	}
	long := strings.Repeat("x", MaxDraftLen+1)
	if err := s.SetDraft(ctx, "member", "c1", long, "", 1); !errors.Is(err, ErrTooLong) {
		t.Fatalf("oversized draft: got %v, want ErrTooLong", err)
	}
}
