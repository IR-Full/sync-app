package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// report is what a run measured. Durations are milliseconds so the JSON reads
// without a unit table.
type report struct {
	Mode                string  `json:"mode"`
	Connections         int     `json:"connections"`
	Ready               int     `json:"ready"`
	Failed              int     `json:"failed"`
	Acked               int     `json:"acked,omitempty"`
	ThroughputMsgPerSec float64 `json:"throughput_msg_per_sec,omitempty"`
	P50Ms               float64 `json:"p50_ms,omitempty"`
	P95Ms               float64 `json:"p95_ms,omitempty"`
	P99Ms               float64 `json:"p99_ms,omitempty"`
	MaxMs               float64 `json:"max_ms,omitempty"`
	GoroutinesPerConn   float64 `json:"goroutines_per_conn,omitempty"`
	HeapBytesPerConn    float64 `json:"heap_bytes_per_conn,omitempty"`
	ServerRSSBytes      float64 `json:"server_rss_bytes,omitempty"`

	latenciesMeasured  bool
	idleDeltasMeasured bool
}

// limits are the thresholds; a zero value means "not checked".
type limits struct {
	maxP99               time.Duration
	minThroughput        float64
	maxRSSMB             float64
	maxHeapPerConn       float64
	maxGoroutinesPerConn float64
}

// check returns every threshold the report breaks. A threshold on something the
// run did not measure is a violation too: a check that passes because its metric
// was missing is not a check.
func (l limits) check(r report) []string {
	var out []string
	if l.maxP99 > 0 {
		switch {
		case !r.latenciesMeasured:
			out = append(out, "-max-p99 is set but no latency was measured")
		case r.P99Ms > millis(l.maxP99):
			out = append(out, fmt.Sprintf("p99 %.2f ms > %.2f ms", r.P99Ms, millis(l.maxP99)))
		}
	}
	if l.minThroughput > 0 && r.ThroughputMsgPerSec < l.minThroughput {
		out = append(out, fmt.Sprintf("throughput %.0f msg/s < %.0f msg/s", r.ThroughputMsgPerSec, l.minThroughput))
	}
	if l.maxRSSMB > 0 {
		switch {
		case r.ServerRSSBytes == 0:
			out = append(out, "-max-rss-mb is set but process_resident_memory_bytes could not be read from -metrics")
		case r.ServerRSSBytes > l.maxRSSMB*(1<<20):
			out = append(out, fmt.Sprintf("server RSS %.1f MiB > %.1f MiB", r.ServerRSSBytes/(1<<20), l.maxRSSMB))
		}
	}
	if l.maxHeapPerConn > 0 || l.maxGoroutinesPerConn > 0 {
		if !r.idleDeltasMeasured {
			out = append(out, "a per-connection threshold is set but the idle-mode deltas were not measured (-idle, and -metrics reachable)")
			return out
		}
		if l.maxHeapPerConn > 0 && r.HeapBytesPerConn > l.maxHeapPerConn {
			out = append(out, fmt.Sprintf("heap per connection %.0f B > %.0f B", r.HeapBytesPerConn, l.maxHeapPerConn))
		}
		if l.maxGoroutinesPerConn > 0 && r.GoroutinesPerConn > l.maxGoroutinesPerConn {
			out = append(out, fmt.Sprintf("goroutines per connection %.2f > %.2f", r.GoroutinesPerConn, l.maxGoroutinesPerConn))
		}
	}
	return out
}

func writeReport(path string, r report) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}
