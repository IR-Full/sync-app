package gateway

import (
	"testing"

	"github.com/IR-Full/sync-app/server/pkg/wire"
)

// The read side used to cost nothing at all. HISTORY is the clearest case: one
// small frame draws a database page and streams up to a hundred full message
// frames back, and a client could ask again immediately, forever.
func TestAmplifyingCoversTheUnboundedReads(t *testing.T) {
	metered := []wire.MsgType{
		wire.MsgHistory, wire.MsgChatList, wire.MsgThread, wire.MsgRead,
		wire.MsgPinList, wire.MsgDraftSync, wire.MsgContactSync,
		wire.MsgInviteList, wire.MsgScheduleList,
	}
	for _, t2 := range metered {
		if !amplifying(t2) {
			t.Errorf("%s answers more than it is asked and is not metered", t2)
		}
	}
}

// Liveness and transport control must stay free: charging a PING would let a
// budget close a healthy connection, which is a worse failure than the flood it
// would be guarding against.
func TestAmplifyingLeavesLivenessAlone(t *testing.T) {
	for _, t2 := range []wire.MsgType{wire.MsgPing, wire.MsgPong, wire.MsgTransportAck, wire.MsgHello, wire.MsgAuth} {
		if amplifying(t2) {
			t.Errorf("%s is liveness/handshake and must not be metered as a read", t2)
		}
	}
}

// The two budgets are deliberately separate. A type in both would be charged
// twice for one frame, which is not what either bucket is sized for.
func TestReadAndWriteBudgetsDoNotOverlap(t *testing.T) {
	for t2 := wire.MsgType(0); t2 < 200; t2++ {
		if amplifying(t2) && stateChanging(t2) {
			t.Errorf("%s is charged to both the read and the write budget", t2)
		}
	}
}

// DefaultConfig must actually configure the bucket. A zero rate would build a
// bucket that refuses everything, turning the whole read path off — the kind of
// mistake that is invisible in review and total in production.
func TestDefaultConfigGivesTheReadBudgetARate(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.ReadRate <= 0 || cfg.ReadBurst <= 0 {
		t.Fatalf("read budget is not configured: rate=%v burst=%v", cfg.ReadRate, cfg.ReadBurst)
	}
	// Reads should be looser than writes: scrolling a chat is normal behaviour,
	// sending twenty messages a second is not.
	if cfg.ReadRate <= cfg.SendRate {
		t.Fatalf("read rate %v is not looser than the write rate %v", cfg.ReadRate, cfg.SendRate)
	}
}
