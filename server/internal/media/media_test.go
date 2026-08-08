package media

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/synapse-chat/synapse/pkg/id"
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
	ticket, err := svc.InitUpload("user1", "note.txt", "text/plain", int64(len(payload)))
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
	ticket, err := svc.InitUpload("u", "virus.txt", "text/plain", int64(len(payload)))
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
	ticket, err := svc.InitUpload("user1", "note.txt", "text/plain", int64(len(payload)))
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
