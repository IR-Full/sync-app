package media

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/IR-Full/sync-app/server/pkg/id"
)

func TestMediaUploadDownloadRoundTrip(t *testing.T) {
	fs, err := NewFSStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ids, _ := id.NewGenerator(1)
	svc := New(fs, ids, []byte("test-secret"), "http://example")

	mux := http.NewServeMux()
	svc.RegisterHTTP(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	// Point signed URLs at the test server.
	svc.baseURL = srv.URL

	payload := []byte("hello media bytes")
	ticket, err := svc.InitUpload("user1", "note.txt", "text/plain", int64(len(payload)), 0)
	if err != nil {
		t.Fatal(err)
	}

	// Upload via the signed URL.
	req, _ := http.NewRequest(http.MethodPut, ticket.UploadURL, bytes.NewReader(payload))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status %d", resp.StatusCode)
	}

	// Download via a signed URL.
	dl, _, err := svc.DownloadURL("user1", ticket.MediaRef)
	if err != nil {
		t.Fatal(err)
	}
	resp2, err := http.Get(dl)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	got := make([]byte, len(payload))
	_, _ = resp2.Body.Read(got)
	if !bytes.Equal(got, payload) {
		t.Fatalf("download mismatch: %q", got)
	}
}

// TestDownloadRendersImagesAndNothingElse pins both halves of the same rule.
// An image has to be displayable — a client cannot render an avatar or a photo
// that arrives as an attachment — while everything else stays inert, because
// serving user-uploaded bytes inline from our own origin is stored XSS.
//
// The type is decided by the CONTENT, so the two cases below differ only in
// what the bytes actually are: the "image" that is really HTML must not be
// rendered no matter what it was uploaded as.
func TestDownloadRendersImagesAndNothingElse(t *testing.T) {
	fs, err := NewFSStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ids, _ := id.NewGenerator(1)
	svc := New(fs, ids, []byte("test-secret"), "http://example")

	mux := http.NewServeMux()
	svc.RegisterHTTP(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc.baseURL = srv.URL

	// A real 1x1 PNG, and a page of HTML dressed up as one.
	png := []byte{
		0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
		'I', 'H', 'D', 'R', 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89,
	}
	html := []byte("<html><script>alert(document.domain)</script></html>")

	cases := []struct {
		name       string
		filename   string
		declared   string
		payload    []byte
		wantType   string
		wantInline bool
	}{
		{"png", "avatar.png", "image/png", png, "image/png", true},
		// Declared as an image and named like one; only the bytes disagree.
		{"html posing as png", "avatar.png", "image/png", html, "application/octet-stream", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ticket, err := svc.InitUpload("user1", tc.filename, tc.declared, int64(len(tc.payload)), 0)
			if err != nil {
				t.Fatal(err)
			}
			req, _ := http.NewRequest(http.MethodPut, ticket.UploadURL, bytes.NewReader(tc.payload))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("upload status %d", resp.StatusCode)
			}

			dl, _, err := svc.DownloadURL("user1", ticket.MediaRef)
			if err != nil {
				t.Fatal(err)
			}
			got, err := http.Get(dl)
			if err != nil {
				t.Fatal(err)
			}
			defer got.Body.Close()

			if ct := got.Header.Get("Content-Type"); ct != tc.wantType {
				t.Fatalf("Content-Type = %q, want %q", ct, tc.wantType)
			}
			disposition := got.Header.Get("Content-Disposition")
			if tc.wantInline && disposition != "inline" {
				t.Fatalf("an image must be displayable, got Content-Disposition %q", disposition)
			}
			if !tc.wantInline && disposition != "attachment" {
				t.Fatalf("non-image served as %q — it must stay an attachment", disposition)
			}
			// nosniff holds either way: it is what stops the browser from
			// second-guessing the type we just decided.
			if got.Header.Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("nosniff missing")
			}
		})
	}
}

func TestMediaRejectsMalware(t *testing.T) {
	fs, _ := NewFSStore(t.TempDir())
	ids, _ := id.NewGenerator(1)
	svc := New(fs, ids, []byte("test-secret"), "http://example")
	mux := http.NewServeMux()
	svc.RegisterHTTP(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc.baseURL = srv.URL

	// A file containing the EICAR test signature must be rejected by the scanner.
	payload := append([]byte("prefix "), eicar...)
	ticket, err := svc.InitUpload("u", "virus.txt", "text/plain", int64(len(payload)), 0)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPut, ticket.UploadURL, bytes.NewReader(payload))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for malware, got %d", resp.StatusCode)
	}
	if svc.store.Exists(ticket.MediaRef) {
		t.Fatal("rejected file must not be stored")
	}
}

func TestMediaRejectsBadSignature(t *testing.T) {
	fs, _ := NewFSStore(t.TempDir())
	ids, _ := id.NewGenerator(1)
	svc := New(fs, ids, []byte("test-secret"), "http://example")
	mux := http.NewServeMux()
	svc.RegisterHTTP(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Forged URL with a bad signature must be rejected.
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/media/upload/mXYZ?exp=9999999999&sig=forged", bytes.NewReader([]byte("x")))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

// TestUploadTicketIsSingleUseAndSizeBound pins the two properties that make a
// signed upload URL a one-shot authorization rather than a 15-minute write
// permit: the declared size is signed and enforced, and the first write wins.
func TestUploadTicketIsSingleUseAndSizeBound(t *testing.T) {
	fs, err := NewFSStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ids, _ := id.NewGenerator(1)
	svc := New(fs, ids, []byte("test-secret"), "http://example")
	mux := http.NewServeMux()
	svc.RegisterHTTP(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc.baseURL = srv.URL

	payload := []byte("exactly this many bytes")
	ticket, err := svc.InitUpload("user1", "note.txt", "text/plain", int64(len(payload)), 0)
	if err != nil {
		t.Fatal(err)
	}

	put := func(url string, body []byte) int {
		req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	// A ticket for N bytes must not accept more than N.
	if code := put(ticket.UploadURL, bytes.Repeat([]byte("x"), len(payload)*4)); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload accepted with status %d", code)
	}
	// Nor fewer: the size is what was authorized, not a ceiling to sneak under.
	if code := put(ticket.UploadURL, payload[:3]); code != http.StatusBadRequest {
		t.Fatalf("undersized upload accepted with status %d", code)
	}
	if code := put(ticket.UploadURL, payload); code != http.StatusCreated {
		t.Fatalf("legitimate upload rejected with status %d", code)
	}
	// The URL is still unexpired — but the bytes behind a ref recipients may
	// already hold must not be replaceable.
	if code := put(ticket.UploadURL, bytes.Repeat([]byte("y"), len(payload))); code != http.StatusConflict {
		t.Fatalf("second upload on the same ticket returned %d, want 409", code)
	}
	data, err := fs.Get(ticket.MediaRef)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("stored bytes were replaced: %q", data)
	}
}

// stubRefs answers the collector's only question from a fixed set.
type stubRefs struct{ live map[string]bool }

func (s stubRefs) MediaRefExists(_ context.Context, ref string) (bool, error) {
	return s.live[ref], nil
}

// TestCollectorKeepsReferencedBlobs pins the rule that makes deletion safe: a
// forwarded message carries a COPY of the original's ref, so "the sender deleted
// their message" is not "these bytes are unreachable". The collector asks the
// message log before it removes anything.
func TestCollectorKeepsReferencedBlobs(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFSStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ids, _ := id.NewGenerator(1)
	live := stubRefs{live: map[string]bool{"still-forwarded": true}}
	svc := New(fs, ids, []byte("secret"), "http://example").WithReferencer(live)

	for _, ref := range []string{"still-forwarded", "last-copy-gone"} {
		if err := fs.Put(ref, []byte("bytes")); err != nil {
			t.Fatal(err)
		}
	}

	svc.DeleteIfUnreferenced(context.Background(), "message", "still-forwarded", "last-copy-gone")

	if !fs.Exists("still-forwarded") {
		t.Fatal("deleted a blob another message still points at")
	}
	if fs.Exists("last-copy-gone") {
		t.Fatal("unreferenced blob survived deletion")
	}
}

// TestSweepCollectsOrphansButSparesFreshUploads pins the age rule. An upload is
// unreferenced by definition between the PUT and the message that mentions it,
// so a sweep with no minimum age would delete users' files mid-send.
func TestSweepCollectsOrphansButSparesFreshUploads(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFSStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ids, _ := id.NewGenerator(1)
	svc := New(fs, ids, []byte("secret"), "http://example").
		WithReferencer(stubRefs{live: map[string]bool{"attached": true}})

	for _, ref := range []string{"attached", "orphan", "in-flight"} {
		if err := fs.Put(ref, []byte("bytes")); err != nil {
			t.Fatal(err)
		}
	}
	// Age everything except the in-flight upload past the grace window.
	old := time.Now().Add(-2 * gcMinAge)
	for _, ref := range []string{"attached", "orphan"} {
		if err := os.Chtimes(filepath.Join(dir, ref), old, old); err != nil {
			t.Fatal(err)
		}
	}

	n, err := svc.SweepOrphans(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("swept %d objects, want exactly the orphan", n)
	}
	if fs.Exists("orphan") {
		t.Fatal("orphaned upload survived the sweep")
	}
	if !fs.Exists("attached") {
		t.Fatal("swept a blob a live message references")
	}
	if !fs.Exists("in-flight") {
		t.Fatal("swept an upload young enough to still be mid-send")
	}
}

// TestTierCeilingIsWhatBounds covers the defect that made MaxUploadBytes
// decorative: the service capped every upload at its own constant, and that
// constant was 100 MiB — exactly the FREE tier's allowance. So a Premium account
// was told it had 4 GiB and refused at a hundredth of it.
func TestTierCeilingIsWhatBounds(t *testing.T) {
	fs, err := NewFSStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ids, _ := id.NewGenerator(1)
	svc := New(fs, ids, []byte("test-secret"), "http://example")

	const mib = 1 << 20
	free := int64(100 * mib)
	premium := int64(4 << 30)

	// The free ceiling refuses what is above it...
	if _, err := svc.InitUpload("u", "big.bin", "application/octet-stream", free+1, free); err == nil {
		t.Error("a file above the free allowance was accepted")
	} else if !errors.Is(err, ErrTooLargeForTier) {
		t.Errorf("refusal must be purchasable (ErrTooLargeForTier), got %v", err)
	}

	// ...and the SAME file is accepted for a caller whose tier allows it. This is
	// the assertion that fails on the old code, where the service constant bound
	// everyone regardless of plan.
	if _, err := svc.InitUpload("u", "big.bin", "application/octet-stream", free+1, premium); err != nil {
		t.Errorf("a Premium caller was refused the size their plan grants: %v", err)
	}
}

// TestDeploymentCeilingIsFinal pins the other direction: an operator's limit is
// not purchasable, so it must not answer with an upgrade prompt.
func TestDeploymentCeilingIsFinal(t *testing.T) {
	fs, err := NewFSStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ids, _ := id.NewGenerator(1)
	svc := New(fs, ids, []byte("test-secret"), "http://example").WithMaxSize(1 << 20)

	_, err = svc.InitUpload("u", "big.bin", "application/octet-stream", 2<<20, 4<<30)
	if err == nil {
		t.Fatal("a file above the deployment ceiling was accepted")
	}
	if errors.Is(err, ErrTooLargeForTier) {
		t.Error("the deployment ceiling was reported as a plan limit; no upgrade would lift it")
	}
}
