package id

import "sync"

// Generator is a thread-safe Snowflake source bound to one node id.
type Generator struct {
	mu       sync.Mutex
	node     int64
	lastMs   int64
	sequence int64
}
