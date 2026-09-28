package media

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func newStore(t *testing.T) ObjectStore {
	t.Helper()
	st, err := NewFSStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestFSStorePutIsCreateOnly(t *testing.T) {
	st := newStore(t)
	if err := st.Put("ref1", []byte("first")); err != nil {
		t.Fatal(err)
	}
	// A signed PUT stays valid for its whole TTL, so overwriting must be refused —
	// otherwise the holder could swap the bytes behind a ref recipients already have.
	if err := st.Put("ref1", []byte("second")); !errors.Is(err, ErrExists) {
		t.Fatalf("second Put: got %v, want ErrExists", err)
	}
	got, err := st.Get("ref1")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("first")) {
		t.Fatalf("content replaced: %q", got)
	}
}

// Exactly one of N concurrent uploads on one ticket may win, and the stored bytes
// must be one writer's payload in full — never a mix of two.
func TestFSStoreConcurrentPutOneWinner(t *testing.T) {
	st := newStore(t)
	const writers = 16
	payload := func(i int) []byte { return bytes.Repeat([]byte{byte('a' + i)}, 4096) }

	var wg sync.WaitGroup
	results := make([]error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = st.Put("contested", payload(i))
		}(i)
	}
	wg.Wait()

	won := 0
	for i, err := range results {
		switch {
		case err == nil:
			won++
		case errors.Is(err, ErrExists):
			// The losers of the race. Expected, and the point of the test.
		default:
			t.Fatalf("writer %d: unexpected error %v", i, err)
		}
	}
	if won != 1 {
		t.Fatalf("%d writers succeeded, want exactly 1", won)
	}

	stored, err := st.Get("contested")
	if err != nil {
		t.Fatal(err)
	}
	matched := false
	for i := 0; i < writers; i++ {
		if bytes.Equal(stored, payload(i)) {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("stored bytes match no single writer (len=%d) — a torn write", len(stored))
	}
}

// A reader racing a writer must never observe a partially written object: the ref
// appears only once its content is complete.
func TestFSStoreGetNeverSeesPartialWrite(t *testing.T) {
	st := newStore(t)
	want := bytes.Repeat([]byte("xyz"), 100_000)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := st.Put("big", want); err != nil {
			t.Errorf("put: %v", err)
		}
	}()

	for {
		got, err := st.Get("big")
		if err == nil {
			if !bytes.Equal(got, want) {
				t.Fatalf("read a partial object: %d of %d bytes", len(got), len(want))
			}
			break
		}
		select {
		case <-done:
			// Writer finished; one last read must succeed and be whole.
			got, err := st.Get("big")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("final read incomplete: %d of %d bytes", len(got), len(want))
			}
			return
		default:
		}
	}
	<-done
}

// The sweeper walks the directory, and an in-flight upload's temp file lives
// there too — it must not be reported as a stored ref.
func TestFSStoreListSkipsTempFiles(t *testing.T) {
	dir := t.TempDir()
	st, err := NewFSStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Put("real", []byte("data")); err != nil {
		t.Fatal(err)
	}
	lister, ok := st.(Lister)
	if !ok {
		t.Fatal("fsStore no longer implements Lister; the orphan sweep depends on it")
	}
	refs, err := lister.ListOlderThan(time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0] != "real" {
		t.Fatalf("listed %v, want [real] only", refs)
	}
	for _, r := range refs {
		if strings.HasPrefix(r, tmpPrefix) {
			t.Fatalf("temp file %q reported as a ref", r)
		}
	}
}

func TestFSStoreDeleteIsIdempotent(t *testing.T) {
	st := newStore(t)
	if err := st.Put("gone", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete("gone"); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete("gone"); err != nil {
		t.Fatalf("deleting a missing object: %v, want nil", err)
	}
	if st.Exists("gone") {
		t.Fatal("object still present after delete")
	}
}

// path() collapses a ref to a base name, so a traversal sequence cannot reach
// outside the store root.
func TestFSStoreRefCannotEscapeRoot(t *testing.T) {
	dir := t.TempDir()
	st, err := NewFSStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Put("../escaped", []byte("x")); err != nil {
		t.Fatal(err)
	}
	// It landed inside the store under the collapsed name, not one level up.
	if !st.Exists("escaped") {
		t.Fatal("traversal ref did not collapse into the store root")
	}
}
