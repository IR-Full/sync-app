package chat

import (
	"context"
	"testing"

	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/store/memory"
)

// TestUserChatsPagesByActivityNotByID pins the ordering, and it is the opposite
// of what this test used to assert.
//
// The list was ordered by chat id — which is creation order — and paged by a bare
// id cursor. That is not a chat list: a conversation someone replied to a minute
// ago belongs at the top, and its position has nothing to do with when the chat
// was created. The order is now (last activity DESC, chat id DESC), and the cursor
// names both so a page boundary cannot skip or repeat while messages arrive.
func TestUserChatsPagesByActivityNotByID(t *testing.T) {
	ctx := context.Background()
	st := memory.New()
	s := New(st, nil)

	// Three chats. The OLDEST-created one is the most recently active, so id order
	// and activity order disagree on every position.
	for _, c := range []struct {
		id       string
		activity int64
	}{
		{"9", 3000},   // created first, active most recently
		{"10", 1000},  // least recently active
		{"100", 2000}, // created last, in the middle
	} {
		if err := st.CreateChat(ctx, &model.Chat{
			ID: c.id, Type: model.ChatGroup, Title: "c" + c.id, OwnerID: "u1",
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.AddMember(ctx, &model.ChatMember{ChatID: c.id, UserID: "u1", Role: model.RoleOwner}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := st.InsertMessage(ctx, &model.Message{
			ID: "m" + c.id, ChatID: c.id, SenderID: "u2",
			Text: "hello from " + c.id, CreatedAt: c.activity,
		}, "", nil); err != nil {
			t.Fatal(err)
		}
	}

	first, err := s.UserChats(ctx, "u1", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].Chat.ID != "9" || first[1].Chat.ID != "100" {
		t.Fatalf("not in activity order: %s", ids(first))
	}
	if first[0].MyRole != model.RoleOwner {
		t.Fatalf("role missing from the summary: %+v", first[0])
	}
	// The row carries what a list actually draws, which it previously did not.
	if first[0].LastMessage == nil || first[0].LastMessage.Text != "hello from 9" {
		t.Fatalf("no last-message preview: %+v", first[0])
	}
	if first[0].UnreadCount != 1 {
		t.Fatalf("unread = %d, want 1", first[0].UnreadCount)
	}
	if first[0].LastActivityAt != 3000 {
		t.Fatalf("activity = %d, want 3000", first[0].LastActivityAt)
	}

	// Paging uses BOTH halves of the cursor.
	last := first[len(first)-1]
	second, err := s.UserChatPage(ctx, "u1", ChatPage{
		AfterActivity: last.LastActivityAt, After: last.Chat.ID, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].Chat.ID != "10" {
		t.Fatalf("second page lost or repeated rows: %s", ids(second))
	}
}

// TestUserChatsCountsUnreadAgainstTheReadCursor checks the badge, including the
// case that would otherwise go negative: a read cursor ahead of the newest LIVE
// message, which happens when the message it pointed at was deleted.
func TestUserChatsCountsUnreadAgainstTheReadCursor(t *testing.T) {
	ctx := context.Background()
	st := memory.New()
	s := New(st, nil)

	if err := st.CreateChat(ctx, &model.Chat{ID: "7", Type: model.ChatGroup, OwnerID: "u1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMember(ctx, &model.ChatMember{ChatID: "7", UserID: "u1", Role: model.RoleOwner}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		if _, _, err := st.InsertMessage(ctx, &model.Message{
			ID: "m" + string(rune('0'+i)), ChatID: "7", SenderID: "u2", CreatedAt: int64(i * 100),
		}, "", nil); err != nil {
			t.Fatal(err)
		}
	}

	list, err := s.UserChats(ctx, "u1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if list[0].UnreadCount != 5 {
		t.Fatalf("unread = %d with nothing read, want 5", list[0].UnreadCount)
	}

	if err := st.SetRead(ctx, &model.ReadState{ChatID: "7", UserID: "u1", UpToSeq: 3}); err != nil {
		t.Fatal(err)
	}
	list, _ = s.UserChats(ctx, "u1", "", 10)
	if list[0].UnreadCount != 2 {
		t.Fatalf("unread = %d after reading 3 of 5, want 2", list[0].UnreadCount)
	}

	// A cursor beyond the newest live message must clamp to zero, not go negative.
	if err := st.SetRead(ctx, &model.ReadState{ChatID: "7", UserID: "u1", UpToSeq: 99}); err != nil {
		t.Fatal(err)
	}
	list, _ = s.UserChats(ctx, "u1", "", 10)
	if list[0].UnreadCount != 0 {
		t.Fatalf("unread = %d for an over-advanced cursor, want 0", list[0].UnreadCount)
	}
}

// TestUserChatsHidesArchivedUnlessAsked checks the per-member pile. Archiving is
// one person's opinion about a shared chat, so it must not remove the chat for
// anyone else.
func TestUserChatsHidesArchivedUnlessAsked(t *testing.T) {
	ctx := context.Background()
	st := memory.New()
	s := New(st, nil)

	for _, id := range []string{"1", "2"} {
		if err := st.CreateChat(ctx, &model.Chat{ID: id, Type: model.ChatGroup, OwnerID: "u1"}); err != nil {
			t.Fatal(err)
		}
		for _, uid := range []string{"u1", "u2"} {
			if err := st.AddMember(ctx, &model.ChatMember{ChatID: id, UserID: uid, Role: model.RoleMember}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := st.SetMemberFlags(ctx, "1", "u1", model.MemberFlags{Archived: true}); err != nil {
		t.Fatal(err)
	}

	visible, err := s.UserChats(ctx, "u1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 1 || visible[0].Chat.ID != "2" {
		t.Fatalf("archived chat still in the main list: %s", ids(visible))
	}

	archived, err := s.UserChatPage(ctx, "u1", ChatPage{Limit: 10, IncludeArchived: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != 2 {
		t.Fatalf("archived pile has %d chats, want both", len(archived))
	}

	// And the other member's list is untouched.
	other, err := s.UserChats(ctx, "u2", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 2 {
		t.Fatalf("one member's archive hid a chat from another: %s", ids(other))
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
