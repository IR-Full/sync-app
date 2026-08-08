package media

import (
	"os"
	"path/filepath"
	"time"
)

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

// Put writes the object exactly once. O_EXCL makes "does it already exist?" and
// "create it" a single atomic step, so two concurrent uploads on one ticket
// cannot both succeed — the check-then-write version of this has a race that a
// retry storm would find.
func (f *fsStore) Put(ref string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	// 0600: readable only by the server process; access is mediated by signed URLs.
	// #nosec G703 -- path() collapses ref via filepath.Base (no traversal)
	fh, err := os.OpenFile(f.path(ref), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return ErrExists
		}
		return err
	}
	if _, err := fh.Write(data); err != nil {
		_ = fh.Close()
		return err
	}
	return fh.Close()
}

func (f *fsStore) Get(ref string) ([]byte, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return os.ReadFile(f.path(ref)) // #nosec G703 -- path() collapses ref via filepath.Base (no traversal)
}

// Delete removes an object; a missing one is already in the desired state.
func (f *fsStore) Delete(ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	err := os.Remove(f.path(ref)) // #nosec G703 -- path() collapses ref via filepath.Base (no traversal)
	if err != nil && os.IsNotExist(err) {
		return nil
	}
	return err
}

// ListOlderThan enumerates stored refs last written before t. The filename IS
// the ref (path() collapses to a base name), so no mapping is needed.
func (f *fsStore) ListOlderThan(t time.Time) ([]string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
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
	f.mu.RLock()
	defer f.mu.RUnlock()
	_, err := os.Stat(f.path(ref)) // #nosec G703 -- path() collapses ref via filepath.Base (no traversal)
	return err == nil
}
