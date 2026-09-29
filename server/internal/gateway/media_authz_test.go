package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/IR-Full/sync-app/server/internal/chat"
	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/store"
	"github.com/IR-Full/sync-app/server/internal/store/memory"
	"github.com/IR-Full/sync-app/server/pkg/id"
)

/*
Who may download a blob.

Before this gate, possession of the ref was the whole of the answer: 128 bits of
entropy plus an HMAC-signed URL, which defeats guessing and says nothing about a
ref that LEAKED — quoted in a screenshot, copied into a log, or carried into
another chat by a forward. These tests pin the three ways a fetch is legitimate
and the one way it is not.
*/

type authzFixture struct {
	authorizer mediaAuthorizer
	stores     store.Stores
	chats      *chat.Service
	ids        *id.Generator
}

func newAuthzFixture(t *testing.T) *authzFixture {
	t.Helper()
	ids, err := id.NewGenerator(1)
	if err != nil {
		t.Fatal(err)
	}
	st := memory.New().Stores()
	chatSvc := chat.New(st.Chats, ids)

	svc := &Services{
		Chat:       chatSvc,
		Users:      st.Users,
		MediaChats: st.Messages.(store.MediaChatResolver),
	}
	return &authzFixture{
		authorizer: mediaAuthorizer{svc: svc},
		stores:     st,
		chats:      chatSvc,
		ids:        ids,
	}
}

// postWithMedia puts a message carrying `ref` into `chatID`.
func (f *authzFixture) postWithMedia(t *testing.T, chatID, senderID, ref string) {
	t.Helper()
	m := &model.Message{
		ID:        f.ids.NextString(),
		ChatID:    chatID,
		SenderID:  senderID,
		MediaRef:  ref,
		CreatedAt: 1,
	}
	if _, _, err := f.stores.Messages.InsertMessage(context.Background(), m, f.ids.NextString(), nil); err != nil {
		t.Fatalf("insert: %v", err)
	}
}

func (f *authzFixture) may(t *testing.T, userID, ref string) bool {
	t.Helper()
	ok, err := f.authorizer.MayFetch(context.Background(), userID, ref)
	if err != nil {
		t.Fatalf("MayFetch: %v", err)
	}
	return ok
}

func TestAMemberMayFetchAChatsMedia(t *testing.T) {
	f := newAuthzFixture(t)
	ch, err := f.chats.CreateGroup(context.Background(), "owner", "Team", model.ChatGroup, []string{"member"})
	if err != nil {
		t.Fatal(err)
	}
	f.postWithMedia(t, ch.ID, "owner", "media-1")

	if !f.may(t, "member", "media-1") {
		t.Error("a member of the chat could not fetch its media")
	}
}

func TestAStrangerMayNotFetchAChatsMedia(t *testing.T) {
	// The point of the whole gate: a ref that leaked out of a chat stops being
	// enough on its own.
	f := newAuthzFixture(t)
	ch, err := f.chats.CreateGroup(context.Background(), "owner", "Team", model.ChatGroup, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.postWithMedia(t, ch.ID, "owner", "media-1")

	if f.may(t, "stranger", "media-1") {
		t.Error("someone outside the chat fetched its media with a leaked ref")
	}
}

func TestAForwardGrantsAccessInTheChatItLandedIn(t *testing.T) {
	/*
	 * A forward carries the ORIGINAL's ref into another chat, so one blob can
	 * legitimately live in several. Membership in any one of them is enough:
	 * somebody who can read the forward can already see the picture, and denying
	 * the fetch would render it as a broken image next to text they can read.
	 */
	f := newAuthzFixture(t)
	ctx := context.Background()

	origin, err := f.chats.CreateGroup(ctx, "owner", "Origin", model.ChatGroup, nil)
	if err != nil {
		t.Fatal(err)
	}
	elsewhere, err := f.chats.CreateGroup(ctx, "owner", "Elsewhere", model.ChatGroup, []string{"reader"})
	if err != nil {
		t.Fatal(err)
	}

	f.postWithMedia(t, origin.ID, "owner", "media-1")
	f.postWithMedia(t, elsewhere.ID, "owner", "media-1") // the forwarded copy

	if !f.may(t, "reader", "media-1") {
		t.Error("a reader of the forward could not fetch the blob it shows")
	}
}

func TestAnAvatarIsFetchableByAnyone(t *testing.T) {
	/*
	 * An avatar is reachable from no message at all, so the membership rule alone
	 * would deny every one of them — and a chat list draws dozens per screen. A
	 * profile is readable by anyone who can name the account, so the picture that
	 * goes with it carries no additional secret.
	 */
	f := newAuthzFixture(t)
	ctx := context.Background()

	u := &model.User{ID: "u1", Username: "alice", AvatarRef: "avatar-1", CreatedAt: 1}
	if err := f.stores.Users.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}

	if !f.may(t, "a-complete-stranger", "avatar-1") {
		t.Error("an avatar could not be fetched; every chat list would render blank")
	}
}

func TestAnUnreferencedBlobIsNotFetchable(t *testing.T) {
	// Nothing points at it, so nothing authorises it.
	f := newAuthzFixture(t)

	if f.may(t, "someone", "media-nobody-references") {
		t.Error("an unreferenced blob was fetchable")
	}
}

func TestAnEmptyUserOrRefIsRefused(t *testing.T) {
	// Both are the shape of a malformed request, and neither can identify anything
	// to authorise.
	f := newAuthzFixture(t)

	if f.may(t, "", "media-1") {
		t.Error("an unauthenticated fetch was allowed")
	}
	if f.may(t, "someone", "") {
		t.Error("a fetch with no ref was allowed")
	}
}

func TestAMissingResolverLeavesTheOldBehaviour(t *testing.T) {
	/*
	 * A backend that cannot resolve a blob to its chats keeps what shipped before
	 * this gate existed: the unguessable ref and the signed URL. Denying instead
	 * would take media away wholesale on a store that merely lacks a capability.
	 */
	ids, err := id.NewGenerator(1)
	if err != nil {
		t.Fatal(err)
	}
	st := memory.New().Stores()
	authorizer := mediaAuthorizer{svc: &Services{
		Chat:  chat.New(st.Chats, ids),
		Users: st.Users,
		// MediaChats deliberately nil.
	}}

	ok, err := authorizer.MayFetch(context.Background(), "someone", "media-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("a store without the capability started denying every fetch")
	}
}

// failingResolver stands in for a database that is down.
type failingResolver struct{}

func (failingResolver) MediaRefChats(context.Context, string, int) ([]string, error) {
	return nil, errors.New("database is down")
}

func TestAResolverFailureDeniesRatherThanAllows(t *testing.T) {
	/*
	 * Failing open here would turn a database blip into an access-control bypass —
	 * and one that nobody would notice, because the fetch succeeds. The media
	 * service maps the error to "not found", so the client retries rather than
	 * treating it as a permanent refusal.
	 */
	ids, err := id.NewGenerator(1)
	if err != nil {
		t.Fatal(err)
	}
	st := memory.New().Stores()
	authorizer := mediaAuthorizer{svc: &Services{
		Chat:       chat.New(st.Chats, ids),
		Users:      st.Users,
		MediaChats: failingResolver{},
	}}

	ok, err := authorizer.MayFetch(context.Background(), "someone", "media-1")

	if ok {
		t.Error("a failing resolver allowed the fetch")
	}
	if err == nil {
		t.Error("the failure was swallowed; the caller cannot tell it apart from a refusal")
	}
}
