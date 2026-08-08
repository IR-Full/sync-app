// Package media is the Media Service (Section 11). Large files never travel over
// the binary protocol — only a short media_ref does. Clients ask this service to
// begin an upload; it returns a short-lived, HMAC-signed URL to PUT the bytes to
// object storage, and later signed URLs to GET them. Access is gated by the
// signature (and, in production, a chat-membership check). Bytes live in an
// object store (filesystem locally, S3/GCS + CDN in production).
package media

import (
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

	"github.com/synapse-chat/synapse/pkg/id"
)

// randSuffix returns 128 bits of URL-safe crypto-random for capability refs.
func randSuffix() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Scan flags the EICAR signature.
func (HeuristicScanner) Scan(data []byte) error {
	if bytesContains(data, eicar) {
		return errors.New("media: rejected by malware scan")
	}
	return nil
}

func bytesContains(h, n []byte) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if string(h[i:i+len(n)]) == string(n) {
			return true
		}
	}
	return false
}

// New builds the media service with the default heuristic (EICAR) scanner.
func New(store ObjectStore, ids *id.Generator, secret []byte, baseURL string) *Service {
	return &Service{
		store:   store,
		ids:     ids,
		secret:  secret,
		baseURL: strings.TrimRight(baseURL, "/"),
		ttl:     15 * time.Minute,
		maxSize: 100 << 20, // 100 MiB
		scanner: HeuristicScanner{},
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// WithScanner overrides the malware scanner (e.g. a ClamAV client).
func (s *Service) WithScanner(sc Scanner) *Service { s.scanner = sc; return s }

// InitUpload validates the request and returns a signed upload URL.
func (s *Service) InitUpload(userID, filename, contentType string, size int64) (Ticket, error) {
	if size <= 0 || size > s.maxSize {
		return Ticket{}, fmt.Errorf("media: size out of range (max %d)", s.maxSize)
	}
	// The ref is a capability token: a snowflake (ordering/debugging) plus 128
	// bits of crypto-random so it cannot be guessed or enumerated. Combined with
	// the signed URL this makes access sound even without a membership check;
	// production should ALSO verify the fetcher is a member of a chat where the
	// media was posted (defense in depth against a leaked ref).
	ref := "m" + s.ids.NextString() + "-" + randSuffix()
	exp := time.Now().Add(s.ttl).Unix()
	// The declared size is part of what is signed, and the upload handler holds
	// the body to it. Otherwise the size is a suggestion: a ticket issued for a
	// one-kilobyte avatar would happily accept a hundred megabytes, and any quota
	// decided at InitUpload time would be decoration.
	sig := s.sign(ref, "put", size, exp)
	url := fmt.Sprintf("%s/media/upload/%s?exp=%d&sz=%d&sig=%s", s.baseURL, ref, exp, size, sig)
	return Ticket{MediaRef: ref, UploadURL: url, ExpiresAt: exp * 1000}, nil
}

// DownloadURL returns a signed URL to fetch a media_ref (the access check for
// chat membership is a production TODO; here any authenticated holder of the ref
// may fetch, protected by the unguessable ref + signature).
func (s *Service) DownloadURL(userID, ref string) (url string, expiresAtMs int64, err error) {
	if !s.store.Exists(ref) {
		return "", 0, errors.New("media: not found")
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
	fmt.Fprintf(mac, "%s|%s|%d|%d", ref, op, size, exp)
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
