// Package metrics defines the Prometheus collectors exposed at /metrics
// (Section 13 observability). Keeping them in one place lets any service
// increment them without re-declaring, and gives operators a stable metric
// surface for dashboards and SLO alerting.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// ConnActive is the number of live client connections on this node.
	ConnActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "SYNCAPP_connections_active",
		Help: "Currently connected clients on this gateway node.",
	})
	// ConnRejected counts connections dropped at the accept edge by the per-IP
	// rate/concurrency guard (before handshake).
	ConnRejected = promauto.NewCounter(prometheus.CounterOpts{
		Name: "SYNCAPP_connections_rejected_total",
		Help: "Connections rejected by the per-IP accept guard (flood/storm defense).",
	})
	// PanicsRecovered counts panics contained at a goroutine boundary, by site.
	// A non-zero value is always a bug: the panic did not take the process down,
	// but whatever it interrupted did not finish.
	PanicsRecovered = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "SYNCAPP_panics_recovered_total",
		Help: "Panics recovered at a goroutine boundary, labeled by site.",
	}, []string{"site"})
	// FanoutShardJobs counts hot-chat fanout shard jobs published (each delivered
	// in parallel by a competing worker).
	FanoutShardJobs = promauto.NewCounter(prometheus.CounterOpts{
		Name: "SYNCAPP_fanout_shard_jobs_total",
		Help: "Hot-chat fanout member-chunk jobs published for parallel delivery.",
	})
	// FramesIn counts inbound protocol frames.
	FramesIn = promauto.NewCounter(prometheus.CounterOpts{
		Name: "SYNCAPP_frames_in_total",
		Help: "Total inbound protocol frames.",
	})
	// FramesOut counts outbound protocol frames.
	FramesOut = promauto.NewCounter(prometheus.CounterOpts{
		Name: "SYNCAPP_frames_out_total",
		Help: "Total outbound protocol frames.",
	})
	// MessagesSent counts successfully persisted chat messages.
	MessagesSent = promauto.NewCounter(prometheus.CounterOpts{
		Name: "SYNCAPP_messages_sent_total",
		Help: "Chat messages accepted and persisted.",
	})
	// Errors counts protocol error frames sent to clients, by code.
	Errors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "SYNCAPP_errors_total",
		Help: "Protocol error responses, labeled by error code.",
	}, []string{"code"})
	// OutboxPublished counts events relayed from the transactional outbox.
	OutboxPublished = promauto.NewCounter(prometheus.CounterOpts{
		Name: "SYNCAPP_outbox_published_total",
		Help: "Domain events published from the transactional outbox.",
	})
	// MessageOps counts message mutations by operation (create/edit/delete),
	// emitted by the message broker.
	MessageOps = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "SYNCAPP_message_ops_total",
		Help: "Message mutations, labeled by op (create/edit/delete).",
	}, []string{"op"})

	// SendAckSeconds is the server-side send→ack latency distribution — the core
	// user-facing SLI. Buckets span sub-ms to a few seconds.
	SendAckSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "SYNCAPP_send_ack_seconds",
		Help:    "Server-side latency from receiving a SEND to writing its SEND_ACK.",
		Buckets: latencyBuckets,
	})
	// FanoutLagSeconds is the time from a message's creation to fanout processing
	// it (event-bus + relay lag) — the delivery-pipeline health signal.
	FanoutLagSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "SYNCAPP_fanout_lag_seconds",
		Help:    "Latency from message creation to fanout processing (bus + relay lag).",
		Buckets: latencyBuckets,
	})
	// OutboxBatch is the size of each relay drain batch (queue-depth signal).
	OutboxBatch = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "SYNCAPP_outbox_batch_size",
		Help:    "Number of events claimed per outbox relay drain.",
		Buckets: []float64{1, 2, 5, 10, 25, 50, 100, 200},
	})
	// WriteBatch is the number of messages committed per group-commit transaction
	// (higher = better fsync amortization under load).
	WriteBatch = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "SYNCAPP_write_batch_size",
		Help:    "Messages committed per group-commit transaction.",
		Buckets: []float64{1, 2, 4, 8, 16, 32, 64, 128},
	})
	// WriteBatchSplit counts group-commit batches that failed and were halved to
	// isolate the offending write. A steady rate means duplicate sends (or a
	// constraint violation) are costing extra commits — watch it alongside
	// WriteBatch, whose distribution shifts left when splitting is frequent.
	WriteBatchSplit = promauto.NewCounter(prometheus.CounterOpts{
		Name: "SYNCAPP_write_batch_splits_total",
		Help: "Group-commit batches bisected after a failed transaction.",
	})
	// --- Limits and bounds ---
	//
	// A limit nobody can see is indistinguishable from a limit that is quietly
	// strangling legitimate traffic. Each bound the system enforces reports both
	// how full it is and how often it bit.

	// CacheEntries is the live size of an in-process cache, labelled by which one.
	// Watch it against the ceiling in the code: sitting at the cap means the node
	// is touching more chats than the cache was sized for.
	CacheEntries = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "SYNCAPP_cache_entries",
		Help: "Entries currently held by an in-process cache.",
	}, []string{"cache"})
	// ThrottleDropped counts frames refused by a per-connection throttle, by kind
	// ("typing", "signal"). A steady rate on typing is normal for a chatty client;
	// a rate that tracks your active-user count is the throttle being too tight.
	ThrottleDropped = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "SYNCAPP_throttle_dropped_total",
		Help: "Frames dropped or refused by a per-connection throttle.",
	}, []string{"kind"})
	// Billing. The webhook counters are the ones that matter operationally: a
	// rising WebhookRejected is a misconfigured secret or someone probing, and a
	// WebhookDuplicate rate far above WebhookApplied means a provider is retrying
	// because it is not getting the acknowledgement it expects.
	CheckoutStarted = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "SYNCAPP_checkout_started_total",
		Help: "Payment checkouts started, by provider and method.",
	}, []string{"provider", "method"})
	CheckoutDeduped = promauto.NewCounter(prometheus.CounterOpts{
		Name: "SYNCAPP_checkout_deduplicated_total",
		Help: "Checkouts resolved to an existing payment by idempotency key.",
	})
	WebhookApplied = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "SYNCAPP_billing_webhook_applied_total",
		Help: "Provider callbacks that changed a payment, by provider and new status.",
	}, []string{"provider", "status"})
	WebhookDuplicate = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "SYNCAPP_billing_webhook_duplicate_total",
		Help: "Provider callbacks that were not news (duplicate or reordered).",
	}, []string{"provider"})
	WebhookRejected = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "SYNCAPP_billing_webhook_rejected_total",
		Help: "Provider callbacks whose signature did not verify.",
	}, []string{"provider"})
	WebhookUnknown = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "SYNCAPP_billing_webhook_unknown_total",
		Help: "Provider callbacks naming a payment this server never started.",
	}, []string{"provider"})
	SubscriptionExpired = promauto.NewCounter(prometheus.CounterOpts{
		Name: "SYNCAPP_subscription_expired_total",
		Help: "Subscriptions closed by the expiry sweep.",
	})
	// PushSuppressedMuted counts notifications not sent because the recipient had
	// muted the chat. It is the only evidence that muting does anything — the
	// feature's whole observable effect is an absence.
	PushSuppressedMuted = promauto.NewCounter(prometheus.CounterOpts{
		Name: "SYNCAPP_push_suppressed_muted_total",
		Help: "Push notifications suppressed because the recipient muted the chat.",
	})
	// SecretQueued counts E2E envelopes held because the addressed device had no
	// live connection. A rate near zero means secret chats are effectively
	// synchronous; a sustained one is normal (people are offline) — what matters
	// is that it used to be the rate at which messages were silently destroyed.
	SecretQueued = promauto.NewCounter(prometheus.CounterOpts{
		Name: "SYNCAPP_secret_queued_total",
		Help: "End-to-end envelopes stored for an offline device.",
	})
	// SecretDequeued counts envelopes a device confirmed and had dropped. It
	// should track SecretQueued over time; a persistent gap means clients are
	// collecting their backlog but not acknowledging it, and the queue is growing
	// until the collector expires it.
	SecretDequeued = promauto.NewCounter(prometheus.CounterOpts{
		Name: "SYNCAPP_secret_dequeued_total",
		Help: "End-to-end envelopes acknowledged by a device and deleted.",
	})
	// SecretExpired counts envelopes nobody came back for. Each one is a message
	// that was never read, so a rising rate is a product signal, not only a
	// storage one.
	SecretExpired = promauto.NewCounter(prometheus.CounterOpts{
		Name: "SYNCAPP_secret_expired_total",
		Help: "End-to-end envelopes collected after their TTL.",
	})
	// ReplayDropped counts resume-buffer entries dropped because the async writer's
	// queue was full. Sustained growth means the replay store (Redis) cannot keep
	// up or is down; the cost is a history backfill on the affected resumes.
	ReplayDropped = promauto.NewCounter(prometheus.CounterOpts{
		Name: "SYNCAPP_replay_dropped_total",
		Help: "Resume-buffer frames dropped because the async writer queue was full.",
	})
	// ReplayWriteErrors counts failed batch writes to the replay store.
	ReplayWriteErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "SYNCAPP_replay_write_errors_total",
		Help: "Failed batch writes to the resume replay store.",
	})
	// SlowConnDropped counts connections torn down because a non-droppable
	// outbound lane filled up — the client could not keep up with important
	// frames. This is backpressure of last resort, so any sustained rate is a
	// capacity signal.
	SlowConnDropped = promauto.NewCounter(prometheus.CounterOpts{
		Name: "SYNCAPP_slow_connections_dropped_total",
		Help: "Connections closed because an outbound QoS lane overflowed.",
	})
	// PresenceTransitions counts published online/offline transitions. Offline
	// should roughly track disconnects of LAST devices, not of every device.
	PresenceTransitions = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "SYNCAPP_presence_transitions_total",
		Help: "Presence transitions published, by new state.",
	}, []string{"state"})
	// RowsPurged counts rows deleted by a retention janitor, by table. Flat at
	// zero while traffic flows means a janitor is not running — the tables that
	// feed it grow forever.
	RowsPurged = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "SYNCAPP_rows_purged_total",
		Help: "Rows removed by retention janitors, by table.",
	}, []string{"table"})
	// MediaDeleted counts blobs removed from the object store, by reason
	// ("message", "expired", "orphan").
	MediaDeleted = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "SYNCAPP_media_deleted_total",
		Help: "Media objects deleted, by reason.",
	}, []string{"reason"})
	// PushSent / PushFailed count notification deliveries and their failures,
	// labelled by provider outcome; PushTokensInvalidated counts device tokens the
	// provider rejected as dead (and which were therefore removed).
	PushSent = promauto.NewCounter(prometheus.CounterOpts{
		Name: "SYNCAPP_push_sent_total",
		Help: "Push notifications accepted by the provider.",
	})
	PushFailed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "SYNCAPP_push_failed_total",
		Help: "Push notifications that failed, by reason.",
	}, []string{"reason"})
	PushTokensInvalidated = promauto.NewCounter(prometheus.CounterOpts{
		Name: "SYNCAPP_push_tokens_invalidated_total",
		Help: "Device push tokens dropped after the provider reported them dead.",
	})
)

// latencyBuckets covers 500µs .. 5s, tuned for the send/deliver SLOs.
var latencyBuckets = []float64{
	0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5,
}
