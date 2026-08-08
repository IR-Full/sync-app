package media

import "sync"

// fsStore is a filesystem-backed ObjectStore for local dev. Production swaps in
// an S3/GCS implementation of the same interface fronted by a CDN.
type fsStore struct {
	dir string
	mu  sync.RWMutex
}
