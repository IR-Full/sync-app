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
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/pkg/id"
	"golang.org/x/crypto/argon2"
)

// New builds the auth service.
func New(users store.UserStore, sessions store.SessionStore, ids *id.Generator) *Service {
	return &Service{users: users, sessions: sessions, ids: ids}
}

// Register creates a user, its first device, and an initial session.
func (s *Service) Register(ctx context.Context, username, password, displayName, deviceID, platform string) (*model.Session, *model.User, error) {
	username = strings.TrimSpace(strings.ToLower(username))
	if len(username) < 3 || len(password) < 6 {
		return nil, nil, fmt.Errorf("%w: username>=3 password>=6", store.ErrConflict)
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

// Login verifies credentials and starts a new session on the given device.
func (s *Service) Login(ctx context.Context, username, password, deviceID, platform string) (*model.Session, *model.User, error) {
	username = strings.TrimSpace(strings.ToLower(username))
	u, err := s.users.GetUserByUsername(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		// Run a dummy verify to keep timing uniform (mitigates user enumeration).
		_ = verifyPassword(password, dummyHash)
		return nil, nil, ErrBadCredentials
	}
	if err != nil {
		return nil, nil, err
	}
	if !verifyPassword(password, u.PasswordHash) {
		return nil, nil, ErrBadCredentials
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
	plainToken := randToken()
	plainResume := randToken()
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
	u, err := s.users.GetUser(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}
	return &Identity{Session: sess, User: u}, nil
}

// Resume validates a resume token (used to re-establish a dropped connection
// without re-entering credentials).
func (s *Service) Resume(ctx context.Context, resumeToken string) (*Identity, error) {
	sess, err := s.sessions.GetSessionByResumeToken(ctx, hashToken(resumeToken))
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrInvalidSession
	}
	if err != nil {
		return nil, err
	}
	if err := validSession(sess); err != nil {
		return nil, err
	}
	u, err := s.users.GetUser(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}
	return &Identity{Session: sess, User: u}, nil
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
func randToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
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
// OOMing. Sized from SYNAPSE_AUTH_HASH_CONCURRENCY, default GOMAXPROCS.
var hashSem = newHashSem()

func newHashSem() chan struct{} {
	n := runtime.GOMAXPROCS(0)
	if v := os.Getenv("SYNAPSE_AUTH_HASH_CONCURRENCY"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			n = p
		}
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

// dummyHash is a precomputed valid-shape hash used to equalize login timing for
// unknown users. Value is irrelevant; only the work it forces matters.
var dummyHash, _ = hashPassword("timing-equalizer")
