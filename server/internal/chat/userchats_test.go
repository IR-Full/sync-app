package chat

import (
	"context"
	"testing"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store/memory"
)

// TestUserChatsPagesInNumericIDOrder pins the cursor's ordering. Chat ids are
// snowflakes that travel as decimal STRINGS, so plain string order puts "9"
// after "10" — and a cursor that skips backwards would silently drop chats from
// the list of anyone whose ids straddle a digit-length boundary.
func TestUserChatsPagesInNumericIDOrder(t *testing.T) {
	ctx := context.Background()
	st := memory.New()
	s := New(st, nil)

	// Deliberately out of insertion order, and with ids of different lengths.
	for _, id := range []string{"100", "9", "10"} {
		if err := st.CreateChat(ctx, &model.Chat{ID: id, Type: model.ChatGroup, Title: "c" + id, OwnerID: "u1"}); err != nil {
			t.Fatal(err)
		}
		if err := st.AddMember(ctx, &model.ChatMember{ChatID: id, UserID: "u1", Role: model.RoleOwner}); err != nil {
			t.Fatal(err)
		}
	}

	first, err := s.UserChats(ctx, "u1", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].Chat.ID != "9" || first[1].Chat.ID != "10" {
		t.Fatalf("first page is not in numeric order: %s", ids(first))
	}
	if first[0].MyRole != model.RoleOwner {
		t.Fatalf("role missing from the summary: %+v", first[0])
	}

	second, err := s.UserChats(ctx, "u1", first[len(first)-1].Chat.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].Chat.ID != "100" {
		t.Fatalf("second page lost or repeated rows: %s", ids(second))
	}
}

// TestUserChatsNamesTheDirectPeer: a 1:1 chat has no title, so the summary has
// to carry the other participant or the row cannot be rendered at all.
func TestUserChatsNamesTheDirectPeer(t *testing.T) {
	ctx := context.Background()
	st := memory.New()
	s := New(st, nil)

	if err := st.CreateChat(ctx, &model.Chat{ID: "5", Type: model.ChatDirect}); err != nil {
		t.Fatal(err)
	}
	for _, uid := range []string{"u1", "u2"} {
		if err := st.AddMember(ctx, &model.ChatMember{ChatID: "5", UserID: uid, Role: model.RoleMember}); err != nil {
			t.Fatal(err)
		}
	}

	list, err := s.UserChats(ctx, "u1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].PeerID != "u2" {
		t.Fatalf("peer not resolved: %+v", list)
	}

	// The same chat, seen from the other side, names the other person.
	list, err = s.UserChats(ctx, "u2", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].PeerID != "u1" {
		t.Fatalf("peer not resolved for the second member: %+v", list)
	}
}

func ids(list []model.ChatSummary) string {
	out := ""
	for _, s := range list {
		out += s.Chat.ID + " "
	}
	return out
}
