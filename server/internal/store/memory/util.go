package memory

import "time"

func nowMs() int64 { return time.Now().UnixMilli() }

// directKey builds the canonical key for a 1:1 chat from an unordered user pair.
func directKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}
