// Package auth is the identity/session service (Sections 5). It owns
// registration, login, and session validation. Passwords are hashed with
// argon2id (a well-known, memory-hard KDF — no home-grown crypto). Session
// tokens are opaque 256-bit random values (recommended over JWT for the realtime
// path: instant server-side revocation, no key distribution, tiny on the wire;
// a short-lived JWT layer can be added later for stateless edge checks).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/envcfg"
	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/pkg/id"
	"golang.org/x/crypto/argon2"
)

// New builds the auth service.
func New(users store.UserStore, sessions store.SessionStore, ids *id.Generator) *Service {
	return &Service{users: users, sessions: sessions, ids: ids}
}

// WithTwoFactor enables the second factor.
//
// Optional rather than a constructor argument so every existing caller keeps
// compiling and keeps its current behaviour: no store, no second factor. That is
// where the system was, and it is a safe place for a deployment to stay until an
// operator configures SYNCAPP_TOTP_KEY.
func (s *Service) WithTwoFactor(tf store.TwoFactorStore) *Service {
	s.twoFactor = tf
	return s
}

// Register creates a user, its first device, and an initial session.
func (s *Service) Register(ctx context.Context, username, password, displayName, deviceID, platform string) (*model.Session, *model.User, error) {
	// The leading sigil is stripped rather than rejected: a user typing "@bob"
	// means the handle bob, and every client sends it either way.
	username = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(username)), "@")
	if err := ValidateUsername(username); err != nil {
		return nil, nil, err
	}
	if len(password) < MinPasswordLen {
		return nil, nil, fmt.Errorf("%w: password must be at least %d characters",
			store.ErrConflict, MinPasswordLen)
	}
	if displayName == "" {
		displayName = username
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, nil, err
	}
	u := &model.User{
		ID:           s.ids.NextString(),
		Username:     username,
		DisplayName:  displayName,
		PasswordHash: hash,
		CreatedAt:    nowMs(),
	}
	if err := s.users.CreateUser(ctx, u); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, nil, ErrUsernameTaken
		}
		return nil, nil, err
	}
	sess, err := s.startSession(ctx, u, deviceID, platform)
	if err != nil {
		return nil, nil, err
	}
	return sess, u, nil
}

// ValidateUsername reports whether a handle may be registered.
//
// Exported so the gateway can reject a bad one with ErrBadArg — "your name has
// a character we cannot address" is the client's mistake to fix, and reporting
// it as an authentication failure would send the app to a login screen it can
// never get past.
func ValidateUsername(username string) error {
	if len(username) < MinUsernameLen || len(username) > MaxUsernameLen {
		return fmt.Errorf("%w: username must be %d..%d characters",
			ErrBadUsername, MinUsernameLen, MaxUsernameLen)
	}
	if !usernameRe.MatchString(username) {
		return fmt.Errorf("%w: username may contain only a-z, 0-9, and _ . -",
			ErrBadUsername)
	}
	return nil
}

// Login verifies credentials and starts a new session on the given device.
//
// Deliberately does NOT re-validate the handle: accounts registered before the
// alphabet rule existed must keep working, and a login is a lookup of something
// already stored rather than a new claim on the namespace.
func (s *Service) Login(ctx context.Context, username, password, deviceID, platform string) (*model.Session, *model.User, error) {
	return s.LoginWithCode(ctx, username, password, "", deviceID, platform)
}

// LoginWithCode is Login with a second factor.
//
// The two-step shape matters and is easy to get wrong. The password is checked
// FIRST and its failure reported as ErrBadCredentials; only once it is correct
// does a missing code become ErrTwoFactorRequired. Checking the code first — or
// reporting "code required" before the password is verified — turns the second
// factor into an oracle for which accounts have one, and for whether a guessed
// password was right.
//
// The code is verified BEFORE a session exists, so a correct password with a
// wrong code produces no session at all. Issuing one and upgrading it later would
// mean a half-authenticated token, which is a thing nobody can reason about.
func (s *Service) LoginWithCode(ctx context.Context, username, password, code, deviceID, platform string) (*model.Session, *model.User, error) {
	username = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(username)), "@")
	u, err := s.users.GetUserByUsername(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		// Run a dummy verify to keep timing uniform (mitigates user enumeration).
		_ = verifyPassword(password, dummyHash())
		return nil, nil, ErrBadCredentials
	}
	if err != nil {
		return nil, nil, err
	}
	if !verifyPassword(password, u.PasswordHash) {
		return nil, nil, ErrBadCredentials
	}
	if s.TwoFactorEnabled(ctx, u.ID) {
		if code == "" {
			return nil, nil, ErrTwoFactorRequired
		}
		ok, err := s.verifySecondFactor(ctx, u.ID, code)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, ErrBadTOTPCode
		}
	}
	sess, err := s.startSession(ctx, u, deviceID, platform)
	if err != nil {
		return nil, nil, err
	}
	return sess, u, nil
}

// startSession registers the device (idempotent) and creates a session.
func (s *Service) startSession(ctx context.Context, u *model.User, deviceID, platform string) (*model.Session, error) {
	if deviceID == "" {
		deviceID = s.ids.NextString()
	}
	now := nowMs()
	dev := &model.Device{ID: deviceID, UserID: u.ID, Platform: platform, CreatedAt: now, LastSeen: now}
	if err := s.users.UpsertDevice(ctx, dev); err != nil {
		if !errors.Is(err, store.ErrConflict) {
			return nil, err
		}
		// The client asked for a device id that belongs to another account. Do not
		// fail the login — that would turn the id space into an existence oracle —
		// and do not take the row over. Assign a fresh id instead; the client is
		// told which one it got in AUTH_OK, exactly as when it asks for none.
		deviceID = s.ids.NextString()
		dev = &model.Device{ID: deviceID, UserID: u.ID, Platform: platform, CreatedAt: now, LastSeen: now}
		if err := s.users.UpsertDevice(ctx, dev); err != nil {
			return nil, err
		}
	}
	// Generate high-entropy tokens; store only their SHA-256 so a database leak
	// does not expose usable credentials. The plaintext is returned to the
	// caller exactly once, for delivery to the client.
	plainToken, err := randToken()
	if err != nil {
		return nil, err
	}
	plainResume, err := randToken()
	if err != nil {
		return nil, err
	}
	sess := &model.Session{
		ID:          s.ids.NextString(),
		UserID:      u.ID,
		DeviceID:    deviceID,
		Token:       hashToken(plainToken),
		ResumeToken: hashToken(plainResume),
		CreatedAt:   now,
		ExpiresAt:   now + SessionTTL.Milliseconds(),
	}
	if err := s.sessions.CreateSession(ctx, sess); err != nil {
		return nil, err
	}
	sess.Token = plainToken
	sess.ResumeToken = plainResume
	return sess, nil
}

// Authenticate validates a bearer token and returns the identity behind it.
func (s *Service) Authenticate(ctx context.Context, token string) (*Identity, error) {
	sess, err := s.sessions.GetSessionByToken(ctx, hashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrInvalidSession
	}
	if err != nil {
		return nil, err
	}
	if err := validSession(sess); err != nil {
		return nil, err
	}
	s.refresh(ctx, sess)
	u, err := s.users.GetUser(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}
	return &Identity{Session: sess, User: u}, nil
}

// refresh turns the session TTL into a ROLLING window: using a session pushes
// its expiry out, so one in daily use never expires while one that goes quiet
// for the full TTL does.
//
// Throttled by SessionRefreshAfter so this is not a database write on every
// connection — a client that reconnects on every network change would otherwise
// make authentication a write path.
//
// A failure is logged nowhere and ignored on purpose: the session is already
// valid, the caller is already authenticated, and refusing a login because an
// expiry could not be extended would turn a convenience into an outage. The
// worst case is that the session expires on its original schedule.
func (s *Service) refresh(ctx context.Context, sess *model.Session) {
	if sess.ExpiresAt == 0 {
		return // no expiry to extend
	}
	remaining := sess.ExpiresAt - nowMs()
	if remaining > (SessionTTL - SessionRefreshAfter).Milliseconds() {
		return // still fresh; nothing to do
	}
	next := nowMs() + SessionTTL.Milliseconds()
	if err := s.sessions.TouchSession(ctx, sess.ID, next); err == nil {
		// Reflect it in the value we hand back, so a caller that reports
		// expires_at (the session list does) does not show a stale one.
		sess.ExpiresAt = next
	}
}

// Resume validates a resume token and ROTATES it.
//
// The rotation is the security property, and its absence was the bug: a resume
// token used to survive unchanged for the session whole 14-day life, so a token
// captured once granted access for a fortnight — and its use was undetectable,
// because the legitimate client kept working right alongside whoever had it.
//
// Two things fall out of rotating:
//
//   - A stolen token is useful for ONE reconnect instead of a fortnight, because
//     the first party to use it consumes it.
//   - The theft becomes VISIBLE. A resume arriving for an already-consumed token
//     means two parties hold the chain, and the response is to end the chain
//     rather than guess which of them is the owner. That is the standard
//     refresh-token rotation response, and it is right here for the same reason:
//     the owner can re-authenticate with a password, while the thief cannot.
//
// The returned Identity carries the NEW token in Session.ResumeToken, for the
// caller to hand to the client.
func (s *Service) Resume(ctx context.Context, resumeToken string) (*Identity, error) {
	hashed := hashToken(resumeToken)
	sess, err := s.sessions.GetSessionByResumeToken(ctx, hashed)
	if errors.Is(err, store.ErrNotFound) {
		// Not a live token. Before concluding it is simply wrong, check whether it
		// is one that was ALREADY rotated away — that is not an unknown credential,
		// it is a replay, and the two deserve very different responses.
		if s.detectResumeReplay(ctx, hashed) {
			return nil, ErrResumeReplayed
		}
		return nil, ErrInvalidSession
	}
	if err != nil {
		return nil, err
	}
	if err := validSession(sess); err != nil {
		return nil, err
	}
	plainNext, err := randToken()
	if err != nil {
		return nil, err
	}
	if err := s.sessions.RotateResumeToken(ctx, sess.ID, hashed, hashToken(plainNext), nowMs()); err != nil {
		// Losing the compare-and-swap means another resume consumed this token
		// between the read and the write. That is the replay case arriving
		// concurrently rather than later, and it gets the same answer.
		if errors.Is(err, store.ErrNotFound) {
			_ = s.sessions.RevokeSession(ctx, sess.ID, nowMs())
			return nil, ErrResumeReplayed
		}
		return nil, err
	}
	u, err := s.users.GetUser(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}
	sess.ResumeToken = plainNext
	return &Identity{Session: sess, User: u}, nil
}

// detectResumeReplay looks for a session that already rotated this token away,
// and ends it if it finds one. Reports whether that happened.
//
// Ending rather than merely reporting: at this point two parties have held the
// same token and there is no way to tell from here which is the owner. Killing
// the session costs the owner one password entry and costs the thief everything,
// which is the only asymmetry available.
func (s *Service) detectResumeReplay(ctx context.Context, hashedToken string) bool {
	sess, err := s.sessions.GetSessionByConsumedResumeToken(ctx, hashedToken)
	if err != nil || sess == nil {
		return false
	}
	if sess.RevokedAt == 0 {
		_ = s.sessions.RevokeSession(ctx, sess.ID, nowMs())
	}
	return true
}

// Revoke invalidates a session immediately (logout / lost device).
func (s *Service) Revoke(ctx context.Context, sessionID string) error {
	return s.sessions.RevokeSession(ctx, sessionID, nowMs())
}

func validSession(sess *model.Session) error {
	if sess.RevokedAt != 0 {
		return ErrInvalidSession
	}
	if sess.ExpiresAt != 0 && nowMs() > sess.ExpiresAt {
		return ErrInvalidSession
	}
	return nil
}

func nowMs() int64 { return time.Now().UnixMilli() }

// randToken returns a URL-safe 256-bit random token.
//
// The error is returned rather than discarded, and that is the whole point: a
// failing crypto/rand (an exhausted fd table, a container with no entropy
// source) would otherwise leave the buffer zeroed and hand out a PREDICTABLE
// session token — which the rest of the flow would accept, because hashToken
// would simply hash the zeros. Failing the login is the only safe outcome.
func randToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: cannot generate a token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken is the at-rest form of a bearer token. SHA-256 is sufficient here
// (unlike passwords) because the input is already 256 bits of uniform entropy,
// so it is not brute-forceable — no need for a slow KDF.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// --- argon2id password hashing ---

// hashSem bounds concurrent argon2id operations. argon2id is memory-hard (64 MiB
// each) by design, so an unbounded auth flood — a login/registration storm —
// would exhaust RAM and CPU and take the whole process down. The semaphore caps
// concurrent hashes so auth degrades gracefully (requests queue) instead of
// OOMing. Sized from SYNCAPP_AUTH_HASH_CONCURRENCY, default GOMAXPROCS.
var hashSem = newHashSem()

func newHashSem() chan struct{} {
	n := runtime.GOMAXPROCS(0)
	if p := envcfg.Int("SYNCAPP_AUTH_HASH_CONCURRENCY", 0); p > 0 {
		n = p
	}
	return make(chan struct{}, n)
}

func argon2Key(password string, salt []byte, keyLen uint32) []byte {
	hashSem <- struct{}{}
	defer func() { <-hashSem }()
	return argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, keyLen)
}

// hashPassword returns an encoded argon2id hash: argon2id$<b64salt>$<b64hash>.
func hashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2Key(password, salt, argonKeyLen)
	return fmt.Sprintf("argon2id$%s$%s",
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// verifyPassword checks a password against an encoded hash in constant time.
func verifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 3 || parts[0] != "argon2id" {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[1])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[2])
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2Key(password, salt, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is a valid-shape hash used to equalize login timing for unknown
// users. The value is irrelevant; only the work it forces matters.
//
// Computed on first use rather than at package init. argon2id is configured for
// 64 MiB, and paying that during import meant every process that merely links
// this package — including each test binary — spent it at startup, whether or
// not anyone ever logged in.
var dummyHash = sync.OnceValue(func() string {
	h, err := hashPassword("timing-equalizer")
	if err != nil {
		// Unreachable short of a crypto/rand failure, and a constant of the right
		// SHAPE is enough: verifyPassword rejects it either way, and the only job
		// left is to burn comparable time.
		return "argon2id$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	}
	return h
})
