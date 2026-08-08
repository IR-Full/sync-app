package chat

import "time"

func nowMs() int64 { return time.Now().UnixMilli() }

// lessID orders two decimal snowflake ids NUMERICALLY. They travel as strings,
// and plain string order puts "9" after "10" — which would make a chat-list
// cursor skip rows the moment ids of different lengths coexist.
func lessID(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}
