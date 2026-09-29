package call

import (
	"context"
	"errors"
	"testing"

	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/store/memory"
	"github.com/IR-Full/sync-app/server/pkg/eventbus"
	"github.com/IR-Full/sync-app/server/pkg/id"
	"github.com/IR-Full/sync-app/server/pkg/wire"
)

// roomChats is a fixed chat roster for tests.
type roomChats struct {
	members []string
}

func (r roomChats) IsMember(_ context.Context, _, userID string) (bool, error) {
	for _, m := range r.members {
		if m == userID {
			return true, nil
		}
	}
	return false, nil
}
func (r roomChats) MemberIDs(context.Context, string) ([]string, error) { return r.members, nil }

type capBus struct{ events []eventbus.Event }

func (b *capBus) Publish(_ context.Context, e eventbus.Event) error {
	b.events = append(b.events, e)
	return nil
}
func (b *capBus) Subscribe(string, string, eventbus.Handler) error { return nil }
func (b *capBus) Close() error                                     { return nil }

// lastState decodes the most recent broadcast call state.
func (b *capBus) lastState(t *testing.T) wire.CallStateBody {
	t.Helper()
	for i := len(b.events) - 1; i >= 0; i-- {
		if b.events[i].Subject == eventbus.SubjCallState {
			var s wire.CallStateBody
			if err := wire.Unmarshal(b.events[i].Data, &s); err != nil {
				t.Fatalf("decode state: %v", err)
			}
			return s
		}
	}
	t.Fatal("no call.state event published")
	return wire.CallStateBody{}
}

func newSvc(members ...string) (*Service, *capBus) {
	bus := &capBus{}
	ids, _ := id.NewGenerator(3)
	return New(memory.New().Stores().Calls, roomChats{members: members}, bus, ids), bus
}

func TestCallLifecycle1to1(t *testing.T) {
	s, bus := newSvc("alice", "bob")
	ctx := context.Background()

	c, err := s.Invite(ctx, "chat1", "alice", "devA", model.CallVideo, 100)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != model.CallRinging {
		t.Fatalf("new call state = %s, want ringing", c.State)
	}
	// Caller is joined; callee is invited (their device rings).
	st := bus.lastState(t)
	states := map[string]string{}
	for _, p := range st.Participants {
		states[p.UserID] = p.State
	}
	if states["alice"] != "joined" || states["bob"] != "invited" {
		t.Fatalf("roster after invite: %v", states)
	}

	// Bob accepts → the room goes active.
	if _, err := s.Accept(ctx, c.ID, "bob", "devB", 200); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, c.ID); got.State != model.CallActive {
		t.Fatalf("after accept state = %s, want active", got.State)
	}

	// Bob hangs up → only Alice remains, nobody pending → call ends.
	if err := s.Hangup(ctx, c.ID, "bob", 300); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, c.ID)
	if got.State != model.CallEnded {
		t.Fatalf("after hangup state = %s, want ended", got.State)
	}
	if got.EndedAt != 300 {
		t.Fatalf("ended_at = %d, want 300", got.EndedAt)
	}
}

func TestCallDeclineEndsOneToOne(t *testing.T) {
	s, _ := newSvc("alice", "bob")
	ctx := context.Background()
	c, err := s.Invite(ctx, "chat1", "alice", "devA", model.CallAudio, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Decline(ctx, c.ID, "bob", 2); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, c.ID)
	if got.State != model.CallEnded {
		t.Fatalf("declined 1:1 call state = %s, want ended", got.State)
	}
}

// mutableChats lets a test add a member after a call has already started.
type mutableChats struct{ members *[]string }

func (m mutableChats) IsMember(_ context.Context, _, userID string) (bool, error) {
	for _, id := range *m.members {
		if id == userID {
			return true, nil
		}
	}
	return false, nil
}
func (m mutableChats) MemberIDs(context.Context, string) ([]string, error) { return *m.members, nil }

// Invite rings every member at the time of the call, so everyone then in the chat
// is legitimately in the roster. Someone who joins the chat LATER is not — and
// leaving a room is only meaningful for someone in it. Without the roster check
// they could write a "left" row for a participant that never existed, and in a
// room down to two the settle() it triggers ends everyone else's conversation.
func TestLateChatJoinerCannotHangUpOrDeclineACallTheyAreNotIn(t *testing.T) {
	members := []string{"alice", "bob"}
	bus := &capBus{}
	ids, _ := id.NewGenerator(3)
	s := New(memory.New().Stores().Calls, mutableChats{members: &members}, bus, ids)
	ctx := context.Background()

	c, err := s.Invite(ctx, "chat1", "alice", "devA", model.CallVideo, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accept(ctx, c.ID, "bob", "devB", 2); err != nil {
		t.Fatal(err)
	}

	// Mallory joins the chat only now: a chat member, but never rung.
	members = append(members, "mallory")

	if err := s.Hangup(ctx, c.ID, "mallory", 3); !errors.Is(err, ErrForbidden) {
		t.Fatalf("late joiner hangup: got %v, want ErrForbidden", err)
	}
	if err := s.Decline(ctx, c.ID, "mallory", 3); !errors.Is(err, ErrForbidden) {
		t.Fatalf("late joiner decline: got %v, want ErrForbidden", err)
	}

	// The call Alice and Bob are on is untouched, and no junk row was written.
	got, _ := s.Get(ctx, c.ID)
	if got.State != model.CallActive {
		t.Fatalf("call state = %s, want active — an outsider ended it", got.State)
	}
	parts, err := s.Participants(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range parts {
		if p.UserID == "mallory" {
			t.Fatalf("outsider got a participant row: %+v", p)
		}
	}

	// They can still JOIN — chat membership is what entitles someone to enter.
	if _, err := s.Accept(ctx, c.ID, "mallory", "devM", 4); err != nil {
		t.Fatalf("late joiner should still be able to accept: %v", err)
	}
	// And having joined, they may now leave.
	if err := s.Hangup(ctx, c.ID, "mallory", 5); err != nil {
		t.Fatalf("participant hangup after joining: %v", err)
	}
}

func TestConferenceStaysAliveWhileTwoRemain(t *testing.T) {
	s, _ := newSvc("alice", "bob", "carol")
	ctx := context.Background()
	c, _ := s.Invite(ctx, "chat1", "alice", "devA", model.CallVideo, 1)
	if _, err := s.Accept(ctx, c.ID, "bob", "devB", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accept(ctx, c.ID, "carol", "devC", 3); err != nil {
		t.Fatal(err)
	}
	// Carol leaves — alice+bob are still talking, so the conference continues.
	if err := s.Hangup(ctx, c.ID, "carol", 4); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, c.ID)
	if got.State == model.CallEnded {
		t.Fatal("conference ended while two participants remained")
	}
	// Bob leaves too → only alice, nobody pending → ends.
	if err := s.Hangup(ctx, c.ID, "bob", 5); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Get(ctx, c.ID); got.State != model.CallEnded {
		t.Fatalf("state = %s, want ended after the last peer left", got.State)
	}
}

func TestSecondInviteJoinsExistingRoom(t *testing.T) {
	s, _ := newSvc("alice", "bob", "carol")
	ctx := context.Background()
	first, _ := s.Invite(ctx, "chat1", "alice", "devA", model.CallVideo, 1)
	// Carol also presses "call" in the same chat — she must join, not start a
	// rival room (otherwise the chat has two calls nobody can reconcile).
	second, err := s.Invite(ctx, "chat1", "carol", "devC", model.CallVideo, 2)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("second invite created a rival room %s (first %s)", second.ID, first.ID)
	}
	parts, _ := s.Participants(ctx, first.ID)
	for _, p := range parts {
		if p.UserID == "carol" && p.State != model.PartJoined {
			t.Fatalf("carol state = %s, want joined", p.State)
		}
	}
}

func TestNonMemberCannotCallOrSignal(t *testing.T) {
	s, _ := newSvc("alice", "bob")
	ctx := context.Background()
	if _, err := s.Invite(ctx, "chat1", "mallory", "devM", model.CallAudio, 1); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-member invite: got %v, want ErrForbidden", err)
	}
	c, _ := s.Invite(ctx, "chat1", "alice", "devA", model.CallAudio, 1)
	if _, err := s.Accept(ctx, c.ID, "mallory", "devM", 2); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-member accept: got %v, want ErrForbidden", err)
	}
	// InCall gates signaling relay: a non-participant must not be able to inject
	// SDP/ICE into someone else's call.
	if in, _ := s.InCall(ctx, c.ID, "mallory"); in {
		t.Fatal("non-participant reported as in-call (signaling would be relayed)")
	}
	if in, _ := s.InCall(ctx, c.ID, "bob"); !in {
		t.Fatal("invited member should be in-call for signaling")
	}
}

func TestCannotJoinEndedCall(t *testing.T) {
	s, _ := newSvc("alice", "bob")
	ctx := context.Background()
	c, _ := s.Invite(ctx, "chat1", "alice", "devA", model.CallAudio, 1)
	if err := s.Decline(ctx, c.ID, "bob", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accept(ctx, c.ID, "bob", "devB", 3); !errors.Is(err, ErrCallEnded) {
		t.Fatalf("join ended call: got %v, want ErrCallEnded", err)
	}
}

func TestInvalidKindRejected(t *testing.T) {
	s, _ := newSvc("alice", "bob")
	if _, err := s.Invite(context.Background(), "chat1", "alice", "devA", model.CallKind("hologram"), 1); !errors.Is(err, ErrBadKind) {
		t.Fatalf("got %v, want ErrBadKind", err)
	}
}
