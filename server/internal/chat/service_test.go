package chat

import (
	"context"
	"errors"
	"testing"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/internal/store/memory"
	"github.com/SyncApp-chat/SyncApp/pkg/id"
)

/*
 * The service's public surface is almost entirely authorization: who may post,
 * who may pin, who is a member. None of those questions has a safe default, and
 * a wrong answer is silent — a channel that lets any subscriber broadcast, or a
 * demotion that keeps working for the length of a cache TTL, looks exactly like
 * correct behaviour until someone abuses it.
 */

func newService(t *testing.T) (*Service, store.ChatStore) {
	t.Helper()
	ids, err := id.NewGenerator(1)
	if err != nil {
		t.Fatal(err)
	}
	chats := memory.New().Stores().Chats
	return New(chats, ids), chats
}

func ctx() context.Context { return context.Background() }

// ----------------------------------------------------------- EnsureDirect

func TestEnsureDirectCreatesTheChat(t *testing.T) {
	svc, _ := newService(t)

	c, err := svc.EnsureDirect(ctx(), "u1", "u2")
	if err != nil {
		t.Fatal(err)
	}
	if c.Type != model.ChatDirect {
		t.Errorf("type = %q, want direct", c.Type)
	}
}

func TestEnsureDirectIsIdempotentOnTheUnorderedPair(t *testing.T) {
	// Both users can open the conversation at the same moment from either side;
	// two rows would split one conversation into two half-empty ones.
	svc, _ := newService(t)

	first, err := svc.EnsureDirect(ctx(), "u1", "u2")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.EnsureDirect(ctx(), "u2", "u1")
	if err != nil {
		t.Fatal(err)
	}

	if first.ID != second.ID {
		t.Errorf("the same pair produced two chats: %s and %s", first.ID, second.ID)
	}
}

func TestEnsureDirectSeedsBothMembers(t *testing.T) {
	svc, _ := newService(t)

	c, err := svc.EnsureDirect(ctx(), "u1", "u2")
	if err != nil {
		t.Fatal(err)
	}

	for _, user := range []string{"u1", "u2"} {
		member, err := svc.IsMember(ctx(), c.ID, user)
		if err != nil {
			t.Fatal(err)
		}
		if !member {
			t.Errorf("%s is not a member of the chat that was just created for them", user)
		}
	}
}

/*
 * The cache invalidation on create is what makes the first message work. Without
 * it the authorization view is populated before the membership rows exist, and
 * the very first send is refused for the length of the cache TTL — a bug that
 * only ever reproduces on a brand-new conversation.
 */
func TestEnsureDirectLetsTheFirstMessageThroughImmediately(t *testing.T) {
	svc, _ := newService(t)

	// Warm the cache on a chat that does not exist yet, as a speculative
	// authorization check would.
	_, _ = svc.CanPost(ctx(), "does-not-exist", "u1")

	c, err := svc.EnsureDirect(ctx(), "u1", "u2")
	if err != nil {
		t.Fatal(err)
	}

	can, err := svc.CanPost(ctx(), c.ID, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if !can {
		t.Error("the creator may not post to the chat that was just created")
	}
}

// ------------------------------------------------------------- FindDirect

func TestFindDirectReportsNotFoundRatherThanCreating(t *testing.T) {
	// The gateway rate-limits only NEW conversations, and it tells them apart by
	// this call. A FindDirect that created would make every lookup count as a
	// new chat and throttle ordinary messaging.
	svc, _ := newService(t)

	if _, err := svc.FindDirect(ctx(), "u1", "u2"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestFindDirectReturnsAnExistingChat(t *testing.T) {
	svc, _ := newService(t)
	created, err := svc.EnsureDirect(ctx(), "u1", "u2")
	if err != nil {
		t.Fatal(err)
	}

	found, err := svc.FindDirect(ctx(), "u1", "u2")
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != created.ID {
		t.Errorf("found %s, want %s", found.ID, created.ID)
	}
}

func TestFindDirectIgnoresPairOrder(t *testing.T) {
	svc, _ := newService(t)
	created, err := svc.EnsureDirect(ctx(), "u1", "u2")
	if err != nil {
		t.Fatal(err)
	}

	found, err := svc.FindDirect(ctx(), "u2", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != created.ID {
		t.Error("the reversed pair did not find the same chat")
	}
}

// ------------------------------------------------------------ CreateGroup

func TestCreateGroupSeedsTheOwnerAsOwner(t *testing.T) {
	// Every admin action in the chat is gated on this role; a creator seeded as
	// a plain member could not administer the group they just made.
	svc, _ := newService(t)

	c, err := svc.CreateGroup(ctx(), "owner", "Team", model.ChatGroup, nil)
	if err != nil {
		t.Fatal(err)
	}

	role, member, err := svc.MemberRole(ctx(), c.ID, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if !member || role != model.RoleOwner {
		t.Errorf("creator is %q (member=%v), want owner", role, member)
	}
}

func TestCreateGroupSeedsTheInitialMembers(t *testing.T) {
	svc, _ := newService(t)

	c, err := svc.CreateGroup(ctx(), "owner", "Team", model.ChatGroup, []string{"u2", "u3"})
	if err != nil {
		t.Fatal(err)
	}

	for _, user := range []string{"u2", "u3"} {
		role, member, err := svc.MemberRole(ctx(), c.ID, user)
		if err != nil {
			t.Fatal(err)
		}
		if !member || role != model.RoleMember {
			t.Errorf("%s is %q (member=%v), want member", user, role, member)
		}
	}
}

func TestCreateGroupKeepsTheOwnerRoleWhenListedAsAMember(t *testing.T) {
	/*
	 * A client that includes the creator in the member list would otherwise
	 * demote them to a plain member on the second `add` — losing control of the
	 * group at the moment of creating it. The dedup keeps the first role, which
	 * is the owner's.
	 */
	svc, _ := newService(t)

	c, err := svc.CreateGroup(ctx(), "owner", "Team", model.ChatGroup, []string{"owner", "u2"})
	if err != nil {
		t.Fatal(err)
	}

	role, _, err := svc.MemberRole(ctx(), c.ID, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if role != model.RoleOwner {
		t.Errorf("creator was demoted to %q by appearing in their own member list", role)
	}
}

func TestCreateGroupDeduplicatesRepeatedMembers(t *testing.T) {
	svc, _ := newService(t)

	c, err := svc.CreateGroup(ctx(), "owner", "Team", model.ChatGroup, []string{"u2", "u2", "u2"})
	if err != nil {
		t.Fatal(err)
	}

	members, err := svc.Members(ctx(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Errorf("chat has %d members, want 2 (owner + u2)", len(members))
	}
}

func TestCreateGroupRecordsTitleAndOwner(t *testing.T) {
	svc, _ := newService(t)

	c, err := svc.CreateGroup(ctx(), "owner", "Team", model.ChatGroup, nil)
	if err != nil {
		t.Fatal(err)
	}

	got, err := svc.Get(ctx(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Team" || got.OwnerID != "owner" {
		t.Errorf("chat = %+v", got)
	}
}

func TestCreateGroupCanCreateAChannel(t *testing.T) {
	svc, _ := newService(t)

	c, err := svc.CreateGroup(ctx(), "owner", "News", model.ChatChannel, nil)
	if err != nil {
		t.Fatal(err)
	}

	got, err := svc.Get(ctx(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != model.ChatChannel {
		t.Errorf("type = %q, want channel", got.Type)
	}
}

func TestCreateGroupStampsACreationTime(t *testing.T) {
	svc, _ := newService(t)

	c, err := svc.CreateGroup(ctx(), "owner", "Team", model.ChatGroup, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.CreatedAt == 0 {
		t.Error("chat has no creation time")
	}
}

// ----------------------------------------------------------------- CanPost

/*
 * A channel is the one chat type where membership is not permission: a million
 * subscribers may read and only admins may broadcast. Getting this wrong turns
 * every subscriber into a publisher, which is the difference between a channel
 * and a very large group.
 */
func TestChannelSubscribersMayNotPost(t *testing.T) {
	svc, _ := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "News", model.ChatChannel, []string{"reader"})
	if err != nil {
		t.Fatal(err)
	}

	can, err := svc.CanPost(ctx(), c.ID, "reader")
	if err != nil {
		t.Fatal(err)
	}
	if can {
		t.Error("a channel subscriber may broadcast")
	}
}

func TestChannelOwnerMayPost(t *testing.T) {
	svc, _ := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "News", model.ChatChannel, nil)
	if err != nil {
		t.Fatal(err)
	}

	can, err := svc.CanPost(ctx(), c.ID, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if !can {
		t.Error("a channel owner may not broadcast")
	}
}

func TestChannelAdminMayPost(t *testing.T) {
	svc, chats := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "News", model.ChatChannel, []string{"editor"})
	if err != nil {
		t.Fatal(err)
	}
	promote(t, svc, chats, c.ID, "editor", model.RoleAdmin)

	can, err := svc.CanPost(ctx(), c.ID, "editor")
	if err != nil {
		t.Fatal(err)
	}
	if !can {
		t.Error("a channel admin may not broadcast")
	}
}

func TestAnyGroupMemberMayPost(t *testing.T) {
	svc, _ := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "Team", model.ChatGroup, []string{"u2"})
	if err != nil {
		t.Fatal(err)
	}

	can, err := svc.CanPost(ctx(), c.ID, "u2")
	if err != nil {
		t.Fatal(err)
	}
	if !can {
		t.Error("a group member may not post")
	}
}

func TestANonMemberMayNotPost(t *testing.T) {
	svc, _ := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "Team", model.ChatGroup, nil)
	if err != nil {
		t.Fatal(err)
	}

	can, err := svc.CanPost(ctx(), c.ID, "stranger")
	if err != nil {
		t.Fatal(err)
	}
	if can {
		t.Error("a stranger may post to a chat they are not in")
	}
}

func TestPostingToAnUnknownChatIsRefused(t *testing.T) {
	/*
	 * An id that does not exist is exactly what a malicious client sends, so the
	 * property that matters is failing CLOSED: `can` must be false whatever the
	 * error says. The error itself is ErrNotFound rather than a bare refusal,
	 * which lets the gateway answer NOT_FOUND instead of FORBIDDEN — a more
	 * honest answer, and one that does not leak whether a chat exists to someone
	 * who is not in it (they get NOT_FOUND either way).
	 */
	svc, _ := newService(t)

	can, err := svc.CanPost(ctx(), "no-such-chat", "u1")

	if can {
		t.Error("posting to a chat that does not exist was allowed")
	}
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound so the gateway can map it", err)
	}
}

// ---------------------------------------------------------------- IsMember

func TestIsMemberReportsMembership(t *testing.T) {
	svc, _ := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "Team", model.ChatGroup, []string{"u2"})
	if err != nil {
		t.Fatal(err)
	}

	for user, want := range map[string]bool{"owner": true, "u2": true, "stranger": false} {
		got, err := svc.IsMember(ctx(), c.ID, user)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("IsMember(%s) = %v, want %v", user, got, want)
		}
	}
}

func TestIsMemberOfAnUnknownChatIsFalse(t *testing.T) {
	// Fails closed, and reports not-found so the caller can tell "no such chat"
	// apart from "not your chat".
	svc, _ := newService(t)

	got, err := svc.IsMember(ctx(), "no-such-chat", "u1")

	if got {
		t.Error("a user is a member of a chat that does not exist")
	}
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// ------------------------------------------------------------------ CanPin

/*
 * A pin is visible to everyone in the chat, so in a group it is an admin action.
 * A 1:1 chat has no hierarchy — both participants are equals — and requiring an
 * "admin" there would make pinning impossible for either of them.
 */
func TestEitherParticipantMayPinADirectChat(t *testing.T) {
	svc, _ := newService(t)
	c, err := svc.EnsureDirect(ctx(), "u1", "u2")
	if err != nil {
		t.Fatal(err)
	}

	for _, user := range []string{"u1", "u2"} {
		can, err := svc.CanPin(ctx(), c.ID, user)
		if err != nil {
			t.Fatal(err)
		}
		if !can {
			t.Errorf("%s may not pin in their own 1:1 chat", user)
		}
	}
}

func TestAPlainGroupMemberMayNotPin(t *testing.T) {
	svc, _ := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "Team", model.ChatGroup, []string{"u2"})
	if err != nil {
		t.Fatal(err)
	}

	can, err := svc.CanPin(ctx(), c.ID, "u2")
	if err != nil {
		t.Fatal(err)
	}
	if can {
		t.Error("a plain member may pin for everyone in a group")
	}
}

func TestGroupAdminsAndOwnersMayPin(t *testing.T) {
	svc, chats := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "Team", model.ChatGroup, []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	promote(t, svc, chats, c.ID, "admin", model.RoleAdmin)

	for _, user := range []string{"owner", "admin"} {
		can, err := svc.CanPin(ctx(), c.ID, user)
		if err != nil {
			t.Fatal(err)
		}
		if !can {
			t.Errorf("%s may not pin", user)
		}
	}
}

func TestANonMemberMayNotPin(t *testing.T) {
	svc, _ := newService(t)
	c, err := svc.EnsureDirect(ctx(), "u1", "u2")
	if err != nil {
		t.Fatal(err)
	}

	can, err := svc.CanPin(ctx(), c.ID, "stranger")
	if err != nil {
		t.Fatal(err)
	}
	if can {
		t.Error("a stranger may pin in someone else's chat")
	}
}

// ------------------------------------------------------------- CanModerate

/*
 * Moderation is "may this person act on what SOMEBODY ELSE wrote". It is a
 * different question from CanPost, and the difference is invisible in exactly
 * one chat type — a channel, where posting is already admin-only. That is why
 * using CanPost in its place looked correct for as long as it did, and why the
 * group cases below are the ones that matter.
 */
func TestAPlainGroupMemberMayNotModerate(t *testing.T) {
	svc, _ := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "Team", model.ChatGroup, []string{"u2"})
	if err != nil {
		t.Fatal(err)
	}

	can, err := svc.CanModerate(ctx(), c.ID, "u2")
	if err != nil {
		t.Fatal(err)
	}
	if can {
		t.Error("a plain group member may act on another member's message")
	}
}

func TestGroupAdminsAndOwnersMayModerate(t *testing.T) {
	svc, chats := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "Team", model.ChatGroup, []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	promote(t, svc, chats, c.ID, "admin", model.RoleAdmin)

	for _, user := range []string{"owner", "admin"} {
		can, err := svc.CanModerate(ctx(), c.ID, user)
		if err != nil {
			t.Fatal(err)
		}
		if !can {
			t.Errorf("%s may not moderate", user)
		}
	}
}

func TestEitherParticipantMayModerateA1To1Chat(t *testing.T) {
	// Both shapes of 1:1 — a direct chat and a secret one — have two equals and
	// no admin. Looking for a role there would lock both participants out.
	svc, _ := newService(t)
	direct, err := svc.EnsureDirect(ctx(), "u1", "u2")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := svc.EnsureSecret(ctx(), "u3", "u4")
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ chatID, user string }{
		{direct.ID, "u1"}, {direct.ID, "u2"},
		{secret.ID, "u3"}, {secret.ID, "u4"},
	} {
		can, err := svc.CanModerate(ctx(), tc.chatID, tc.user)
		if err != nil {
			t.Fatal(err)
		}
		if !can {
			t.Errorf("%s may not moderate their own 1:1 chat", tc.user)
		}
	}
}

func TestAPlainChannelSubscriberMayNotModerate(t *testing.T) {
	svc, _ := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "News", model.ChatChannel, []string{"reader"})
	if err != nil {
		t.Fatal(err)
	}

	can, err := svc.CanModerate(ctx(), c.ID, "reader")
	if err != nil {
		t.Fatal(err)
	}
	if can {
		t.Error("a channel subscriber may moderate")
	}
}

func TestANonMemberMayNotModerate(t *testing.T) {
	svc, _ := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "Team", model.ChatGroup, nil)
	if err != nil {
		t.Fatal(err)
	}

	can, err := svc.CanModerate(ctx(), c.ID, "stranger")
	if err != nil {
		t.Fatal(err)
	}
	if can {
		t.Error("a stranger may moderate someone else's chat")
	}
}

// -------------------------------------------------------------- MemberRole

func TestMemberRoleReportsNotAMember(t *testing.T) {
	// The invite service branches on `ok`; a zero role returned as if it were
	// real would read as "member" and grant access.
	svc, _ := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "Team", model.ChatGroup, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, ok, err := svc.MemberRole(ctx(), c.ID, "stranger")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("a stranger was reported as a member")
	}
}

// ------------------------------------------------------- CountMembersWithRole

func TestCountMembersWithRoleCountsOwners(t *testing.T) {
	// "Is this the last owner?" is asked as a count so that leaving a group does
	// not have to enumerate a million members to answer it.
	svc, chats := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "Team", model.ChatGroup, []string{"u2", "u3"})
	if err != nil {
		t.Fatal(err)
	}

	owners, err := svc.CountMembersWithRole(ctx(), c.ID, model.RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	if owners != 1 {
		t.Errorf("owners = %d, want 1", owners)
	}

	promote(t, svc, chats, c.ID, "u2", model.RoleOwner)
	owners, err = svc.CountMembersWithRole(ctx(), c.ID, model.RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	if owners != 2 {
		t.Errorf("owners = %d after a promotion, want 2", owners)
	}
}

func TestCountMembersWithRoleCountsPlainMembers(t *testing.T) {
	svc, _ := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "Team", model.ChatGroup, []string{"u2", "u3"})
	if err != nil {
		t.Fatal(err)
	}

	members, err := svc.CountMembersWithRole(ctx(), c.ID, model.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	if members != 2 {
		t.Errorf("members = %d, want 2", members)
	}
}

// ----------------------------------------------------------------- NextSeq

func TestNextSeqStartsAtOneAndIsGapFree(t *testing.T) {
	// Client-visible ordering is defined by this sequence, and history paging
	// walks it backwards — a gap would make a page look like it had lost rows.
	svc, _ := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "Team", model.ChatGroup, nil)
	if err != nil {
		t.Fatal(err)
	}

	for want := uint64(1); want <= 5; want++ {
		got, err := svc.NextSeq(ctx(), c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("seq = %d, want %d", got, want)
		}
	}
}

func TestNextSeqIsPerChat(t *testing.T) {
	// It is a per-chat position, not a global one; sharing a counter would leave
	// visible gaps in every chat.
	svc, _ := newService(t)
	first, err := svc.CreateGroup(ctx(), "owner", "A", model.ChatGroup, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateGroup(ctx(), "owner", "B", model.ChatGroup, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.NextSeq(ctx(), first.ID); err != nil {
		t.Fatal(err)
	}
	got, err := svc.NextSeq(ctx(), second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Errorf("a fresh chat started at seq %d, want 1", got)
	}
}

// --------------------------------------------------------------- AddMember

/*
 * The invalidation is the point. A user who joins through an invite gets an
 * immediate "you may not post here" otherwise — for exactly as long as the
 * cached view lives, which is long enough to look like the invite did not work.
 */
func TestAddMemberTakesEffectImmediately(t *testing.T) {
	svc, _ := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "Team", model.ChatGroup, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Warm the cache while they are still an outsider.
	if can, _ := svc.CanPost(ctx(), c.ID, "joiner"); can {
		t.Fatal("an outsider could already post")
	}

	if err := svc.AddMember(ctx(), &model.ChatMember{
		ChatID: c.ID, UserID: "joiner", Role: model.RoleMember,
	}); err != nil {
		t.Fatal(err)
	}

	can, err := svc.CanPost(ctx(), c.ID, "joiner")
	if err != nil {
		t.Fatal(err)
	}
	if !can {
		t.Error("a member who just joined may not post — the cache was not invalidated")
	}
}

func TestAddMemberRecordsTheRole(t *testing.T) {
	svc, _ := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "News", model.ChatChannel, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.AddMember(ctx(), &model.ChatMember{
		ChatID: c.ID, UserID: "editor", Role: model.RoleAdmin,
	}); err != nil {
		t.Fatal(err)
	}

	role, member, err := svc.MemberRole(ctx(), c.ID, "editor")
	if err != nil {
		t.Fatal(err)
	}
	if !member || role != model.RoleAdmin {
		t.Errorf("role = %q (member=%v), want admin", role, member)
	}
}

// ----------------------------------------------------------- SetMemberRole

/*
 * A demotion that lingers for a cache TTL is a security hole with a timer on it:
 * the admin who was just stripped of their rights keeps them for another minute,
 * which is exactly when they would use them.
 */
func TestDemotionTakesEffectImmediately(t *testing.T) {
	svc, _ := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "News", model.ChatChannel, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AddMember(ctx(), &model.ChatMember{
		ChatID: c.ID, UserID: "editor", Role: model.RoleAdmin,
	}); err != nil {
		t.Fatal(err)
	}

	// Warm the cache with the admin's broadcast rights.
	if can, _ := svc.CanPost(ctx(), c.ID, "editor"); !can {
		t.Fatal("the admin could not post before being demoted")
	}

	if err := svc.SetMemberRole(ctx(), c.ID, "editor", model.RoleMember); err != nil {
		t.Fatal(err)
	}

	can, err := svc.CanPost(ctx(), c.ID, "editor")
	if err != nil {
		t.Fatal(err)
	}
	if can {
		t.Error("a demoted admin may still broadcast — the cache was not invalidated")
	}
}

func TestPromotionTakesEffectImmediately(t *testing.T) {
	svc, _ := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "News", model.ChatChannel, []string{"reader"})
	if err != nil {
		t.Fatal(err)
	}
	if can, _ := svc.CanPost(ctx(), c.ID, "reader"); can {
		t.Fatal("a reader could already broadcast")
	}

	if err := svc.SetMemberRole(ctx(), c.ID, "reader", model.RoleAdmin); err != nil {
		t.Fatal(err)
	}

	can, err := svc.CanPost(ctx(), c.ID, "reader")
	if err != nil {
		t.Fatal(err)
	}
	if !can {
		t.Error("a newly promoted admin may not broadcast")
	}
}

func TestSetMemberRoleReportsNotFoundOnABackendWithoutRoles(t *testing.T) {
	// Role changes are an optional store capability; a backend without one has
	// to refuse rather than silently report success.
	ids, err := id.NewGenerator(1)
	if err != nil {
		t.Fatal(err)
	}
	svc := New(rolelessStore{}, ids)

	if err := svc.SetMemberRole(ctx(), "c1", "u1", model.RoleAdmin); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

// ----------------------------------------------------------- MemberIDs(Page)

func TestMemberIDsReturnsEveryone(t *testing.T) {
	svc, _ := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "Team", model.ChatGroup, []string{"u2", "u3"})
	if err != nil {
		t.Fatal(err)
	}

	ids, err := svc.MemberIDs(ctx(), c.ID)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	for _, want := range []string{"owner", "u2", "u3"} {
		if !got[want] {
			t.Errorf("%s missing from the member list %v", want, ids)
		}
	}
}

func TestMemberIDsOfAnUnknownChatReportsNotFound(t *testing.T) {
	// Fanout calls this; an empty list returned as success would silently drop
	// the message instead of surfacing that the chat is gone.
	svc, _ := newService(t)

	ids, err := svc.MemberIDs(ctx(), "no-such-chat")

	if len(ids) != 0 {
		t.Errorf("got %v, want no members", ids)
	}
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestMemberIDsPageWalksByKeyset(t *testing.T) {
	// Fanout streams a large chat's members rather than materialising them, and
	// the cursor is the user id — an offset would skip or repeat rows as the
	// membership changes underneath the walk.
	svc, _ := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "Team", model.ChatGroup, []string{"u2", "u3", "u4"})
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	after := ""
	for range 10 {
		page, err := svc.MemberIDsPage(ctx(), c.ID, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		for _, id := range page {
			if seen[id] {
				t.Fatalf("%s was returned twice while paging", id)
			}
			seen[id] = true
		}
		after = page[len(page)-1]
	}

	if len(seen) != 4 {
		t.Errorf("paged %d members, want 4: %v", len(seen), seen)
	}
}

// ----------------------------------------------------------------- Members

func TestMembersListsTheRoster(t *testing.T) {
	svc, _ := newService(t)
	c, err := svc.CreateGroup(ctx(), "owner", "Team", model.ChatGroup, []string{"u2"})
	if err != nil {
		t.Fatal(err)
	}

	members, err := svc.Members(ctx(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("roster has %d entries, want 2", len(members))
	}
	for _, m := range members {
		if m.ChatID != c.ID {
			t.Errorf("member %+v belongs to another chat", m)
		}
	}
}

// --------------------------------------------------------------------- Get

func TestGetReportsNotFoundForAnUnknownChat(t *testing.T) {
	svc, _ := newService(t)

	if _, err := svc.Get(ctx(), "no-such-chat"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

// ------------------------------------------------------------------ helpers

// promote changes a role through the store and clears the cached view, which is
// what the service itself does — so a test that needs an admin does not depend
// on SetMemberRole being correct.
func promote(t *testing.T, svc *Service, chats store.ChatStore, chatID, userID string, role model.MemberRole) {
	t.Helper()
	rs, ok := chats.(store.MemberRoleStore)
	if !ok {
		t.Skip("this store cannot change roles")
	}
	if err := rs.SetMemberRole(ctx(), chatID, userID, role); err != nil {
		t.Fatal(err)
	}
	svc.invalidate(chatID)
}

// rolelessStore is a ChatStore that does not implement store.MemberRoleStore.
type rolelessStore struct{ store.ChatStore }
