// Delivery receipts: telling a sender that a message reached someone's device.
//
// A SendAck proves the write was DURABLE and a ReadUpd proves it was READ, but
// nothing reported the step between them — fanout pushed a message and told the
// sender nothing, so a client could only ever draw two states. What was missing is
// not a counter but a *witness*: only the gateway holding the recipient's socket
// knows whether the bytes left the server.
//
// So the receipt is raised here, from the write path, and nowhere else. `route()`
// returning a node count would have been the cheap answer, and it would have meant
// "some node was notified" — which is true even when that node's connection dies
// with the frame still queued on it.
package gateway

import (
	"context"

	"github.com/synapse-chat/synapse/internal/metrics"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// deliveryReport is one receipt waiting to be routed back to its sender.
type deliveryReport struct {
	senderID string
	payload  []byte
}

// reportDelivery hands a receipt to the async reporter. Called from the
// connection's single writer, so it never blocks and never fails loudly: routing
// the receipt needs a registry lookup, and a writer that waited for Redis would
// stall the very connection it just wrote to.
func (g *Gateway) reportDelivery(r deliveryReport) {
	select {
	case g.delivered <- r:
	default:
		// The queue is full, which means the node is already struggling to keep up
		// with its own writes. A dropped receipt costs one tick that stays grey until
		// the read receipt supersedes it — the same trade the ephemeral QoS lane makes,
		// and far better than blocking a writer to preserve a decoration.
		metrics.ThrottleDropped.WithLabelValues("delivered").Inc()
	}
}

// deliveryReporterFor builds the OnWritten hook for a MsgNew being pushed to
// recipientID, or nil when there is nothing worth reporting.
//
// The body is decoded ONCE per node-delivery rather than per connection: a user
// with three devices produces three writes, and the sender's cursor is monotonic,
// so three identical receipts are harmless — three protobuf decodes would not be.
func (g *Gateway) deliveryReporterFor(recipientID string, body []byte) func() {
	if g.svc.Router == nil || g.svc.Bus == nil {
		return nil // single process without cross-node wiring: nothing to route through
	}
	var msg wire.NewMessageBody
	if err := wire.Unmarshal(body, &msg); err != nil {
		return nil
	}
	// Nothing to tell the sender about their own devices, and a message with no
	// sequence is not a chat message we can report a position for.
	if msg.SenderID == "" || msg.SenderID == recipientID || msg.ChatSeq == 0 {
		return nil
	}
	report := deliveryReport{
		senderID: msg.SenderID,
		payload: wire.Marshal(wire.ReadUpdateBody{
			ChatID:      msg.ChatID,
			UserID:      recipientID,
			UpToChatSeq: msg.ChatSeq,
		}),
	}
	return func() { g.reportDelivery(report) }
}

// runDeliveryReporter drains receipts on one goroutine per node. One, not one per
// receipt: at message rates a goroutine each would be the most expensive part of
// delivering a message.
func (g *Gateway) runDeliveryReporter() {
	for {
		select {
		case <-g.reaperDone:
			return
		case r := <-g.delivered:
			g.routeToUser(context.Background(), r.senderID, "", wire.MsgDelivered, r.payload)
		}
	}
}
