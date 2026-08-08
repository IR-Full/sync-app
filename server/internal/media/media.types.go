package media

import (
	"log/slog"
	"time"

	"github.com/synapse-chat/synapse/pkg/id"
)

// ObjectStore is the blob backend. fsStore (this package) is the local default;
// an S3 implementation slots in unchanged.
type ObjectStore interface {
	// Put stores the object. It MUST fail with ErrExists if ref is already
	// present, and must do so atomically (fs: O_EXCL; S3: If-None-Match).
	Put(ref string, data []byte) error
	Get(ref string) ([]byte, error)
	Exists(ref string) bool
	// Delete removes an object. A missing object is not an error: deletion is
	// idempotent, and the collector may race with itself across nodes.
	Delete(ref string) error
}

// Lister is an optional ObjectStore capability: enumerating stored objects by
// age, which the orphan sweep needs. A backend that cannot enumerate cheaply
// (or at all) simply does not implement it and is never swept.
type Lister interface {
	ListOlderThan(t time.Time) ([]string, error)
}

// Scanner is the anti-malware hook run on upload. The default HeuristicScanner
// rejects the EICAR test signature (a stand-in proving the hook works); a real
// deployment plugs in a ClamAV/ICAP scanner implementing this interface.
type Scanner interface {
	// Scan returns a non-nil error to reject (quarantine) the upload.
	Scan(data []byte) error
}

// HeuristicScanner is the default: it rejects the standard EICAR antivirus test
// string so the scan path is exercised without real AV infrastructure.
type HeuristicScanner struct{}

// Service issues upload/download tickets and validates their signatures.
type Service struct {
	store   ObjectStore
	ids     *id.Generator
	secret  []byte        // HMAC key for signing URLs
	baseURL string        // public base, e.g. http://localhost:8080
	ttl     time.Duration // ticket lifetime
	maxSize int64
	scanner Scanner    // anti-malware hook run on upload
	refs    Referencer // "is this blob still referenced?" (nil = never collect)
	log     *slog.Logger
}

// Ticket is a signed upload authorization.
type Ticket struct {
	MediaRef  string
	UploadURL string
	ExpiresAt int64
}
