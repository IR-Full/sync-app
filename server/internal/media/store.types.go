package media

// fsStore is a filesystem-backed ObjectStore for local dev. Production swaps in
// an S3/GCS implementation of the same interface fronted by a CDN.
//
// It holds no lock: Put publishes by hard-linking a completed temp file, which
// makes creation atomic and create-only at the filesystem level, so nothing here
// needs to serialize reads against writes.
type fsStore struct {
	dir string
}
