// Package safego contains the goroutine boundary guard.
//
// In Go a panic in ANY goroutine terminates the whole process, not just that
// goroutine. For a gateway holding a million connections that is the difference
// between one client hitting a bug and every connected user being disconnected,
// so every goroutine that serves or is influenced by remote input is wrapped
// here. The guard is deliberately thin: it logs with a stack, counts the panic
// so it cannot pass silently, and returns — it never re-panics, because the
// caller's whole reason for existing is that this goroutine may die alone.
package safego

import (
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/IR-Full/sync-app/server/internal/metrics"
)

// Recover contains a panic at a goroutine boundary. Use as the first deferred
// call in any function running as its own goroutine:
//
//	go func() {
//		defer safego.Recover(log, "fanout.deliver")
//		...
//	}()
//
// site names the boundary and becomes a metric label, so it must be a low
// cardinality constant, never user input.
func Recover(log *slog.Logger, site string) {
	r := recover()
	if r == nil {
		return
	}
	metrics.PanicsRecovered.WithLabelValues(site).Inc()
	if log == nil {
		log = slog.Default()
	}
	log.Error("recovered panic", "site", site, "panic", r, "stack", string(debug.Stack()))
}

// Go runs fn in a new goroutine with Recover already installed. Preferred over
// a bare `go func()` for fire-and-forget work, so the guard cannot be forgotten.
func Go(log *slog.Logger, site string, fn func()) {
	go func() {
		defer Recover(log, site)
		fn()
	}()
}

// Loop runs a long-lived loop in its own goroutine, restarting it if it panics,
// until done is closed. Pass ctx.Done() for a context-scoped loop.
//
// Plain Recover is wrong for a node-wide loop such as the connection reaper or
// the delivery reporter: containing the panic would keep the process alive while
// permanently removing the loop, so the node would go on accepting connections
// with liveness or receipts silently dead. Restarting turns that into a logged,
// counted blip. The pause before re-entry stops a panic that reproduces
// immediately from becoming a hot loop.
func Loop(done <-chan struct{}, log *slog.Logger, site string, fn func()) {
	go func() {
		for {
			if restart := runOnce(log, site, fn); !restart {
				return
			}
			select {
			case <-done:
				return
			case <-time.After(restartDelay):
			}
		}
	}()
}

// runOnce reports whether fn panicked and should therefore be restarted.
func runOnce(log *slog.Logger, site string, fn func()) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			metrics.PanicsRecovered.WithLabelValues(site).Inc()
			l := log
			if l == nil {
				l = slog.Default()
			}
			l.Error("recovered panic, restarting loop",
				"site", site, "panic", r, "stack", string(debug.Stack()))
		}
	}()
	fn()
	return false
}

const restartDelay = time.Second
