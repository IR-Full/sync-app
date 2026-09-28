package media

import (
	"bytes"
	"testing"
)

// The scanner runs synchronously inside the upload handler, on a body that may
// be 100 MiB. The previous implementation compared byte by byte from every
// offset — O(n*m) — so this benchmark exists to keep anyone from reverting to
// a hand-rolled search on the grounds that it avoids an import.
func BenchmarkHeuristicScannerClean(b *testing.B) {
	// A worst case for a naive search: long runs that repeatedly match the first
	// byte of the needle and then fail.
	// Built from the real signature with its final byte changed, so the search
	// keeps finding near-matches and rejecting them.
	nearMiss := append(append([]byte{}, eicar[:len(eicar)-1]...), '?')
	payload := bytes.Repeat(nearMiss, 200_000)
	scanner := HeuristicScanner{}
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := scanner.Scan(payload); err != nil {
			b.Fatal("the payload is deliberately NOT eicar")
		}
	}
}

func TestHeuristicScannerFindsEicarAnywhere(t *testing.T) {
	scanner := HeuristicScanner{}
	for name, payload := range map[string][]byte{
		"at the start":  append(append([]byte{}, eicar...), bytes.Repeat([]byte("a"), 1000)...),
		"in the middle": append(append(bytes.Repeat([]byte("a"), 500), eicar...), bytes.Repeat([]byte("b"), 500)...),
		"at the end":    append(bytes.Repeat([]byte("a"), 1000), eicar...),
	} {
		if err := scanner.Scan(payload); err == nil {
			t.Errorf("%s: eicar was not detected", name)
		}
	}
}

func TestHeuristicScannerPassesCleanPayloads(t *testing.T) {
	scanner := HeuristicScanner{}
	// A near-miss: every byte of the signature except the last.
	almost := append([]byte{}, eicar[:len(eicar)-1]...)
	for name, payload := range map[string][]byte{
		"empty":     {},
		"short":     []byte("hello"),
		"near miss": almost,
	} {
		if err := scanner.Scan(payload); err != nil {
			t.Errorf("%s: a clean payload was rejected: %v", name, err)
		}
	}
}
