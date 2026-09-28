package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
)

type fakeUsers struct {
	store.UserStore
	user *model.User
	err  error
}

func (f fakeUsers) GetUser(context.Context, string) (*model.User, error) {
	return f.user, f.err
}

type fakeContacts struct {
	ContactService
	isContact bool
	err       error
}

func (f fakeContacts) IsContact(context.Context, string, string) (bool, error) {
	return f.isContact, f.err
}

func audienceFor(t *testing.T, v model.Visibility, contacts ContactService) interface {
	MaySeePresence(ctx context.Context, ownerID, viewerID string) (bool, error)
} {
	t.Helper()
	return NewPresenceAudience(Services{
		Users:    fakeUsers{user: &model.User{ID: "owner", Privacy: model.Privacy{LastSeen: v}}},
		Contacts: contacts,
	})
}

func TestPresenceAudienceEveryone(t *testing.T) {
	ok, err := audienceFor(t, model.VisibilityEveryone, nil).MaySeePresence(context.Background(), "owner", "viewer")
	if err != nil || !ok {
		t.Fatalf("everyone: ok=%v err=%v", ok, err)
	}
}

func TestPresenceAudienceNobody(t *testing.T) {
	ok, err := audienceFor(t, model.VisibilityNobody, nil).MaySeePresence(context.Background(), "owner", "viewer")
	if err != nil || ok {
		t.Fatalf("nobody: ok=%v err=%v", ok, err)
	}
}

// The direction of "contacts" is the part that is easy to invert: it is the
// OWNER's address book that decides. The other reading would let anyone grant
// themselves a view by adding the person they want to watch.
func TestPresenceAudienceContacts(t *testing.T) {
	yes := audienceFor(t, model.VisibilityContacts, fakeContacts{isContact: true})
	if ok, err := yes.MaySeePresence(context.Background(), "owner", "viewer"); err != nil || !ok {
		t.Fatalf("a contact was refused: ok=%v err=%v", ok, err)
	}

	no := audienceFor(t, model.VisibilityContacts, fakeContacts{isContact: false})
	if ok, err := no.MaySeePresence(context.Background(), "owner", "viewer"); err != nil || ok {
		t.Fatalf("a stranger was allowed: ok=%v err=%v", ok, err)
	}
}

// Without an address book "contacts" cannot be evaluated, and the user asked for
// a narrower audience than everyone — so the honest answer is no.
func TestPresenceAudienceDeniesContactsWithoutAnAddressBook(t *testing.T) {
	a := audienceFor(t, model.VisibilityContacts, nil)
	if ok, _ := a.MaySeePresence(context.Background(), "owner", "viewer"); ok {
		t.Fatal("contacts-only presence leaked with no contacts service")
	}
}

// You always see your own state, whatever the setting — otherwise a client could
// not render its own presence.
func TestPresenceAudienceAlwaysAllowsSelf(t *testing.T) {
	a := audienceFor(t, model.VisibilityNobody, nil)
	if ok, err := a.MaySeePresence(context.Background(), "owner", "owner"); err != nil || !ok {
		t.Fatalf("self was refused: ok=%v err=%v", ok, err)
	}
}

// A row written before these columns existed reads as the historical behaviour
// rather than as an error nobody can act on.
func TestPresenceAudienceTreatsAnUnknownSettingAsEveryone(t *testing.T) {
	a := audienceFor(t, model.Visibility("something-new"), nil)
	if ok, err := a.MaySeePresence(context.Background(), "owner", "viewer"); err != nil || !ok {
		t.Fatalf("an unknown setting was not defaulted: ok=%v err=%v", ok, err)
	}
}

// A deleted account has no presence to announce and nobody to announce it about.
func TestPresenceAudienceHandlesAMissingUser(t *testing.T) {
	a := NewPresenceAudience(Services{Users: fakeUsers{err: store.ErrNotFound}})
	ok, err := a.MaySeePresence(context.Background(), "gone", "viewer")
	if err != nil || ok {
		t.Fatalf("missing user: ok=%v err=%v", ok, err)
	}
}

// A real failure is reported rather than swallowed: fanout turns an error into
// "do not announce", which is the safe direction, but only if it hears about it.
func TestPresenceAudienceReportsAStoreFailure(t *testing.T) {
	boom := errors.New("database is down")
	a := NewPresenceAudience(Services{Users: fakeUsers{err: boom}})
	if _, err := a.MaySeePresence(context.Background(), "owner", "viewer"); !errors.Is(err, boom) {
		t.Fatalf("the store error was swallowed: %v", err)
	}
}
