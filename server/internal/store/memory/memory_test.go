package memory_test

import (
	"testing"

	"github.com/IR-Full/sync-app/server/internal/store"
	"github.com/IR-Full/sync-app/server/internal/store/memory"
	"github.com/IR-Full/sync-app/server/internal/store/storetest"
)

// The in-memory store is what every unit test in the repo runs against, so its
// behaviour is the de-facto contract — which makes it the most important
// implementation to hold to the written one.
func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Stores {
		return memory.New().Stores()
	})
}
