package eventbus

import (
	"context"
	"strings"
)

// NewMemory returns an in-process event bus.
func NewMemory() Bus {
	return &memoryBus{subs: make(map[string][]*subscription)}
}

func (b *memoryBus) Subscribe(subject, queue string, h Handler) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[subject] = append(b.subs[subject], &subscription{queue: queue, h: h})
	return nil
}

func (b *memoryBus) Publish(ctx context.Context, e Event) error {
	b.mu.RLock()
	// Gather matching subscriptions grouped by queue.
	groups := map[string][]*subscription{}
	var direct []*subscription
	for pattern, list := range b.subs {
		if !match(pattern, e.Subject) {
			continue
		}
		for _, s := range list {
			if s.queue == "" {
				direct = append(direct, s)
			} else {
				groups[s.queue] = append(groups[s.queue], s)
			}
		}
	}
	b.mu.RUnlock()

	// Non-queue subscribers each get a copy. (Go 1.22+ scopes the loop var per
	// iteration, so capturing it in the goroutine is safe.)
	for _, s := range direct {
		go func() { _ = s.h(ctx, e) }()
	}
	// One member per queue group gets the event; pick by a stable hash of Key so
	// events for the same chat land on the same handler (ordering affinity).
	for _, list := range groups {
		s := list[hashKey(e.Key)%uint32(len(list))]
		go func() { _ = s.h(ctx, e) }()
	}
	return nil
}

func (b *memoryBus) Close() error { return nil }

// match implements NATS-style trailing-wildcard matching: "message.*" matches
// "message.created". A bare pattern matches its exact subject.
func match(pattern, subject string) bool {
	if pattern == subject {
		return true
	}
	if strings.HasSuffix(pattern, ".*") {
		return strings.HasPrefix(subject, pattern[:len(pattern)-1])
	}
	if pattern == "*" {
		return true
	}
	return false
}

func hashKey(s string) uint32 {
	// FNV-1a; small and dependency-free.
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	if h == 0 {
		return 1
	}
	return h
}
