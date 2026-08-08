package search

import "sync"

// memoryBackend is an in-process inverted index (single node / dev / tests).
type memoryBackend struct {
	mu       sync.RWMutex
	docs     map[string]*Doc
	inverted map[string]map[string]struct{}
	order    []string
}
