package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/internal/store/memory"
	"github.com/SyncApp-chat/SyncApp/pkg/id"
	"github.com/SyncApp-chat/SyncApp/pkg/totp"
)

/*
The account-security suite.

Two of the three things tested here could not happen at all before: the password
could not be changed by any code path, and there was no second factor. The third —
resume-token rotation — existed as a token that never changed, which meant one
capture granted access for a fortnight and its use was invisible.
*/

func newSecuritySvc(t *testing.T) (*Service, *memory.Store) {
	t.Helper()
	// A key so the TOTP secret can be stored encrypted. Without one, enrolment is
	// REFUSED rather than falling back to plaintext — which is itself tested below.
	t.Setenv("SYNCAPP_TOTP_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	st := memory.New()
	gen, err := id.NewGenerator(1)
	if err != nil {
		t.Fatal(err)
	}
	return New(st, st, gen).WithTwoFactor(st), st
}

func register(t *testing.T, s *Service, username, password string) string {
	t.Helper()
	_, u, err := s.Register(context.Background(), username, password, "", "dev-1", "test")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return u.ID
}

// ---------------------------------------------------------------- password

// TestChangePasswordReplacesTheCredential is the headline: this was impossible.
// A leaked password meant a permanently lost account, because revoking sessions
// does not stop whoever knows the password from signing in again.
func TestChangePasswordReplacesTheCredential(t *testing.T) {
	s, _ := newSecuritySvc(t)
	ctx := context.Background()
	register(t, s, "pwalice", "oldsecret")

	sess, u, err := s.Login(ctx, "pwalice", "oldsecret", "dev-2", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangePassword(ctx, u.ID, "oldsecret", "newsecret", sess.ID); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	// The old password stops working — which is the entire point.
	if _, _, err := s.Login(ctx, "pwalice", "oldsecret", "dev-3", "test"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("the old password still logs in (err=%v)", err)
	}
	if _, _, err := s.Login(ctx, "pwalice", "newsecret", "dev-3", "test"); err != nil {
		t.Fatalf("the new password does not log in: %v", err)
	}
}

// TestChangePasswordRequiresTheOldOne: a session token is enough to ACT as the
// account but not enough to replace its credential. Otherwise a stolen token
// becomes permanent ownership, which is what a password change exists to undo.
func TestChangePasswordRequiresTheOldOne(t *testing.T) {
	s, _ := newSecuritySvc(t)
	ctx := context.Background()
	uid := register(t, s, "pwbob", "realsecret")

	if _, err := s.ChangePassword(ctx, uid, "guess", "newsecret", ""); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("a wrong old password was accepted (err=%v)", err)
	}
	// And the credential is untouched.
	if _, _, err := s.Login(ctx, "pwbob", "realsecret", "dev-2", "test"); err != nil {
		t.Fatalf("the failed change altered the password: %v", err)
	}
}

// TestChangePasswordSignsOutOtherSessionsButNotThisOne pins both halves of the
// revoke. A password change is nearly always a response to suspecting someone else
// has access, so leaving their sessions alive makes it cosmetic — while signing
// the user out of the device they are using to fix things is how they do not
// finish.
func TestChangePasswordSignsOutOtherSessionsButNotThisOne(t *testing.T) {
	s, _ := newSecuritySvc(t)
	ctx := context.Background()
	uid := register(t, s, "pwcarol", "oldsecret")

	var keep string
	for i, dev := range []string{"d1", "d2", "d3"} {
		sess, _, err := s.Login(ctx, "pwcarol", "oldsecret", dev, "test")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			keep = sess.ID
		}
	}
	live, err := s.ListSessions(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	before := len(live)

	n, err := s.ChangePassword(ctx, uid, "oldsecret", "newsecret", keep)
	if err != nil {
		t.Fatal(err)
	}
	if n != before-1 {
		t.Fatalf("revoked %d of %d sessions, want all but the caller", n, before)
	}
	after, _ := s.ListSessions(ctx, uid)
	if len(after) != 1 || after[0].ID != keep {
		t.Fatalf("expected only the caller session to survive, got %d", len(after))
	}
}

func TestChangePasswordRejectsWeakAndUnchanged(t *testing.T) {
	s, _ := newSecuritySvc(t)
	ctx := context.Background()
	uid := register(t, s, "pwdan", "oldsecret")

	if _, err := s.ChangePassword(ctx, uid, "oldsecret", "x", ""); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("a too-short password was accepted (err=%v)", err)
	}
	// Rejected rather than accepted as a no-op: someone who types their current
	// password into "new password" has misread the form, and silently succeeding
	// tells them the change happened.
	if _, err := s.ChangePassword(ctx, uid, "oldsecret", "oldsecret", ""); !errors.Is(err, ErrSamePassword) {
		t.Errorf("an unchanged password was accepted (err=%v)", err)
	}
}

// ---------------------------------------------------------------- 2FA

// TestTOTPEnrolmentIsTwoSteps pins the confirmation gate. A secret exists from the
// moment setup begins and must NOT be enforced before the user proves they can
// produce a code — otherwise a mis-scanned QR code locks the account out of itself,
// which is the most common way a 2FA rollout goes wrong.
func TestTOTPEnrolmentIsTwoSteps(t *testing.T) {
	s, _ := newSecuritySvc(t)
	ctx := context.Background()
	uid := register(t, s, "tfalice", "secret123")

	secret, uri, err := s.BeginTOTP(ctx, uid, "SyncApp")
	if err != nil {
		t.Fatalf("BeginTOTP: %v", err)
	}
	if secret == "" || uri == "" {
		t.Fatal("setup returned nothing to enrol with")
	}
	// Not enforced yet: a login still works on the password alone.
	if s.TwoFactorEnabled(ctx, uid) {
		t.Fatal("the factor is enforced before it was confirmed")
	}
	if _, _, err := s.Login(ctx, "tfalice", "secret123", "d2", "test"); err != nil {
		t.Fatalf("login broke during unconfirmed enrolment: %v", err)
	}

	codes, err := s.ConfirmTOTP(ctx, uid, mustCode(t, secret))
	if err != nil {
		t.Fatalf("ConfirmTOTP: %v", err)
	}
	if len(codes) != recoveryCodeCount {
		t.Fatalf("got %d recovery codes, want %d", len(codes), recoveryCodeCount)
	}
	if !s.TwoFactorEnabled(ctx, uid) {
		t.Fatal("the factor is not enforced after confirmation")
	}
}

func TestTOTPConfirmRejectsAWrongCode(t *testing.T) {
	s, _ := newSecuritySvc(t)
	ctx := context.Background()
	uid := register(t, s, "tfbob", "secret123")
	if _, _, err := s.BeginTOTP(ctx, uid, "SyncApp"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfirmTOTP(ctx, uid, "000000"); !errors.Is(err, ErrBadTOTPCode) {
		t.Fatalf("a wrong code enrolled the factor (err=%v)", err)
	}
	if s.TwoFactorEnabled(ctx, uid) {
		t.Fatal("a failed confirmation enabled the factor")
	}
}

// TestLoginRequiresTheSecondFactorAndReportsItDistinctly is the login-flow test.
//
// The order matters: the password is checked FIRST and its failure reported as
// ErrBadCredentials; only once it is right does a missing code become
// ErrTwoFactorRequired. Any other order makes the factor an oracle for which
// accounts have one, and for whether a guessed password was correct.
func TestLoginRequiresTheSecondFactorAndReportsItDistinctly(t *testing.T) {
	s, _ := newSecuritySvc(t)
	ctx := context.Background()
	uid := register(t, s, "tfcarol", "secret123")
	secret, _, _ := s.BeginTOTP(ctx, uid, "SyncApp")
	if _, err := s.ConfirmTOTP(ctx, uid, mustCode(t, secret)); err != nil {
		t.Fatal(err)
	}

	// Right password, no code: a challenge, not a rejection.
	if _, _, err := s.Login(ctx, "tfcarol", "secret123", "d2", "test"); !errors.Is(err, ErrTwoFactorRequired) {
		t.Fatalf("want ErrTwoFactorRequired, got %v", err)
	}
	// WRONG password: reported as bad credentials, NOT as a code challenge —
	// otherwise the response tells an attacker their password guess was right.
	if _, _, err := s.LoginWithCode(ctx, "tfcarol", "wrong", "", "d2", "test"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("a wrong password leaked the 2FA state: %v", err)
	}
	// Right password, wrong code: no session.
	if _, _, err := s.LoginWithCode(ctx, "tfcarol", "secret123", "000000", "d2", "test"); !errors.Is(err, ErrBadTOTPCode) {
		t.Fatalf("want ErrBadTOTPCode, got %v", err)
	}
	// Both right.
	if _, _, err := s.LoginWithCode(ctx, "tfcarol", "secret123", mustCode(t, secret), "d2", "test"); err != nil {
		t.Fatalf("a correct password and code did not log in: %v", err)
	}
}

// TestRecoveryCodeWorksOnceAndOnlyOnce is the lost-phone path, plus the property
// that makes it safe: single use.
func TestRecoveryCodeWorksOnceAndOnlyOnce(t *testing.T) {
	s, _ := newSecuritySvc(t)
	ctx := context.Background()
	uid := register(t, s, "tfdan", "secret123")
	secret, _, _ := s.BeginTOTP(ctx, uid, "SyncApp")
	codes, err := s.ConfirmTOTP(ctx, uid, mustCode(t, secret))
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.LoginWithCode(ctx, "tfdan", "secret123", codes[0], "d2", "test"); err != nil {
		t.Fatalf("a recovery code did not work: %v", err)
	}
	if left := s.RecoveryCodesLeft(ctx, uid); left != len(codes)-1 {
		t.Fatalf("%d codes left after spending one of %d", left, len(codes))
	}
	// The same code again must fail: a replayed one is exactly what an observer
	// would try.
	if _, _, err := s.LoginWithCode(ctx, "tfdan", "secret123", codes[0], "d3", "test"); !errors.Is(err, ErrBadTOTPCode) {
		t.Fatalf("a spent recovery code was accepted again (err=%v)", err)
	}
	// A different one still works.
	if _, _, err := s.LoginWithCode(ctx, "tfdan", "secret123", codes[1], "d3", "test"); err != nil {
		t.Fatalf("a second recovery code did not work: %v", err)
	}
}

// TestRecoveryCodeIsAcceptedHoweverItWasTranscribed: these get written on paper
// and typed back, so case, the separator and stray spaces must not matter. A code
// nobody can transcribe is not a recovery path.
func TestRecoveryCodeIsAcceptedHoweverItWasTranscribed(t *testing.T) {
	s, _ := newSecuritySvc(t)
	ctx := context.Background()
	uid := register(t, s, "tfeve", "secret123")
	secret, _, _ := s.BeginTOTP(ctx, uid, "SyncApp")
	codes, _ := s.ConfirmTOTP(ctx, uid, mustCode(t, secret))

	mangled := "  " + lower(codes[0]) + " "
	if _, _, err := s.LoginWithCode(ctx, "tfeve", "secret123", mangled, "d2", "test"); err != nil {
		t.Fatalf("a lower-cased, space-padded code was rejected: %v", err)
	}
}

// TestDisableTOTPNeedsPasswordAndCode: someone holding only a stolen session token
// must not be able to remove the factor that would keep them out of the next login.
func TestDisableTOTPNeedsPasswordAndCode(t *testing.T) {
	s, _ := newSecuritySvc(t)
	ctx := context.Background()
	uid := register(t, s, "tffrank", "secret123")
	secret, _, _ := s.BeginTOTP(ctx, uid, "SyncApp")
	if _, err := s.ConfirmTOTP(ctx, uid, mustCode(t, secret)); err != nil {
		t.Fatal(err)
	}

	if err := s.DisableTOTP(ctx, uid, "wrong", mustCode(t, secret)); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("disabled without the password (err=%v)", err)
	}
	if err := s.DisableTOTP(ctx, uid, "secret123", "000000"); !errors.Is(err, ErrBadTOTPCode) {
		t.Errorf("disabled without a code (err=%v)", err)
	}
	if !s.TwoFactorEnabled(ctx, uid) {
		t.Fatal("a failed disable removed the factor")
	}
	if err := s.DisableTOTP(ctx, uid, "secret123", mustCode(t, secret)); err != nil {
		t.Fatalf("DisableTOTP: %v", err)
	}
	if s.TwoFactorEnabled(ctx, uid) {
		t.Fatal("the factor survived a valid disable")
	}
}

// TestReEnrolIsRefusedWhileEnabled: silently replacing a working factor would let
// a stolen session swap it for one the thief controls.
func TestReEnrolIsRefusedWhileEnabled(t *testing.T) {
	s, _ := newSecuritySvc(t)
	ctx := context.Background()
	uid := register(t, s, "tfgina", "secret123")
	secret, _, _ := s.BeginTOTP(ctx, uid, "SyncApp")
	if _, err := s.ConfirmTOTP(ctx, uid, mustCode(t, secret)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.BeginTOTP(ctx, uid, "SyncApp"); !errors.Is(err, ErrTwoFactorEnabled) {
		t.Fatalf("re-enrolment over a live factor was allowed (err=%v)", err)
	}
}

// TestEnrolmentIsRefusedWithoutAnAtRestKey is the fail-closed case. Storing the
// secret in the clear because the operator forgot a key would make a database dump
// equivalent to a dump of everyone second factor — the one thing it must survive.
func TestEnrolmentIsRefusedWithoutAnAtRestKey(t *testing.T) {
	s, _ := newSecuritySvc(t)
	ctx := context.Background()
	uid := register(t, s, "tfhugo", "secret123")

	t.Setenv("SYNCAPP_TOTP_KEY", "")
	if _, _, err := s.BeginTOTP(ctx, uid, "SyncApp"); !errors.Is(err, ErrNoTOTPKey) {
		t.Fatalf("enrolment proceeded with no at-rest key (err=%v)", err)
	}
}

// TestSecretIsNotStoredInTheClear reads the stored row directly. The point of
// encrypting it is that a dump yields nothing, so the test looks at the dump.
func TestSecretIsNotStoredInTheClear(t *testing.T) {
	s, st := newSecuritySvc(t)
	ctx := context.Background()
	uid := register(t, s, "tfiris", "secret123")
	secret, _, err := s.BeginTOTP(ctx, uid, "SyncApp")
	if err != nil {
		t.Fatal(err)
	}

	tf, err := st.GetTwoFactor(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if tf.SecretEnc == secret {
		t.Fatal("the TOTP secret is stored verbatim")
	}
	if contains(tf.SecretEnc, secret) {
		t.Fatal("the stored value contains the plaintext secret")
	}
	// And it must still be recoverable with the key, or verification cannot work.
	key, err := totpKey()
	if err != nil {
		t.Fatal(err)
	}
	back, err := openSecret(key, tf.SecretEnc)
	if err != nil {
		t.Fatalf("the stored secret does not decrypt: %v", err)
	}
	if back != secret {
		t.Fatal("the stored secret decrypts to something else")
	}
}

// TestRecoveryCodesAreStoredHashed: unlike the TOTP secret these are never needed
// back, so the stored form must be a hash and a leak must yield nothing usable.
func TestRecoveryCodesAreStoredHashed(t *testing.T) {
	s, st := newSecuritySvc(t)
	ctx := context.Background()
	uid := register(t, s, "tfjane", "secret123")
	secret, _, _ := s.BeginTOTP(ctx, uid, "SyncApp")
	codes, err := s.ConfirmTOTP(ctx, uid, mustCode(t, secret))
	if err != nil {
		t.Fatal(err)
	}

	tf, err := st.GetTwoFactor(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	for _, stored := range tf.RecoveryHashes {
		for _, plain := range codes {
			if stored == plain || contains(stored, normalizeRecoveryCode(plain)) {
				t.Fatal("a recovery code is stored in a recoverable form")
			}
		}
		if !hasPrefix(stored, "argon2id$") {
			t.Fatalf("a recovery code is not argon2id-hashed: %q", stored)
		}
	}
}

// ---------------------------------------------------------------- resume

// TestResumeRotatesTheToken is the fix: an unrotated resume token granted access
// for the session whole 14-day life from a single capture.
func TestResumeRotatesTheToken(t *testing.T) {
	s, _ := newSecuritySvc(t)
	ctx := context.Background()
	register(t, s, "rsalice", "secret123")
	sess, _, err := s.Login(ctx, "rsalice", "secret123", "d2", "test")
	if err != nil {
		t.Fatal(err)
	}
	first := sess.ResumeToken

	ident, err := s.Resume(ctx, first)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if ident.Session.ResumeToken == "" {
		t.Fatal("resume returned no new token; the client has nothing to use next time")
	}
	if ident.Session.ResumeToken == first {
		t.Fatal("the resume token did not rotate")
	}
	// The new one works.
	if _, err := s.Resume(ctx, ident.Session.ResumeToken); err != nil {
		t.Fatalf("the rotated token does not resume: %v", err)
	}
}

// TestReplayingAResumeTokenEndsTheSession is the theft response.
//
// A resume for an already-consumed token means two parties hold the chain, and
// there is no way from here to tell which is the owner. Ending the session costs
// the owner one password entry and costs the thief everything, which is the only
// asymmetry available — and it is what makes the theft detectable at all.
func TestReplayingAResumeTokenEndsTheSession(t *testing.T) {
	s, _ := newSecuritySvc(t)
	ctx := context.Background()
	uid := register(t, s, "rsbob", "secret123")
	sess, _, err := s.Login(ctx, "rsbob", "secret123", "d2", "test")
	if err != nil {
		t.Fatal(err)
	}
	stolen := sess.ResumeToken

	ident, err := s.Resume(ctx, stolen)
	if err != nil {
		t.Fatal(err)
	}
	rotated := ident.Session.ResumeToken

	// The thief replays the token they captured.
	if _, err := s.Resume(ctx, stolen); !errors.Is(err, ErrResumeReplayed) {
		t.Fatalf("a replayed token was reported as %v, want ErrResumeReplayed", err)
	}
	// The session is gone — for BOTH of them. The owner re-authenticates; the
	// thief cannot.
	if _, err := s.Resume(ctx, rotated); err == nil {
		t.Fatal("the session survived a detected replay")
	}
	live, _ := s.ListSessions(ctx, uid)
	for _, l := range live {
		if l.ID == sess.ID {
			t.Fatal("the compromised session is still live")
		}
	}
}

// TestAnUnknownResumeTokenIsJustInvalid keeps the two cases apart. A token that
// was never valid is routine (an expired install, a typo in a test); one that was
// rotated away means someone else had it. Conflating them would either cry theft
// constantly or never.
func TestAnUnknownResumeTokenIsJustInvalid(t *testing.T) {
	s, _ := newSecuritySvc(t)
	if _, err := s.Resume(context.Background(), "never-issued"); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("an unknown token reported %v, want ErrInvalidSession", err)
	}
}

// TestTwoFactorIsUnavailableWithoutAStore checks the degradation. A deployment
// without the store has NO second factor, which is where the system was — not a
// half-built one that fails in confusing ways.
func TestTwoFactorIsUnavailableWithoutAStore(t *testing.T) {
	t.Setenv("SYNCAPP_TOTP_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	st := memory.New()
	gen, _ := id.NewGenerator(1)
	s := New(st, st, gen) // deliberately no WithTwoFactor
	ctx := context.Background()
	uid := register(t, s, "nostore", "secret123")

	if _, _, err := s.BeginTOTP(ctx, uid, "SyncApp"); !errors.Is(err, store.ErrUnsupported) {
		t.Errorf("BeginTOTP without a store returned %v", err)
	}
	if s.TwoFactorEnabled(ctx, uid) {
		t.Error("a factor is reported enabled with no store to hold it")
	}
	// And login still works on the password alone.
	if _, _, err := s.Login(ctx, "nostore", "secret123", "d2", "test"); err != nil {
		t.Errorf("login broke with no two-factor store: %v", err)
	}
}

// --- helpers ---

func mustCode(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.Code(secret, time.Now())
	if err != nil {
		t.Fatalf("totp.Code: %v", err)
	}
	return code
}

func contains(h, n string) bool {
	if n == "" {
		return false
	}
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
