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
		Name: "synapse_connections_active",
		Help: "Currently connected clients on this gateway node.",
	})
	// ConnRejected counts connections dropped at the accept edge by the per-IP
	// rate/concurrency guard (before handshake).
	ConnRejected = promauto.NewCounter(prometheus.CounterOpts{
		Name: "synapse_connections_rejected_total",
		Help: "Connections rejected by the per-IP accept guard (flood/storm defense).",
	})
	// FanoutShardJobs counts hot-chat fanout shard jobs published (each delivered
	// in parallel by a competing worker).
	FanoutShardJobs = promauto.NewCounter(prometheus.CounterOpts{
		Name: "synapse_fanout_shard_jobs_total",
		Help: "Hot-chat fanout member-chunk jobs published for parallel delivery.",
	})
	// FramesIn counts inbound protocol frames.
	FramesIn = promauto.NewCounter(prometheus.CounterOpts{
		Name: "synapse_frames_in_total",
		Help: "Total inbound protocol frames.",
	})
	// FramesOut counts outbound protocol frames.
	FramesOut = promauto.NewCounter(prometheus.CounterOpts{
		Name: "synapse_frames_out_total",
		Help: "Total outbound protocol frames.",
	})
	// MessagesSent counts successfully persisted chat messages.
	MessagesSent = promauto.NewCounter(prometheus.CounterOpts{
		Name: "synapse_messages_sent_total",
		Help: "Chat messages accepted and persisted.",
	})
	// Errors counts protocol error frames sent to clients, by code.
	Errors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "synapse_errors_total",
		Help: "Protocol error responses, labeled by error code.",
	}, []string{"code"})
	// OutboxPublished counts events relayed from the transactional outbox.
	OutboxPublished = promauto.NewCounter(prometheus.CounterOpts{
		Name: "synapse_outbox_published_total",
		Help: "Domain events published from the transactional outbox.",
	})
	// MessageOps counts message mutations by operation (create/edit/delete),
	// emitted by the message broker.
	MessageOps = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "synapse_message_ops_total",
		Help: "Message mutations, labeled by op (create/edit/delete).",
	}, []string{"op"})

	// SendAckSeconds is the server-side send→ack latency distribution — the core
	// user-facing SLI. Buckets span sub-ms to a few seconds.
	SendAckSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "synapse_send_ack_seconds",
		Help:    "Server-side latency from receiving a SEND to writing its SEND_ACK.",
		Buckets: latencyBuckets,
	})
	// FanoutLagSeconds is the time from a message's creation to fanout processing
	// it (event-bus + relay lag) — the delivery-pipeline health signal.
	FanoutLagSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "synapse_fanout_lag_seconds",
		Help:    "Latency from message creation to fanout processing (bus + relay lag).",
		Buckets: latencyBuckets,
	})
	// OutboxBatch is the size of each relay drain batch (queue-depth signal).
	OutboxBatch = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "synapse_outbox_batch_size",
		Help:    "Number of events claimed per outbox relay drain.",
		Buckets: []float64{1, 2, 5, 10, 25, 50, 100, 200},
	})
	// WriteBatch is the number of messages committed per group-commit transaction
	// (higher = better fsync amortization under load).
	WriteBatch = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "synapse_write_batch_size",
		Help:    "Messages committed per group-commit transaction.",
		Buckets: []float64{1, 2, 4, 8, 16, 32, 64, 128},
	})
	// WriteBatchSplit counts group-commit batches that failed and were halved to
	// isolate the offending write. A steady rate means duplicate sends (or a
	// constraint violation) are costing extra commits — watch it alongside
	// WriteBatch, whose distribution shifts left when splitting is frequent.
	WriteBatchSplit = promauto.NewCounter(prometheus.CounterOpts{
		Name: "synapse_write_batch_splits_total",
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
		Name: "synapse_cache_entries",
		Help: "Entries currently held by an in-process cache.",
	}, []string{"cache"})
	// ThrottleDropped counts frames refused by a per-connection throttle, by kind
	// ("typing", "signal"). A steady rate on typing is normal for a chatty client;
	// a rate that tracks your active-user count is the throttle being too tight.
	ThrottleDropped = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "synapse_throttle_dropped_total",
		Help: "Frames dropped or refused by a per-connection throttle.",
	}, []string{"kind"})
	// SlowConnDropped counts connections torn down because a non-droppable
	// outbound lane filled up — the client could not keep up with important
	// frames. This is backpressure of last resort, so any sustained rate is a
	// capacity signal.
	SlowConnDropped = promauto.NewCounter(prometheus.CounterOpts{
		Name: "synapse_slow_connections_dropped_total",
		Help: "Connections closed because an outbound QoS lane overflowed.",
	})
	// PresenceTransitions counts published online/offline transitions. Offline
	// should roughly track disconnects of LAST devices, not of every device.
	PresenceTransitions = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "synapse_presence_transitions_total",
		Help: "Presence transitions published, by new state.",
	}, []string{"state"})
	// RowsPurged counts rows deleted by a retention janitor, by table. Flat at
	// zero while traffic flows means a janitor is not running — the tables that
	// feed it grow forever.
	RowsPurged = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "synapse_rows_purged_total",
		Help: "Rows removed by retention janitors, by table.",
	}, []string{"table"})
	// MediaDeleted counts blobs removed from the object store, by reason
	// ("message", "expired", "orphan").
	MediaDeleted = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "synapse_media_deleted_total",
		Help: "Media objects deleted, by reason.",
	}, []string{"reason"})
	// PushSent / PushFailed count notification deliveries and their failures,
	// labelled by provider outcome; PushTokensInvalidated counts device tokens the
	// provider rejected as dead (and which were therefore removed).
	PushSent = promauto.NewCounter(prometheus.CounterOpts{
		Name: "synapse_push_sent_total",
		Help: "Push notifications accepted by the provider.",
	})
	PushFailed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "synapse_push_failed_total",
		Help: "Push notifications that failed, by reason.",
	}, []string{"reason"})
	PushTokensInvalidated = promauto.NewCounter(prometheus.CounterOpts{
		Name: "synapse_push_tokens_invalidated_total",
		Help: "Device push tokens dropped after the provider reported them dead.",
	})
)

// latencyBuckets covers 500µs .. 5s, tuned for the send/deliver SLOs.
var latencyBuckets = []float64{
	0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5,
}
