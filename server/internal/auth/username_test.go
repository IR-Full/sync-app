package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store/memory"
	"github.com/SyncApp-chat/SyncApp/pkg/id"
)

/*
A user handle is addressed as "@name" everywhere in the protocol, and every
resolver reaches it through strings.TrimPrefix(ref, "@") + GetUserByUsername. So
what a handle may contain is a correctness question: the alphabet is the only
thing standing between the namespace and look-alike impersonation, and the only
thing guaranteeing a registered account can actually be reached.
*/

func newAuthService(t *testing.T) *Service {
	t.Helper()
	ids, err := id.NewGenerator(1)
	if err != nil {
		t.Fatal(err)
	}
	st := memory.New().Stores()
	return New(st.Users, st.Sessions, ids)
}

func TestValidateUsernameAcceptsOrdinaryHandles(t *testing.T) {
	for _, name := range []string{"bob", "alice99", "a_b", "a.b", "a-b", "u123456"} {
		if err := ValidateUsername(name); err != nil {
			t.Errorf("%q was rejected: %v", name, err)
		}
	}
}

func TestValidateUsernameRejectsNonAscii(t *testing.T) {
	/*
	 * The reason the rule exists. "bob" and "bоb" — the second with a Cyrillic о —
	 * are different handles that render identically in every UI. A member list
	 * cannot distinguish them, which makes the handle worthless as an identity.
	 */
	for _, name := range []string{
		"bоb",   // Cyrillic о
		"аlice", // Cyrillic а
		"你好",
		"café",
		"bob\u200b", // zero-width space: invisible, and a different handle
	} {
		if err := ValidateUsername(name); !errors.Is(err, ErrBadUsername) {
			t.Errorf("%q was accepted; it renders like an ASCII handle", name)
		}
	}
}

func TestValidateUsernameRejectsTheSigil(t *testing.T) {
	// "@bob" used to register and then be unreachable: addressing it resolves to
	// "bob", which is a different account or none at all.
	for _, name := range []string{"@bob", "bo@b", "bob@"} {
		if err := ValidateUsername(name); !errors.Is(err, ErrBadUsername) {
			t.Errorf("%q was accepted; it could never be addressed back", name)
		}
	}
}

func TestValidateUsernameRejectsWhitespace(t *testing.T) {
	for _, name := range []string{"a b", "a\tb", "a\nb", "   "} {
		if err := ValidateUsername(name); !errors.Is(err, ErrBadUsername) {
			t.Errorf("%q was accepted", name)
		}
	}
}

func TestValidateUsernameEnforcesLength(t *testing.T) {
	if err := ValidateUsername("ab"); !errors.Is(err, ErrBadUsername) {
		t.Error("a two-character handle was accepted")
	}
	if err := ValidateUsername(strings.Repeat("a", MinUsernameLen)); err != nil {
		t.Errorf("a handle at the minimum length was rejected: %v", err)
	}
	if err := ValidateUsername(strings.Repeat("a", MaxUsernameLen)); err != nil {
		t.Errorf("a handle at the maximum length was rejected: %v", err)
	}
	// There was no upper bound at all before.
	if err := ValidateUsername(strings.Repeat("a", MaxUsernameLen+1)); !errors.Is(err, ErrBadUsername) {
		t.Error("a handle past the maximum length was accepted")
	}
}

func TestValidateUsernameRejectsUppercase(t *testing.T) {
	// Callers lowercase first. A rule that accepted uppercase would let "Bob" and
	// "bob" both through here and then collide in storage.
	if err := ValidateUsername("Bob"); !errors.Is(err, ErrBadUsername) {
		t.Error("an uppercase handle was accepted; it must be normalised first")
	}
}

func TestRegisterRejectsABadHandle(t *testing.T) {
	svc := newAuthService(t)

	_, _, err := svc.Register(context.Background(), "bоb", "secret123", "", "d1", "test")

	if !errors.Is(err, ErrBadUsername) {
		t.Fatalf("got %v, want ErrBadUsername", err)
	}
}

func TestRegisterStripsTheSigilRatherThanRejectingIt(t *testing.T) {
	// A user typing "@bob" means the handle bob, and the clients send it both
	// ways. Rejecting would be pedantic; storing "@bob" was the actual bug.
	svc := newAuthService(t)

	_, u, err := svc.Register(context.Background(), "@bob", "secret123", "", "d1", "test")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if u.Username != "bob" {
		t.Errorf("stored username = %q, want bob", u.Username)
	}
}

func TestRegisterNormalisesCaseAndSpace(t *testing.T) {
	svc := newAuthService(t)

	_, u, err := svc.Register(context.Background(), "  BoB  ", "secret123", "", "d1", "test")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if u.Username != "bob" {
		t.Errorf("stored username = %q, want bob", u.Username)
	}
}

func TestRegisterStillEnforcesPasswordLength(t *testing.T) {
	svc := newAuthService(t)

	if _, _, err := svc.Register(context.Background(), "bob", "short", "", "d1", "test"); err == nil {
		t.Fatal("a short password was accepted")
	}
}

func TestLoginDoesNotRevalidateTheHandle(t *testing.T) {
	/*
	 * The migration guarantee. Accounts registered before this rule existed may
	 * hold handles the rule would now reject; re-validating on login would lock
	 * every one of those people out of an account they still own.
	 */
	svc := newAuthService(t)
	ctx := context.Background()

	// Inserted straight through the store, bypassing Register — which is exactly
	// how such a row came to exist: written by a build whose Register had no
	// alphabet rule.
	hash, err := hashPassword("secret123")
	if err != nil {
		t.Fatal(err)
	}
	legacy := &model.User{
		ID:           "legacy-user",
		Username:     "bоb.legacy", // Cyrillic о: the rule would refuse this today
		DisplayName:  "Legacy",
		PasswordHash: hash,
		CreatedAt:    nowMs(),
	}
	if err := svc.users.CreateUser(ctx, legacy); err != nil {
		t.Fatal(err)
	}

	if _, _, err := svc.Login(ctx, "bоb.legacy", "secret123", "d1", "test"); err != nil {
		t.Errorf("a legacy handle could not log in: %v", err)
	}
}

func TestLoginAcceptsTheSigil(t *testing.T) {
	// Symmetry with Register: the clients send "@bob" from the same field.
	svc := newAuthService(t)
	ctx := context.Background()

	if _, _, err := svc.Register(ctx, "bob", "secret123", "", "d1", "test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Login(ctx, "@bob", "secret123", "d1", "test"); err != nil {
		t.Errorf("login with a sigil failed: %v", err)
	}
}
