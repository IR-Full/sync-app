package sharded

import "github.com/synapse-chat/synapse/internal/store"

// --- Optional capabilities ---
//
// The message store is not one interface but a base plus optional capabilities,
// discovered by type assertion (threads, the self-destruct reaper, media
// reference checks). A decorator that implements only the base therefore does
// not merely lose a method — it makes the FEATURE disappear, silently, on the
// deployments that use the decorator. Sharding is turned on by an environment
// variable, so "self-destruct works" would have quietly depended on how many
// DSNs an operator listed.
//
// Each capability below is forwarded, and the assertions underneath fail the
// build if a future one is added to store.MessageStore's optional set and not
// forwarded here.
var (
	_ store.MessageStore    = (*MessageStore)(nil)
	_ store.ThreadReader    = (*MessageStore)(nil)
	_ store.Expirer         = (*MessageStore)(nil)
	_ store.MediaReferencer = (*MessageStore)(nil)
)
