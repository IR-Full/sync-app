package auth

import (
	"errors"
	"regexp"
	"time"
)

// SessionTTL is how long a session stays valid without being used.
//
// It was 30 days, which is a long time for a token that a lost phone keeps
// working. 14 is the compromise: the window a stolen token is useful for is
// halved, and nobody active is inconvenienced, because the TTL is now a
// ROLLING one — see SessionRefreshAfter.
const SessionTTL = 14 * 24 * time.Hour

// SessionRefreshAfter is how much of a session's life must elapse before using
// it extends it.
//
// A fixed expiry forces a re-login on a schedule that has nothing to do with
// whether the account is in use, so people either accept the friction or the TTL
// gets raised until it stops protecting anything. A rolling window is the usual
// answer: a session in daily use never expires, one that goes quiet for the full
// TTL does.
//
// The threshold exists so the refresh is not a database write on every
// authentication. At half the TTL a device that connects daily writes once a
// week, and a session can never be more than SessionTTL from its last use.
const SessionRefreshAfter = SessionTTL / 2

// Username bounds. A handle is addressed as "@name" throughout the protocol, so
// what it may contain is a correctness question, not a style one.
const (
	MinUsernameLen = 3
	MaxUsernameLen = 32
	MinPasswordLen = 6
)

/*
usernameRe bounds a user handle to an unambiguous ASCII alphabet.

Three distinct failures it prevents, in descending order of seriousness:

  - Look-alike impersonation. "bob" and "bоb" (Cyrillic о) are different handles
    that render identically. Someone reading a member list cannot tell them
    apart, which makes the handle useless as an identity — and this package
    already knew that, since internal/invite applies the same reasoning to
    public CHAT handles. It simply was never applied to user registration.
  - Unaddressable accounts. "@bob" passed registration and was stored verbatim.
    Addressing it means typing "@@bob", because every resolver does
    strings.TrimPrefix(ref, "@") — so "@bob" resolves to "bob", a different user
    or none. The account existed and could not be reached.
  - Unbounded length. There was no upper bound at all.

The alphabet deliberately mirrors what the clients already tell users is
allowed (`^[a-z0-9_.-]+$` in Android's CredentialRules, "letters and digits, no
@" in the web hint). Being stricter than the advertised rule would reject people
after telling them their name was fine.

Applied on REGISTRATION only. Login looks a handle up without re-validating, so
accounts created before this rule keep working.
*/
var usernameRe = regexp.MustCompile(`^[a-z0-9_.-]+$`)

var (
	// ErrBadCredentials is returned on wrong username/password.
	ErrBadCredentials = errors.New("auth: bad credentials")
	// ErrInvalidSession is returned when a token is unknown, expired, or revoked.
	ErrInvalidSession = errors.New("auth: invalid session")
	// ErrBadUsername is returned when a handle cannot be registered: wrong length
	// or a character outside the addressable alphabet. Distinct from
	// ErrBadCredentials on purpose — it is a malformed field, not a rejected
	// identity, and the two send the client to very different places.
	ErrBadUsername = errors.New("auth: invalid username")
	// ErrUsernameTaken is returned when registering an existing username.
	ErrUsernameTaken = errors.New("auth: username taken")
	// ErrResumeReplayed means a resume token that had already been rotated away
	// was presented again — so two parties held the chain, and it has been ended.
	//
	// Distinct from ErrInvalidSession because the right client response differs:
	// an invalid token means "log in again", while a replayed one means "log in
	// again, and something is wrong", which is worth surfacing to the user.
	ErrResumeReplayed = errors.New("auth: resume token was replayed; session ended")
)

const (
	argonTime    = 1
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 4
	argonKeyLen  = 32
	saltLen      = 16
)

// Recovery-code shape.
//
// Ten codes, because that is enough to survive losing a phone several times and
// few enough that the set stays worth protecting. Twelve characters in two groups
// of six: long enough that guessing is hopeless (base32 minus ambiguous glyphs is
// ~5 bits a character, so ~60 bits), short enough to copy off paper without
// losing your place.
const (
	recoveryCodeCount = 10
	recoveryCodeLen   = 12
	recoveryCodeBytes = 16
)
