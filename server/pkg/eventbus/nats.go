package eventbus

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// isDurable reports whether a subject is served by JetStream (vs core NATS).
func isDurable(subject string) bool {
	return strings.HasPrefix(subject, "message.") || strings.HasPrefix(subject, "notify.")
}

// NewNATS connects and provisions the JetStream stream.
func NewNATS(url string) (Bus, error) {
	nc, err := nats.Connect(url,
		nats.MaxReconnects(-1),
		nats.ReconnectWait(nats.DefaultReconnectWait),
	)
	if err != nil {
		return nil, err
	}
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, err
	}
	// Ensure the durable-events stream exists (idempotent).
	_, err = js.AddStream(&nats.StreamConfig{
		Name:      streamName,
		Subjects:  durableSubjects,
		Retention: nats.LimitsPolicy,
		Storage:   nats.FileStorage,
		MaxAge:    24 * time.Hour, // bound growth; consumers keep up well within this
		Discard:   nats.DiscardOld,
	})
	if err != nil && !errors.Is(err, nats.ErrStreamNameAlreadyInUse) {
		nc.Close()
		return nil, err
	}
	return &natsBus{nc: nc, js: js}, nil
}

func (b *natsBus) Publish(_ context.Context, e Event) error {
	msg := nats.NewMsg(e.Subject)
	if e.Key != "" {
		msg.Header.Set("key", e.Key)
	}
	for k, v := range e.Headers {
		msg.Header.Set("h-"+k, v)
	}
	msg.Data = e.Data
	if isDurable(e.Subject) {
		_, err := b.js.PublishMsg(msg) // waits for the stream ack (durable)
		return err
	}
	return b.nc.PublishMsg(msg)
}

func (b *natsBus) Subscribe(subject, queue string, h Handler) error {
	if isDurable(subject) {
		return b.subscribeDurable(subject, queue, h)
	}
	return b.subscribeCore(subject, queue, h)
}

// subscribeDurable creates (or binds to) a durable JetStream consumer with
// explicit ack. Workers sharing a queue share the consumer (competing consumers);
// a crash redelivers unacked messages.
func (b *natsBus) subscribeDurable(subject, queue string, h Handler) error {
	durable := durableName(queue, subject)
	cb := func(m *nats.Msg) {
		if err := h(context.Background(), fromMsg(m)); err != nil {
			_ = m.Nak() // redeliver later
			return
		}
		_ = m.Ack()
	}
	q := queue
	if q == "" {
		q = durable // JetStream push consumers need a deliver group; reuse durable
	}
	sub, err := b.js.QueueSubscribe(subject, q, cb,
		nats.Durable(durable),
		nats.ManualAck(),
		nats.AckExplicit(),
		nats.AckWait(30*time.Second),
		nats.MaxDeliver(10),
		nats.DeliverNew(), // fresh consumer starts at "new"; existing resumes at its ack floor
	)
	if err != nil {
		return err
	}
	b.subs = append(b.subs, sub)
	return nil
}

func (b *natsBus) subscribeCore(subject, queue string, h Handler) error {
	cb := func(m *nats.Msg) { _ = h(context.Background(), fromMsg(m)) }
	var (
		sub *nats.Subscription
		err error
	)
	if queue != "" {
		sub, err = b.nc.QueueSubscribe(subject, queue, cb)
	} else {
		sub, err = b.nc.Subscribe(subject, cb)
	}
	if err != nil {
		return err
	}
	b.subs = append(b.subs, sub)
	return nil
}

// fromMsg rebuilds an Event from a NATS message (key + app headers).
func fromMsg(m *nats.Msg) Event {
	ev := Event{Subject: m.Subject, Key: m.Header.Get("key"), Data: m.Data}
	for k := range m.Header {
		if strings.HasPrefix(k, "h-") {
			if ev.Headers == nil {
				ev.Headers = map[string]string{}
			}
			ev.Headers[k[2:]] = m.Header.Get(k)
		}
	}
	return ev
}

// durableName derives a JetStream-legal durable name (no dots/spaces) per
// (queue, subject) so each subscribed subject in a queue group has its own
// consumer with a persisted position.
func durableName(queue, subject string) string {
	if queue == "" {
		queue = "q"
	}
	s := strings.NewReplacer(".", "_", "*", "star", ">", "all", " ", "_").Replace(subject)
	return queue + "_" + s
}

func (b *natsBus) Close() error {
	for _, s := range b.subs {
		_ = s.Unsubscribe()
	}
	b.nc.Close()
	return nil
}
