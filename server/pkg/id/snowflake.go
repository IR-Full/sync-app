// Package id generates globally unique, time-sortable 64-bit identifiers using
// a Snowflake scheme. We pick Snowflake over ULID/UUIDv7 for message IDs because
// 64-bit ints are cheaper to index and store in Postgres/wide-column engines,
// sort by creation time, and encode the origin node for debugging — while still
// being collision-free across a sharded write tier without coordination.
//
// Layout (63 usable bits, sign bit kept 0 so IDs are positive):
//
//	 1 bit  unused (sign)
//	41 bits milliseconds since custom epoch (~69 years of range)
//	10 bits node id (0..1023 writer instances)
//	12 bits per-ms sequence (0..4095 ids/ms/node)
package id

import (
	"errors"
	"strconv"
	"time"
)

// NewGenerator returns a generator for the given node id (0..1023).
func NewGenerator(node int64) (*Generator, error) {
	if node < 0 || node > maxNode {
		return nil, errors.New("id: node out of range 0..1023")
	}
	return &Generator{node: node}, nil
}

// Next returns the next unique id. It spins to the next millisecond if the
// per-ms sequence space is exhausted, guaranteeing monotonicity per node.
func (g *Generator) Next() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now().UnixMilli()
	if now < g.lastMs {
		// Clock moved backwards; wait it out rather than emit a smaller id.
		now = g.lastMs
	}
	if now == g.lastMs {
		g.sequence = (g.sequence + 1) & maxSeq
		if g.sequence == 0 {
			// Sequence exhausted this ms; advance the clock.
			for now <= g.lastMs {
				now = time.Now().UnixMilli()
			}
		}
	} else {
		g.sequence = 0
	}
	g.lastMs = now

	return ((now - Epoch) << timeShift) | (g.node << nodeShift) | g.sequence
}

// NextString returns Next() as a base-10 string (the form used across the
// domain model and wire messages so ids stay opaque to clients).
func (g *Generator) NextString() string {
	return strconv.FormatInt(g.Next(), 10)
}

// TimeOf extracts the creation time encoded in an id.
func TimeOf(id int64) time.Time {
	ms := (id >> timeShift) + Epoch
	return time.UnixMilli(ms)
}
