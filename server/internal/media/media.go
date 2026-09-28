// Package media is the Media Service (Section 11). Large files never travel over
// the binary protocol — only a short media_ref does. Clients ask this service to
// begin an upload; it returns a short-lived, HMAC-signed URL to PUT the bytes to
// object storage, and later signed URLs to GET them. Access is gated by the
// signature (and, in production, a chat-membership check). Bytes live in an
// object store (filesystem locally, S3/GCS + CDN in production).
package media

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/SyncApp-chat/SyncApp/pkg/id"
)

// randSuffix returns 128 bits of URL-safe crypto-random for capability refs.
//
// The error is returned rather than discarded. The suffix is what makes a media
// ref unguessable, and access is gated on exactly that plus the signature — so a
// failing crypto/rand would produce a ref of zeros that anyone could construct,
// for a blob the signature would then happily authorise.
func randSuffix() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("media: cannot generate a reference: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Scan flags the EICAR signature.
//
// bytes.Contains rather than a hand-rolled loop. The previous version compared
// byte by byte from every offset, which is O(n*m) over a body that may be 100
// MiB — synchronously, inside the upload handler, holding the request open.
// The standard library's version uses the platform's optimised search, and the
// only reason to write this by hand would be to avoid an import.
func (HeuristicScanner) Scan(data []byte) error {
	if bytes.Contains(data, eicar) {
		return errors.New("media: rejected by malware scan")
	}
	return nil
}

// New builds the media service with the default heuristic (EICAR) scanner.
func New(store ObjectStore, ids *id.Generator, secret []byte, baseURL string) *Service {
	return &Service{
		store:   store,
		ids:     ids,
		secret:  secret,
		baseURL: strings.TrimRight(baseURL, "/"),
		ttl:     15 * time.Minute,
		maxSize: defaultMaxSize,
		scanner: HeuristicScanner{},
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// WithMaxSize sets the ABSOLUTE ceiling, which no tier may exceed.
//
// Distinct from the per-caller limit InitUpload takes: this one is the operator's
// answer to "how large a file is this deployment willing to store at all", while
// the entitlement is the product's answer to "how much does this account get".
// The smaller of the two wins, so raising a tier cannot outrun the disk.
func (s *Service) WithMaxSize(n int64) *Service {
	if n > 0 {
		s.maxSize = n
	}
	return s
}

// WithScanner overrides the malware scanner (e.g. a ClamAV client).
func (s *Service) WithScanner(sc Scanner) *Service { s.scanner = sc; return s }

// WithFetchAuthorizer gates downloads on something more than possession of the
// ref. Without one the service keeps its previous behaviour, so a deployment
// that has not wired the message log in is unchanged rather than broken.
func (s *Service) WithFetchAuthorizer(a FetchAuthorizer) *Service { s.auth = a; return s }

// InitUpload validates the request and returns a signed upload URL.
//
// limit is the CALLER's ceiling, from their entitlements; zero or negative means
// the caller's tier is unknown and only the deployment ceiling applies. The
// effective cap is the smaller of the two.
//
// The per-caller limit is a parameter rather than a field because it varies by
// account, and hard-coding it was a real defect rather than a simplification: the
// service capped every upload at 100 MiB, which is exactly what the FREE tier
// promises, so a Premium account was sold 4 GiB and refused at a hundredth of it.
//
// ErrTooLargeForTier is returned when the TIER is the binding constraint, so the
// gateway can answer with an upgrade prompt instead of a flat rejection. When the
// deployment ceiling is what refuses, no tier change would help and the error
// says so.
func (s *Service) InitUpload(userID, filename, contentType string, size, limit int64) (Ticket, error) {
	if size <= 0 {
		return Ticket{}, fmt.Errorf("media: size out of range (max %d)", s.maxSize)
	}
	if size > s.maxSize {
		return Ticket{}, fmt.Errorf("media: size out of range (max %d)", s.maxSize)
	}
	if limit > 0 && size > limit {
		return Ticket{}, fmt.Errorf("%w: %d bytes exceeds the %d allowed by your plan",
			ErrTooLargeForTier, size, limit)
	}
	// The ref is a capability token: a snowflake (ordering/debugging) plus 128
	// bits of crypto-random so it cannot be guessed or enumerated. Combined with
	// the signed URL this makes access sound even without a membership check;
	// production should ALSO verify the fetcher is a member of a chat where the
	// media was posted (defense in depth against a leaked ref).
	suffix, err := randSuffix()
	if err != nil {
		return Ticket{}, err
	}
	ref := "m" + s.ids.NextString() + "-" + suffix
	exp := time.Now().Add(s.ttl).Unix()
	// The declared size is part of what is signed, and the upload handler holds
	// the body to it. Otherwise the size is a suggestion: a ticket issued for a
	// one-kilobyte avatar would happily accept a hundred megabytes, and any quota
	// decided at InitUpload time would be decoration.
	sig := s.sign(ref, "put", size, exp)
	url := fmt.Sprintf("%s/media/upload/%s?exp=%d&sz=%d&sig=%s", s.baseURL, ref, exp, size, sig)
	return Ticket{MediaRef: ref, UploadURL: url, ExpiresAt: exp * 1000}, nil
}

// DownloadURL returns a signed URL to fetch a media_ref.
//
// Two gates, deliberately in this order. The ref itself carries 128 bits of
// entropy and the URL is HMAC-signed, which defeats guessing and enumeration —
// but says nothing about a ref that LEAKED (quoted in a screenshot, copied into
// a log, carried into a chat by a forward). The authorizer is the answer to
// that: possession stops being sufficient.
//
// Existence is checked first so an unauthorised request for a blob that is not
// there still reads as "not found" rather than as a denial, which would turn the
// ref space into an existence oracle.
func (s *Service) DownloadURL(userID, ref string) (url string, expiresAtMs int64, err error) {
	if !s.store.Exists(ref) {
		return "", 0, errors.New("media: not found")
	}
	if s.auth != nil {
		ok, err := s.auth.MayFetch(context.Background(), userID, ref)
		if err != nil {
			// Denied, not allowed: failing open would turn a database blip into an
			// access-control bypass.
			s.log.Warn("media fetch authorization failed", "user", userID, "ref", ref, "err", err)
			return "", 0, errors.New("media: not found")
		}
		if !ok {
			// Same message as a genuine miss, on purpose — telling a stranger that a
			// ref exists but is not theirs is itself a disclosure.
			return "", 0, errors.New("media: not found")
		}
	}
	exp := time.Now().Add(s.ttl).Unix()
	sig := s.sign(ref, "get", 0, exp)
	return fmt.Sprintf("%s/media/download/%s?exp=%d&sig=%s", s.baseURL, ref, exp, sig), exp * 1000, nil
}

// sign produces the HMAC signature for a (ref, op, size, expiry) tuple. size is
// meaningful for uploads and 0 for downloads; it is part of the signed material
// either way, so a download signature can never be replayed as an upload one.
func (s *Service) sign(ref, op string, size, exp int64) string {
	mac := hmac.New(sha256.New, s.secret)
	// hash.Hash.Write never returns an error, which is why the interface
	// documents it; the signature only has one because io.Writer does.
	_, _ = fmt.Fprintf(mac, "%s|%s|%d|%d", ref, op, size, exp)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// verify checks a signed URL's parameters (constant-time signature compare +
// expiry) and returns the signed size, which the caller enforces on the body.
func (s *Service) verify(ref, op, expStr, sizeStr, sig string) (int64, error) {
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return 0, errors.New("media: bad expiry")
	}
	var size int64
	if sizeStr != "" {
		if size, err = strconv.ParseInt(sizeStr, 10, 64); err != nil {
			return 0, errors.New("media: bad size")
		}
	}
	if time.Now().Unix() > exp {
		return 0, errors.New("media: url expired")
	}
	want := s.sign(ref, op, size, exp)
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return 0, errors.New("media: bad signature")
	}
	return size, nil
}
