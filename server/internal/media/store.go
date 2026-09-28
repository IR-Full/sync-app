package media

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// tmpPrefix names the in-flight uploads Put stages before linking them into
// place. Dot-prefixed so it sorts out of the way, and matched by ListOlderThan so
// a partial upload is never mistaken for a stored object.
const tmpPrefix = ".upload-"

// NewFSStore creates a filesystem object store rooted at dir.
func NewFSStore(dir string) (ObjectStore, error) {
	// 0700: only the server process user may traverse the media store. Blobs are
	// served through signed URLs, never by exposing the directory.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &fsStore{dir: dir}, nil
}

// path maps a ref to an on-disk path, collapsing it to a single filename first so
// no ref can ever escape f.dir. filepath.Base("../../etc/passwd") == "passwd", so
// traversal sequences are neutralized here — every file op below relies on this.
func (f *fsStore) path(ref string) string {
	safe := filepath.Base(ref)
	return filepath.Join(f.dir, safe)
}

// Put writes the object exactly once, and publishes it atomically.
//
// The bytes go to a temporary file first and are then hard-linked into place.
// Both properties this store needs fall out of that, without any lock:
//
//   - Create-only: os.Link fails with EEXIST if the ref is already taken, so two
//     concurrent uploads on one ticket cannot both succeed and the holder of a
//     still-valid signed PUT cannot replace bytes recipients already hold.
//   - No torn reads: the name appears only once the content is complete, so a
//     concurrent Get either misses the file or reads all of it. The earlier
//     version wrote in place under a store-wide RWMutex, which serialized every
//     read and write on the node to prevent exactly this.
func (f *fsStore) Put(ref string, data []byte) error {
	// 0600 on the temp file too: it lives in the same directory and briefly holds
	// the same bytes.
	tmp, err := os.CreateTemp(f.dir, tmpPrefix+"*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// #nosec G703 -- tmpName comes from os.CreateTemp inside f.dir, not from the request.
	defer func() { _ = os.Remove(tmpName) }() // no-op once linked; cleans up on any failure path
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	// Flush before publishing: a crash between link and flush would otherwise
	// expose a ref whose bytes are incomplete.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Link(tmpName, f.path(ref)); err != nil { // #nosec G703 -- path() collapses ref via filepath.Base
		if os.IsExist(err) {
			return ErrExists
		}
		return err
	}
	return nil
}

func (f *fsStore) Get(ref string) ([]byte, error) {
	return os.ReadFile(f.path(ref)) // #nosec G703 -- path() collapses ref via filepath.Base (no traversal)
}

// Delete removes an object; a missing one is already in the desired state.
func (f *fsStore) Delete(ref string) error {
	err := os.Remove(f.path(ref)) // #nosec G703 -- path() collapses ref via filepath.Base (no traversal)
	if err != nil && os.IsNotExist(err) {
		return nil
	}
	return err
}

// ListOlderThan enumerates stored refs last written before t. The filename IS
// the ref (path() collapses to a base name), so no mapping is needed.
func (f *fsStore) ListOlderThan(t time.Time) ([]string, error) {
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// An in-flight Put's temp file is not a ref, and handing it to the sweeper as
		// one would report a media object that no message can ever reference.
		if strings.HasPrefix(e.Name(), tmpPrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // vanished under us; the next sweep will see it or it is gone
		}
		if info.ModTime().Before(t) {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func (f *fsStore) Exists(ref string) bool {
	_, err := os.Stat(f.path(ref)) // #nosec G703 -- path() collapses ref via filepath.Base (no traversal)
	return err == nil
}
