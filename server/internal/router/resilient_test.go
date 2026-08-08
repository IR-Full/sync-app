package router

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

// failingRouter always errors on the shared lookup, simulating a Redis outage.
type failingRouter struct{}

func (failingRouter) Bind(context.Context, string, string, string) error   { return errors.New("down") }
func (failingRouter) Unbind(context.Context, string, string, string) error { return errors.New("down") }
func (failingRouter) Refresh(context.Context, string, string) error        { return errors.New("down") }
func (failingRouter) NodesFor(context.Context, string) ([]string, error) {
	return nil, errors.New("down")
}

// TestResilientFallsBackToLocal proves that when the shared router fails, a node
// still resolves its own locally-bound users (degraded same-node delivery).
func TestResilientFallsBackToLocal(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := NewResilient(failingRouter{}, log)
	ctx := context.Background()

	// Bind succeeds locally even though the shared write fails.
	if err := r.Bind(ctx, "u1", "d1", "node7"); err != nil {
		t.Fatalf("bind should not error (local ok): %v", err)
	}
	// NodesFor degrades to the local view and returns this node.
	nodes, err := r.NodesFor(ctx, "u1")
	if err != nil {
		t.Fatalf("nodesfor: %v", err)
	}
	if len(nodes) != 1 || nodes[0] != "node7" {
		t.Fatalf("want [node7] from local fallback, got %v", nodes)
	}
}
