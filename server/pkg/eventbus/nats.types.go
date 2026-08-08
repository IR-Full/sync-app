package eventbus

import "github.com/nats-io/nats.go"

// natsBus is the production Bus. Domain events that MUST reach their consumers
// (message.created/edited/deleted, message.read, notify.push) go through
// JetStream with durable consumers + explicit ack: if a consumer (search,
// moderation, notification, fanout) is briefly down, JetStream redelivers on
// restart instead of dropping the event — core NATS is fire-and-forget. Ephemeral,
// high-volume traffic (cross-node deliver.<node>, typing, presence) stays on core
// NATS, where persistence would be pointless and expensive.
//
// Note: the transactional outbox already makes event EMISSION durable; JetStream
// adds durable CONSUMPTION so a down worker doesn't miss what was emitted.
type natsBus struct {
	nc   *nats.Conn
	js   nats.JetStreamContext
	subs []*nats.Subscription
}
