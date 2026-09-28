// Tests for the identity/session service. The properties asserted here are the
// ones a bug in would be invisible until it mattered: that the database never
// holds a usable credential, that revocation and expiry actually deny, and that
// an unknown username is indistinguishable from a wrong password.
package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/internal/store/memory"
	"github.com/SyncApp-chat/SyncApp/pkg/id"
)

func newSvc(t *testing.T) (*Service, store.SessionStore) {
	t.Helper()
	st := memory.New().Stores()
	ids, err := id.NewGenerator(7)
	if err != nil {
		t.Fatal(err)
	}
	return New(st.Users, st.Sessions, ids), st.Sessions
}

func mustRegister(t *testing.T, s *Service, user, pass string) string {
	t.Helper()
	sess, u, err := s.Register(context.Background(), user, pass, "", "dev1", "test")
	if err != nil {
		t.Fatalf("register %s: %v", user, err)
	}
	if sess.Token == "" {
		t.Fatal("register returned an empty token")
	}
	return u.ID
}

func TestRegisterThenAuthenticate(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	sess, u, err := s.Register(ctx, "Alice", "secret123", "Alice A", "dev1", "ios")
	if err != nil {
		t.Fatal(err)
	}
	// Usernames are normalized, so "Alice" and "alice" are one account.
	if u.Username != "alice" {
		t.Fatalf("username = %q, want lower-cased", u.Username)
	}
	if u.DisplayName != "Alice A" {
		t.Fatalf("display name = %q", u.DisplayName)
	}
	ident, err := s.Authenticate(ctx, sess.Token)
	if err != nil {
		t.Fatalf("authenticate with fresh token: %v", err)
	}
	if ident.User.ID != u.ID {
		t.Fatalf("authenticated as %s, want %s", ident.User.ID, u.ID)
	}
}

func TestRegisterDefaultsDisplayNameToUsername(t *testing.T) {
	s, _ := newSvc(t)
	_, u, err := s.Register(context.Background(), "bob", "secret123", "", "dev1", "android")
	if err != nil {
		t.Fatal(err)
	}
	if u.DisplayName != "bob" {
		t.Fatalf("display name = %q, want the username", u.DisplayName)
	}
}

func TestRegisterRejectsShortCredentials(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	for _, tc := range []struct{ user, pass string }{
		{"ab", "secret123"}, // username too short
		{"alice", "short"},  // password too short
		{"", ""},
	} {
		if _, _, err := s.Register(ctx, tc.user, tc.pass, "", "d", "test"); err == nil {
			t.Fatalf("register(%q,%q) succeeded, want rejection", tc.user, tc.pass)
		}
	}
}

func TestRegisterRejectsTakenUsername(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	mustRegister(t, s, "alice", "secret123")
	// Different case, same account — the normalization must not open a second one.
	_, _, err := s.Register(ctx, "ALICE", "secret123", "", "dev2", "test")
	if !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("duplicate register: got %v, want ErrUsernameTaken", err)
	}
}

func TestLoginWrongPasswordAndUnknownUserAreIndistinguishable(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	mustRegister(t, s, "alice", "secret123")

	_, _, wrongPass := s.Login(ctx, "alice", "not-the-password", "dev1", "test")
	_, _, noSuchUser := s.Login(ctx, "nobody", "not-the-password", "dev1", "test")

	if !errors.Is(wrongPass, ErrBadCredentials) {
		t.Fatalf("wrong password: got %v, want ErrBadCredentials", wrongPass)
	}
	// Same error for both, or the response itself reveals which usernames exist.
	if !errors.Is(noSuchUser, ErrBadCredentials) {
		t.Fatalf("unknown user: got %v, want ErrBadCredentials", noSuchUser)
	}
}

func TestLoginSucceedsAndIssuesADistinctSession(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	first, _, err := s.Register(ctx, "alice", "secret123", "", "dev1", "test")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := s.Login(ctx, "alice", "secret123", "dev2", "test")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if second.Token == first.Token {
		t.Fatal("a second login reused the first session's token")
	}
	// Multi-device: each device keeps its own session, so the first stays valid.
	if _, err := s.Authenticate(ctx, first.Token); err != nil {
		t.Fatalf("first session invalidated by a second login: %v", err)
	}
	if _, err := s.Authenticate(ctx, second.Token); err != nil {
		t.Fatalf("second session not usable: %v", err)
	}
}

// A database leak must not yield a usable bearer token: only the SHA-256 of a
// token is stored, never the token itself.
func TestStoredSessionHoldsNoUsableToken(t *testing.T) {
	s, sessions := newSvc(t)
	ctx := context.Background()
	sess, _, err := s.Register(ctx, "alice", "secret123", "", "dev1", "test")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := sessions.GetSessionByToken(ctx, hashToken(sess.Token))
	if err != nil {
		t.Fatalf("session not found by hashed token: %v", err)
	}
	if stored.Token == sess.Token {
		t.Fatal("plaintext bearer token stored at rest")
	}
	if stored.ResumeToken == sess.ResumeToken {
		t.Fatal("plaintext resume token stored at rest")
	}
	if stored.Token != hashToken(sess.Token) {
		t.Fatal("stored token is not the SHA-256 of the issued one")
	}
	// And the stored form must not itself authenticate.
	if _, err := s.Authenticate(ctx, stored.Token); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("the at-rest hash authenticated: %v", err)
	}
}

func TestPasswordHashIsArgon2idAndSalted(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	mustRegister(t, s, "alice", "samepassword")
	mustRegister(t, s, "bob", "samepassword")

	users := []string{"alice", "bob"}
	var hashes []string
	for _, name := range users {
		// Login proves the hash verifies; read it back to inspect its shape.
		if _, u, err := s.Login(ctx, name, "samepassword", "d", "test"); err != nil {
			t.Fatalf("login %s: %v", name, err)
		} else {
			hashes = append(hashes, u.PasswordHash)
		}
	}
	for i, h := range hashes {
		if !strings.HasPrefix(h, "argon2id$") {
			t.Fatalf("%s hash is not argon2id: %q", users[i], h)
		}
		if strings.Contains(h, "samepassword") {
			t.Fatalf("%s hash contains the plaintext password", users[i])
		}
	}
	// Identical passwords must produce different hashes, or the salt is not random.
	if hashes[0] == hashes[1] {
		t.Fatal("identical passwords produced identical hashes — salt is not random")
	}
}

func TestVerifyPasswordRejectsMalformedHashes(t *testing.T) {
	for _, encoded := range []string{
		"", "nonsense", "argon2id$onlytwo", "bcrypt$c2FsdA$aGFzaA",
		"argon2id$!!!notbase64!!!$aGFzaA",
		"argon2id$c2FsdA$!!!notbase64!!!",
	} {
		if verifyPassword("secret123", encoded) {
			t.Fatalf("malformed hash %q verified", encoded)
		}
	}
}

func TestResumeTokenReestablishesIdentity(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	sess, u, err := s.Register(ctx, "alice", "secret123", "", "dev1", "test")
	if err != nil {
		t.Fatal(err)
	}
	ident, err := s.Resume(ctx, sess.ResumeToken)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if ident.User.ID != u.ID {
		t.Fatalf("resumed as %s, want %s", ident.User.ID, u.ID)
	}
	// The two tokens are not interchangeable.
	if _, err := s.Resume(ctx, sess.Token); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("bearer token accepted as a resume token: %v", err)
	}
	if _, err := s.Authenticate(ctx, sess.ResumeToken); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("resume token accepted as a bearer token: %v", err)
	}
}

func TestGarbageTokensAreRejected(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	mustRegister(t, s, "alice", "secret123")
	for _, tok := range []string{"", "not-a-token", strings.Repeat("a", 64)} {
		if _, err := s.Authenticate(ctx, tok); !errors.Is(err, ErrInvalidSession) {
			t.Fatalf("Authenticate(%q): got %v, want ErrInvalidSession", tok, err)
		}
		if _, err := s.Resume(ctx, tok); !errors.Is(err, ErrInvalidSession) {
			t.Fatalf("Resume(%q): got %v, want ErrInvalidSession", tok, err)
		}
	}
}

// Revocation is the reason this service issues opaque tokens instead of JWTs, so
// it has to actually deny — on both the bearer and the resume path.
func TestRevokeDeniesBothTokens(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	sess, _, err := s.Register(ctx, "alice", "secret123", "", "dev1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, sess.Token); err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(ctx, sess.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := s.Authenticate(ctx, sess.Token); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("revoked session still authenticates: %v", err)
	}
	if _, err := s.Resume(ctx, sess.ResumeToken); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("revoked session still resumes — a lost device keeps access: %v", err)
	}
}

// Both token paths gate on validSession, so its rules are asserted directly
// rather than by reaching into a store's representation of a session.
func TestValidSessionRejectsRevokedAndExpired(t *testing.T) {
	past := time.Now().Add(-time.Hour).UnixMilli()
	future := time.Now().Add(time.Hour).UnixMilli()

	for name, tc := range map[string]struct {
		sess    model.Session
		wantErr bool
	}{
		"live":                {model.Session{ExpiresAt: future}, false},
		"no expiry":           {model.Session{ExpiresAt: 0}, false},
		"expired":             {model.Session{ExpiresAt: past}, true},
		"revoked":             {model.Session{ExpiresAt: future, RevokedAt: past}, true},
		"revoked and expired": {model.Session{ExpiresAt: past, RevokedAt: past}, true},
	} {
		t.Run(name, func(t *testing.T) {
			err := validSession(&tc.sess)
			if tc.wantErr && !errors.Is(err, ErrInvalidSession) {
				t.Fatalf("got %v, want ErrInvalidSession", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("got %v, want nil", err)
			}
		})
	}
}

func TestSessionCarriesAnExpiry(t *testing.T) {
	s, _ := newSvc(t)
	sess, _, err := s.Register(context.Background(), "alice", "secret123", "", "dev1", "test")
	if err != nil {
		t.Fatal(err)
	}
	// A session with no expiry never ages out, so one must always be set.
	if sess.ExpiresAt <= sess.CreatedAt {
		t.Fatalf("expires_at %d not after created_at %d", sess.ExpiresAt, sess.CreatedAt)
	}
	if got := sess.ExpiresAt - sess.CreatedAt; got != SessionTTL.Milliseconds() {
		t.Fatalf("session TTL = %d ms, want %d", got, SessionTTL.Milliseconds())
	}
}

// A device id is asserted by the client, so one account must not be able to seize
// another's device row — in particular clearing its push token. The squatter is
// handed a fresh id rather than an error, so the id space is not an existence
// oracle.
func TestDeviceIdCollisionAssignsAFreshIdInsteadOfFailing(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	alice, _, err := s.Register(ctx, "alice", "secret123", "", "shared-device", "ios")
	if err != nil {
		t.Fatal(err)
	}
	bob, _, err := s.Register(ctx, "bob", "secret123", "", "shared-device", "ios")
	if err != nil {
		t.Fatalf("second account claiming the same device id failed: %v", err)
	}
	if bob.DeviceID == alice.DeviceID {
		t.Fatal("second account took over the first account's device row")
	}
	// Alice's session is untouched and still usable.
	if _, err := s.Authenticate(ctx, alice.Token); err != nil {
		t.Fatalf("first account's session broken by the collision: %v", err)
	}
}

func TestEmptyDeviceIdGetsGenerated(t *testing.T) {
	s, _ := newSvc(t)
	sess, _, err := s.Register(context.Background(), "alice", "secret123", "", "", "web")
	if err != nil {
		t.Fatal(err)
	}
	if sess.DeviceID == "" {
		t.Fatal("no device id assigned when the client asked for none")
	}
}

func TestTokensAreHighEntropyAndDistinct(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok, err := randToken()
		if err != nil {
			t.Fatalf("randToken: %v", err)
		}
		// 256 bits base64url without padding.
		if len(tok) != 43 {
			t.Fatalf("token length = %d, want 43 (256 bits)", len(tok))
		}
		if seen[tok] {
			t.Fatalf("randToken repeated a value: %q", tok)
		}
		seen[tok] = true
	}
}

// The session TTL is a ROLLING window: using a session pushes its expiry out, so
// one in daily use never expires while one that goes quiet for the full TTL
// does. Without this, halving the TTL to 14 days would just mean everyone
// re-logs in every fortnight.
func TestAuthenticateRefreshesAnAgeingSession(t *testing.T) {
	svc, sessions := newSvc(t)
	sess, _, err := svc.Register(context.Background(), "roller", "secret123", "", "dev-roll", "cli")
	if err != nil {
		t.Fatal(err)
	}

	// Age the session past the refresh threshold without waiting a week for it.
	// TouchSession never moves an expiry backwards, so the row is rewritten
	// through the store's own revoke/create path instead: revoke the fresh one and
	// create a replacement that is already old.
	aged := time.Now().Add(SessionTTL - SessionRefreshAfter - time.Hour).UnixMilli()
	if err := sessions.RevokeSession(context.Background(), sess.ID, 1); err != nil {
		t.Fatal(err)
	}
	old := &model.Session{
		ID: sess.ID + "9", UserID: sess.UserID, DeviceID: sess.DeviceID,
		Token: hashToken("aged-token"), ResumeToken: hashToken("aged-resume"),
		CreatedAt: 1, ExpiresAt: aged,
	}
	if err := sessions.CreateSession(context.Background(), old); err != nil {
		t.Fatal(err)
	}

	id, err := svc.Authenticate(context.Background(), "aged-token")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if id.Session.ExpiresAt <= aged {
		t.Fatalf("expiry was not extended: %d <= %d", id.Session.ExpiresAt, aged)
	}
}

// A session nowhere near its expiry must not cause a write. Authentication runs
// on every connection, and a client that reconnects on each network change would
// otherwise turn the read path into a write path.
func TestAuthenticateDoesNotRefreshAFreshSession(t *testing.T) {
	svc, _ := newSvc(t)
	sess, _, err := svc.Register(context.Background(), "fresher", "secret123", "", "dev-fresh", "cli")
	if err != nil {
		t.Fatal(err)
	}
	before := sess.ExpiresAt

	id, err := svc.Authenticate(context.Background(), sess.Token)
	if err != nil {
		t.Fatal(err)
	}
	if id.Session.ExpiresAt != before {
		t.Fatalf("a fresh session was rewritten: %d != %d", id.Session.ExpiresAt, before)
	}
}
