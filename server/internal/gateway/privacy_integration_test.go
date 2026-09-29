package gateway_test

import (
	"testing"

	"github.com/IR-Full/sync-app/server/pkg/wire"
)

func privacyOf(t *testing.T, c *testClient, reqID uint64) wire.PrivacyBody {
	t.Helper()
	c.send(t, wire.MsgPrivacyGet, reqID, wire.PrivacyGetBody{})
	e := c.readUntil(t, wire.MsgPrivacy)
	var body wire.PrivacyBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		t.Fatalf("decode privacy: %v", err)
	}
	return body
}

/*
The default has to be "everyone".

Not because it is the best setting, but because it is what the system did before
these existed — a migration that silently tightens an account's visibility reads,
from the inside, as the app breaking.
*/
func TestPrivacyDefaultsToEveryone(t *testing.T) {
	addr := startGateway(t)
	c := connect(t, addr, "privdefault", "secret123")

	got := privacyOf(t, c, 1)
	if got.LastSeen != "everyone" || got.Avatar != "everyone" || got.Groups != "everyone" {
		t.Fatalf("defaults are not everyone: %+v", got)
	}
}

func TestPrivacyRoundTrips(t *testing.T) {
	addr := startGateway(t)
	c := connect(t, addr, "privset", "secret123")

	c.send(t, wire.MsgPrivacySet, 1, wire.PrivacySetBody{
		LastSeen: "nobody", Avatar: "contacts", Groups: "nobody",
	})
	echoed := func() wire.PrivacyBody {
		e := c.readUntil(t, wire.MsgPrivacy)
		var body wire.PrivacyBody
		_ = wire.Unmarshal(e.Body, &body)
		return body
	}()
	if echoed.LastSeen != "nobody" || echoed.Avatar != "contacts" {
		t.Fatalf("set echoed %+v", echoed)
	}

	// And it survives a read, which is what proves it reached the store rather
	// than just being reflected back.
	if got := privacyOf(t, c, 2); got.LastSeen != "nobody" || got.Groups != "nobody" {
		t.Fatalf("settings did not persist: %+v", got)
	}
}

/*
An unrecognised value is rejected on the WRITE path rather than normalised.

Storing "everyone" in place of something we do not understand is the one failure
mode that quietly widens visibility — the opposite of what the caller asked for.
*/
func TestPrivacyRejectsUnknownVisibility(t *testing.T) {
	addr := startGateway(t)
	c := connect(t, addr, "privbad", "secret123")

	for _, bad := range []wire.PrivacySetBody{
		{LastSeen: "friends-only", Avatar: "everyone", Groups: "everyone"},
		{LastSeen: "everyone", Avatar: "", Groups: "everyone"},
		{LastSeen: "everyone", Avatar: "everyone", Groups: "EVERYONE"},
	} {
		c.send(t, wire.MsgPrivacySet, 1, bad)
		e := c.readUntil(t, wire.MsgError)
		var eb wire.ErrorBody
		_ = wire.Unmarshal(e.Body, &eb)
		if eb.Code != wire.ErrBadArg {
			t.Fatalf("%+v was accepted (code %d)", bad, eb.Code)
		}
	}

	// Nothing was written: a rejected set must not half-apply.
	if got := privacyOf(t, c, 2); got.LastSeen != "everyone" {
		t.Fatalf("a rejected set changed the stored settings: %+v", got)
	}
}

/*
The avatar is gated; the name and handle are not.

Those two are how an account is addressed and recognised, so hiding them would
produce a conversation with a blank row rather than a private one — and the
handle is public by construction, since anyone can reach the account with it.
*/
func TestPrivacyHidesTheAvatarButNotTheName(t *testing.T) {
	addr := startGateway(t)
	owner := connect(t, addr, "privowner", "secret123")
	viewer := connect(t, addr, "privviewer", "secret123")

	owner.send(t, wire.MsgProfileSet, 1, wire.ProfileSetBody{
		DisplayName: "Owner Name", AvatarRef: "m123-abcdefghijklmnopqrstuv",
	})
	owner.readUntil(t, wire.MsgProfile)

	// Visible by default.
	viewer.send(t, wire.MsgProfileGet, 2, wire.ProfileGetBody{Target: owner.userID})
	before := func() wire.ProfileBody {
		e := viewer.readUntil(t, wire.MsgProfile)
		var p wire.ProfileBody
		_ = wire.Unmarshal(e.Body, &p)
		return p
	}()
	if before.AvatarRef == "" {
		t.Fatal("the avatar was hidden before any setting was made")
	}

	owner.send(t, wire.MsgPrivacySet, 3, wire.PrivacySetBody{
		LastSeen: "everyone", Avatar: "nobody", Groups: "everyone",
	})
	owner.readUntil(t, wire.MsgPrivacy)

	viewer.send(t, wire.MsgProfileGet, 4, wire.ProfileGetBody{Target: owner.userID})
	after := func() wire.ProfileBody {
		e := viewer.readUntil(t, wire.MsgProfile)
		var p wire.ProfileBody
		_ = wire.Unmarshal(e.Body, &p)
		return p
	}()
	if after.AvatarRef != "" {
		t.Fatalf("the avatar survived a 'nobody' setting: %q", after.AvatarRef)
	}
	if after.DisplayName != "Owner Name" || after.Username == "" {
		t.Fatalf("the name or handle was hidden too: %+v", after)
	}
}

// Your own attributes are always visible to you, whatever the setting says —
// otherwise the settings screen could not show what it had just saved.
func TestPrivacyNeverHidesYourOwnProfile(t *testing.T) {
	addr := startGateway(t)
	c := connect(t, addr, "privself", "secret123")

	c.send(t, wire.MsgProfileSet, 1, wire.ProfileSetBody{AvatarRef: "m999-abcdefghijklmnopqrstuv"})
	c.readUntil(t, wire.MsgProfile)
	c.send(t, wire.MsgPrivacySet, 2, wire.PrivacySetBody{
		LastSeen: "nobody", Avatar: "nobody", Groups: "nobody",
	})
	c.readUntil(t, wire.MsgPrivacy)

	c.send(t, wire.MsgProfileGet, 3, wire.ProfileGetBody{})
	e := c.readUntil(t, wire.MsgProfile)
	var mine wire.ProfileBody
	_ = wire.Unmarshal(e.Body, &mine)
	if mine.AvatarRef == "" {
		t.Fatal("a user cannot see their own avatar")
	}
}

/*
"Nobody may add me to groups" has to be enforced where the add happens.

It still leaves the account able to JOIN by link — the setting stops being
dragged in, not participating.
*/
func TestPrivacyRefusesAGroupAdd(t *testing.T) {
	addr := startGateway(t)
	target := connect(t, addr, "privtarget", "secret123")
	creator := connect(t, addr, "privcreator", "secret123")

	target.send(t, wire.MsgPrivacySet, 1, wire.PrivacySetBody{
		LastSeen: "everyone", Avatar: "everyone", Groups: "nobody",
	})
	target.readUntil(t, wire.MsgPrivacy)

	creator.send(t, wire.MsgChatCreate, 2, wire.ChatCreateBody{
		Type: "group", Title: "Unwanted", Members: []string{"@privtarget"},
	})
	e := creator.readUntil(t, wire.MsgError)
	var eb wire.ErrorBody
	_ = wire.Unmarshal(e.Body, &eb)
	if eb.Code != wire.ErrForbidden {
		t.Fatalf("want ErrForbidden, got %d (%q)", eb.Code, eb.Message)
	}
}

func TestPrivacyAllowsAGroupAddByDefault(t *testing.T) {
	addr := startGateway(t)
	_ = connect(t, addr, "privok", "secret123")
	creator := connect(t, addr, "privokcreator", "secret123")

	creator.send(t, wire.MsgChatCreate, 1, wire.ChatCreateBody{
		Type: "group", Title: "Wanted", Members: []string{"@privok"},
	})
	e := creator.readUntil(t, wire.MsgChatInfo)
	var info wire.ChatInfoBody
	_ = wire.Unmarshal(e.Body, &info)
	if info.ChatID == "" {
		t.Fatal("the group was not created")
	}
}
