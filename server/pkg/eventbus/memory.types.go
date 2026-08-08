package eventbus

import "sync"

// memoryBus is an in-process Bus for single-node dev and tests. Delivery is
// asynchronous (each subject fans out to its subscribers on goroutines) and
// at-least-once in spirit — handler errors are logged by the caller, not
// redelivered, which is acceptable for local runs. Queue groups are honored:
// within a group only one subscriber receives each event (round-robin).
type memoryBus struct {
	mu   sync.RWMutex
	subs map[string][]*subscription // key: subject pattern
}

type subscription struct {
	queue string
	h     Handler
}
