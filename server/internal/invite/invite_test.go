package invite

import (
	"context"
	"errors"
	"testing"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/internal/store/memory"
)

// fakeChats is an in-memory chat with a mutable member roster.
type fakeChats struct {
	chat    *model.Chat
	members map[string]model.MemberRole
	st      *memory.Store
}

func (f *fakeChats) Get(context.Context, string) (*model.Chat, error) { return f.chat, nil }
func (f *fakeChats) Members(context.Context, string) ([]*model.ChatMember, error) {
	var out []*model.ChatMember
	for uid, role := range f.members {
		out = append(out, &model.ChatMember{ChatID: f.chat.ID, UserID: uid, Role: role})
	}
	return out, nil
}
func (f *fakeChats) AddMember(_ context.Context, m *model.ChatMember) error {
	f.members[m.UserID] = m.Role
	return nil
}
func (f *fakeChats) IsMember(_ context.Context, _, userID string) (bool, error) {
	_, ok := f.members[userID]
	return ok, nil
}

func (f *fakeChats) MemberRole(_ context.Context, _, userID string) (model.MemberRole, bool, error) {
	role, ok := f.members[userID]
	return role, ok, nil
}

func (f *fakeChats) CountMembersWithRole(_ context.Context, _ string, role model.MemberRole) (int, error) {
	n := 0
	for _, r := range f.members {
		if r == role {
			n++
		}
	}
	return n, nil
}

// SetMemberRole lets the fake double as the role store, so role changes and the
// roster the service reads stay consistent.
func (f *fakeChats) SetMemberRole(_ context.Context, _, userID string, role model.MemberRole) error {
	if _, ok := f.members[userID]; !ok {
		return store.ErrNotFound
	}
	f.members[userID] = role
	return nil
}

func newSvc() (*Service, *fakeChats) {
	st := memory.New()
	chats := &fakeChats{
		chat:    &model.Chat{ID: "c1", Type: model.ChatGroup, OwnerID: "owner"},
		members: map[string]model.MemberRole{"owner": model.RoleOwner, "admin": model.RoleAdmin, "member": model.RoleMember},
		st:      st,
	}
	_ = st.CreateChat(context.Background(), chats.chat)
	return New(st.Stores().Invites, chats, chats), chats
}

func TestUsernameOwnerOnlyAndValidated(t *testing.T) {
	s, _ := newSvc()
	ctx := context.Background()

	// A handle is the chat's public identity → owner-only.
	if err := s.SetUsername(ctx, "c1", "admin", "mychannel"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin set username: got %v, want ErrForbidden", err)
	}
	// Validation rejects unsafe/ambiguous handles.
	for _, bad := range []string{"ab", "1channel", "has space", "bad-dash", "sym!bols"} {
		if err := s.SetUsername(ctx, "c1", "owner", bad); !errors.Is(err, ErrBadUsername) {
			t.Errorf("username %q: got %v, want ErrBadUsername", bad, err)
		}
	}
	if err := s.SetUsername(ctx, "c1", "owner", "mychannel"); err != nil {
		t.Fatal(err)
	}
	got, err := s.ResolveUsername(ctx, "@MyChannel") // case-insensitive
	if err != nil || got.ID != "c1" {
		t.Fatalf("resolve: %+v err=%v", got, err)
	}
}

func TestUsernameUniquenessIsCaseInsensitive(t *testing.T) {
	s, chats := newSvc()
	ctx := context.Background()
	if err := s.SetUsername(ctx, "c1", "owner", "newschannel"); err != nil {
		t.Fatal(err)
	}
	// A second chat cannot claim a case-variant — that would be a phishing surface.
	other := &model.Chat{ID: "c2", Type: model.ChatChannel, OwnerID: "owner2"}
	_ = chats.st.CreateChat(ctx, other)
	if err := chats.st.SetChatUsername(ctx, "c2", "NEWSCHANNEL"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("case-variant claim: got %v, want ErrConflict", err)
	}
}

func TestInviteLinkJoinAndBounds(t *testing.T) {
	s, chats := newSvc()
	ctx := context.Background()

	// Only admins/owner mint links.
	if _, err := s.CreateLink(ctx, "c1", "member", 0, 0, 1); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member create link: got %v, want ErrForbidden", err)
	}
	l, err := s.CreateLink(ctx, "c1", "admin", 0, 2, 1) // capped at 2 uses
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Code) < 20 {
		t.Fatalf("invite code too short to be unguessable: %q", l.Code)
	}
	for _, u := range []string{"newbie1", "newbie2"} {
		if _, err := s.Join(ctx, l.Code, u, 10); err != nil {
			t.Fatalf("join %s: %v", u, err)
		}
		if _, ok := chats.members[u]; !ok {
			t.Fatalf("%s did not become a member", u)
		}
	}
	// The cap is enforced: a third joiner is refused.
	if _, err := s.Join(ctx, l.Code, "newbie3", 10); !errors.Is(err, ErrInvalidLink) {
		t.Fatalf("over-cap join: got %v, want ErrInvalidLink", err)
	}
}

func TestRejoiningDoesNotConsumeAUse(t *testing.T) {
	s, _ := newSvc()
	ctx := context.Background()
	l, err := s.CreateLink(ctx, "c1", "owner", 0, 1, 1) // single use
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(ctx, l.Code, "newbie", 10); err != nil {
		t.Fatal(err)
	}
	// Opening the link again as an existing member must be idempotent, not burn
	// the only remaining use.
	if _, err := s.Join(ctx, l.Code, "newbie", 11); err != nil {
		t.Fatalf("rejoin: %v", err)
	}
	links, err := s.ListLinks(ctx, "c1", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].Uses != 1 {
		t.Fatalf("rejoin consumed a use: uses=%d, want 1", links[0].Uses)
	}
}

func TestRevokedAndExpiredLinksRefused(t *testing.T) {
	s, _ := newSvc()
	ctx := context.Background()

	revoked, _ := s.CreateLink(ctx, "c1", "owner", 0, 0, 1)
	if err := s.RevokeLink(ctx, "c1", "owner", revoked.Code); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(ctx, revoked.Code, "x", 10); !errors.Is(err, ErrInvalidLink) {
		t.Fatalf("revoked link: got %v, want ErrInvalidLink", err)
	}
	expired, _ := s.CreateLink(ctx, "c1", "owner", 100, 0, 1) // expires at t=100
	if _, err := s.Join(ctx, expired.Code, "y", 200); !errors.Is(err, ErrInvalidLink) {
		t.Fatalf("expired link: got %v, want ErrInvalidLink", err)
	}
}

func TestJoinPublicByUsername(t *testing.T) {
	s, chats := newSvc()
	ctx := context.Background()
	if err := s.SetUsername(ctx, "c1", "owner", "openchannel"); err != nil {
		t.Fatal(err)
	}
	// A public handle needs no link — that is what public means.
	if _, err := s.JoinPublic(ctx, "@openchannel", "stranger", 10); err != nil {
		t.Fatal(err)
	}
	if _, ok := chats.members["stranger"]; !ok {
		t.Fatal("public join did not add the member")
	}
}

func TestRoleChangesAreOwnerOnly(t *testing.T) {
	s, chats := newSvc()
	ctx := context.Background()

	// An admin must NOT be able to promote/demote — otherwise an admin could
	// demote the owner and seize the chat.
	if err := s.SetRole(ctx, "c1", "admin", "member", model.RoleAdmin); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin changing roles: got %v, want ErrForbidden", err)
	}
	if err := s.SetRole(ctx, "c1", "owner", "member", model.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if chats.members["member"] != model.RoleAdmin {
		t.Fatalf("promotion did not apply: %v", chats.members["member"])
	}
}

func TestCannotDemoteLastOwner(t *testing.T) {
	s, _ := newSvc()
	ctx := context.Background()
	// Demoting the only owner would leave the chat unadministrable forever.
	if err := s.SetRole(ctx, "c1", "owner", "owner", model.RoleMember); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("got %v, want ErrLastOwner", err)
	}
	// With a second owner it is allowed.
	if err := s.SetRole(ctx, "c1", "owner", "admin", model.RoleOwner); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRole(ctx, "c1", "owner", "owner", model.RoleMember); err != nil {
		t.Fatalf("demote with a co-owner present: %v", err)
	}
}
