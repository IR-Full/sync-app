package media

import (
	"errors"
	"io"
	"net/http"
	"strings"
)

// RegisterHTTP mounts the upload/download handlers on mux. These validate the
// HMAC-signed URL parameters minted by InitUpload/DownloadURL — the binary
// protocol carries only references, never bytes.
func (s *Service) RegisterHTTP(mux *http.ServeMux) {
	mux.HandleFunc("/media/upload/", s.handleUpload)
	mux.HandleFunc("/media/download/", s.handleDownload)
}

func (s *Service) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ref := strings.TrimPrefix(r.URL.Path, "/media/upload/")
	q := r.URL.Query()
	size, err := s.verify(ref, "put", q.Get("exp"), q.Get("sz"), q.Get("sig"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	// Read one byte past the signed size: anything longer is not the upload this
	// ticket authorized. The cap is the DECLARED size, not the global maximum, so
	// a ticket cannot be used to store more than it asked for.
	body, err := io.ReadAll(io.LimitReader(r.Body, size+1))
	if err != nil || int64(len(body)) > size {
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}
	if int64(len(body)) != size {
		http.Error(w, "size does not match the upload ticket", http.StatusBadRequest)
		return
	}
	// Anti-malware scan before the file is ever stored/fetchable (quarantine).
	if s.scanner != nil {
		if err := s.scanner.Scan(body); err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
	}
	if err := s.store.Put(ref, body); err != nil {
		// The ticket is still valid until it expires, so a second PUT is either a
		// retry or an attempt to swap the content behind a ref recipients already
		// hold. Either way the first write wins and this one is refused.
		if errors.Is(err, ErrExists) {
			http.Error(w, "already uploaded", http.StatusConflict)
			return
		}
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	// A production pipeline would now emit media.uploaded → virus scan, metadata,
	// thumbnail/transcode workers before the file is fetchable.
	// The ref is HMAC-verified (line above) and server-generated, but echo it back
	// as inert text/plain with nosniff so no client can ever render it as markup.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(ref)) // #nosec G705 -- HMAC-verified server ref, served as inert text/plain
}

func (s *Service) handleDownload(w http.ResponseWriter, r *http.Request) {
	ref := strings.TrimPrefix(r.URL.Path, "/media/download/")
	q := r.URL.Query()
	if _, err := s.verify(ref, "get", q.Get("exp"), "", q.Get("sig")); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	data, err := s.store.Get(ref)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// Serve user-uploaded bytes as an opaque, non-rendered download: octet-stream +
	// nosniff stops content-type sniffing, and attachment disposition stops inline
	// rendering — together they neutralize stored-XSS via a malicious upload.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "attachment")
	_, _ = w.Write(data) // #nosec G705 -- served as inert attachment (nosniff + octet-stream), never rendered
}
