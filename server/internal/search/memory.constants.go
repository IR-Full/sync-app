package search

// maxDocs bounds the in-memory index so it cannot grow without limit. Once full,
// the oldest indexed message is evicted (FIFO). Production uses the Postgres
// backend (shared across nodes) and drops this cap.
const maxDocs = 100_000
