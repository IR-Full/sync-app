package poll

import (
	"context"
	"errors"
	"testing"

	"github.com/synapse-chat/synapse/internal/store/memory"
	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/id"
	"github.com/synapse-chat/synapse/pkg/wire"
)

type memberChats struct{ members map[string]bool }

func (m memberChats) IsMember(_ context.Context, _, userID string) (bool, error) {
	return m.members[userID], nil
}

type capBus struct{ events []eventbus.Event }

func (b *capBus) Publish(_ context.Context, e eventbus.Event) error {
	b.events = append(b.events, e)
	return nil
}
func (b *capBus) Subscribe(string, string, eventbus.Handler) error { return nil }
func (b *capBus) Close() error                                     { return nil }

func newSvc(members ...string) (*Service, *capBus) {
	set := map[string]bool{}
	for _, m := range members {
		set[m] = true
	}
	bus := &capBus{}
	ids, _ := id.NewGenerator(5)
	return New(memory.New().Stores().Polls, memberChats{members: set}, bus, ids), bus
}

func mkPoll(t *testing.T, s *Service, multi bool) string {
	t.Helper()
	p, err := s.Create(context.Background(), CreateInput{
		ChatID: "c1", MessageID: "m1", CreatorID: "alice",
		Question: "Lunch?", Options: []string{"Pizza", "Sushi", "Salad"},
		MultiChoice: multi,
	}, 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return p.ID
}

func TestPollSingleChoiceReplacesVote(t *testing.T) {
	s, _ := newSvc("alice", "bob")
	ctx := context.Background()
	id := mkPoll(t, s, false)

	if _, err := s.Vote(ctx, id, "bob", 0, 2); err != nil {
		t.Fatal(err)
	}
	st, err := s.Results(ctx, id, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if st.Options[0].Votes != 1 || st.TotalVotes != 1 {
		t.Fatalf("after first vote: %+v", st.Options)
	}
	// Changing the mind must MOVE the vote, not add a second one.
	if _, err := s.Vote(ctx, id, "bob", 2, 3); err != nil {
		t.Fatal(err)
	}
	st, _ = s.Results(ctx, id, "bob")
	if st.Options[0].Votes != 0 || st.Options[2].Votes != 1 || st.TotalVotes != 1 {
		t.Fatalf("single-choice re-vote did not replace: %+v total=%d", st.Options, st.TotalVotes)
	}
	if len(st.MyVotes) != 1 || st.MyVotes[0] != 2 {
		t.Fatalf("my_votes = %v, want [2]", st.MyVotes)
	}
}

func TestPollMultiChoiceToggles(t *testing.T) {
	s, _ := newSvc("alice", "bob")
	ctx := context.Background()
	id := mkPoll(t, s, true)

	for _, opt := range []int32{0, 1} {
		if _, err := s.Vote(ctx, id, "bob", opt, 2); err != nil {
			t.Fatal(err)
		}
	}
	st, _ := s.Results(ctx, id, "bob")
	if st.Options[0].Votes != 1 || st.Options[1].Votes != 1 {
		t.Fatalf("multi-choice votes: %+v", st.Options)
	}
	// Re-voting the same option toggles it off.
	if _, err := s.Vote(ctx, id, "bob", 0, 3); err != nil {
		t.Fatal(err)
	}
	st, _ = s.Results(ctx, id, "bob")
	if st.Options[0].Votes != 0 || st.Options[1].Votes != 1 {
		t.Fatalf("toggle off failed: %+v", st.Options)
	}
}

func TestPollTallyAcrossVoters(t *testing.T) {
	s, _ := newSvc("alice", "bob", "carol")
	ctx := context.Background()
	id := mkPoll(t, s, false)
	for _, u := range []string{"alice", "bob", "carol"} {
		if _, err := s.Vote(ctx, id, u, 1, 2); err != nil {
			t.Fatal(err)
		}
	}
	st, _ := s.Results(ctx, id, "alice")
	if st.Options[1].Votes != 3 || st.TotalVotes != 3 {
		t.Fatalf("tally: %+v total=%d", st.Options, st.TotalVotes)
	}
}

func TestPollClosedRejectsVotes(t *testing.T) {
	s, _ := newSvc("alice", "bob")
	ctx := context.Background()
	id := mkPoll(t, s, false)
	if _, err := s.Close(ctx, id, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Vote(ctx, id, "bob", 0, 2); !errors.Is(err, ErrClosed) {
		t.Fatalf("vote on closed poll: got %v, want ErrClosed", err)
	}
}

func TestPollOnlyCreatorCloses(t *testing.T) {
	s, _ := newSvc("alice", "bob")
	ctx := context.Background()
	id := mkPoll(t, s, false)
	if _, err := s.Close(ctx, id, "bob"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-creator close: got %v, want ErrForbidden", err)
	}
}

func TestPollNonMemberRejected(t *testing.T) {
	s, _ := newSvc("alice", "bob")
	ctx := context.Background()
	id := mkPoll(t, s, false)
	if _, err := s.Vote(ctx, id, "mallory", 0, 2); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-member vote: got %v, want ErrForbidden", err)
	}
	if _, err := s.Results(ctx, id, "mallory"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-member results: got %v, want ErrForbidden", err)
	}
}

func TestPollOptionRangeChecked(t *testing.T) {
	s, _ := newSvc("alice", "bob")
	ctx := context.Background()
	id := mkPoll(t, s, false)
	for _, bad := range []int32{-1, 3, 99} {
		if _, err := s.Vote(ctx, id, "bob", bad, 2); !errors.Is(err, ErrBadOption) {
			t.Fatalf("option %d: got %v, want ErrBadOption", bad, err)
		}
	}
}

func TestPollValidation(t *testing.T) {
	s, _ := newSvc("alice")
	ctx := context.Background()
	cases := map[string]CreateInput{
		"empty question": {ChatID: "c1", CreatorID: "alice", Question: "  ", Options: []string{"a", "b"}},
		"one option":     {ChatID: "c1", CreatorID: "alice", Question: "Q", Options: []string{"a"}},
		"duplicate opts": {ChatID: "c1", CreatorID: "alice", Question: "Q", Options: []string{"a", "a"}},
		"empty option":   {ChatID: "c1", CreatorID: "alice", Question: "Q", Options: []string{"a", " "}},
		"too many opts":  {ChatID: "c1", CreatorID: "alice", Question: "Q", Options: make([]string, MaxOptions+1)},
	}
	for name, in := range cases {
		if _, err := s.Create(ctx, in, 1); !errors.Is(err, ErrBadPoll) {
			t.Errorf("%s: got %v, want ErrBadPoll", name, err)
		}
	}
}

func TestPollBroadcastHidesMyVotes(t *testing.T) {
	s, bus := newSvc("alice", "bob")
	ctx := context.Background()
	id := mkPoll(t, s, false)
	if _, err := s.Vote(ctx, id, "bob", 0, 2); err != nil {
		t.Fatal(err)
	}
	// The chat-wide broadcast must not carry anyone's personal selections.
	for _, e := range bus.events {
		if e.Subject != eventbus.SubjPollState {
			continue
		}
		var st wire.PollStateBody
		if err := wire.Unmarshal(e.Data, &st); err != nil {
			t.Fatal(err)
		}
		if len(st.MyVotes) != 0 {
			t.Fatalf("broadcast leaked personal votes: %v", st.MyVotes)
		}
	}
}
