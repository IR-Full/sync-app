package memory

import (
	"time"

	"github.com/IR-Full/sync-app/server/internal/model"
)

func nowMs() int64 { return time.Now().UnixMilli() }

// directKey builds the canonical key for a 1:1 chat from an unordered user pair.
func directKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

// pairKey namespaces the canonical-pair index by chat type.
//
// The direct form stays UNPREFIXED so every entry written before secret chats
// existed still resolves — prefixing the default would orphan every 1:1 chat to
// express a distinction the direct chat does not need.
func pairKey(typ model.ChatType, a, b string) string {
	if typ == model.ChatDirect || typ == "" {
		return directKey(a, b)
	}
	return string(typ) + ":" + directKey(a, b)
}
