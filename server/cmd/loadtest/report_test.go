package main

import (
	"strings"
	"testing"
	"time"
)

func TestThresholds(t *testing.T) {
	measured := report{
		latenciesMeasured: true, P99Ms: 12, ThroughputMsgPerSec: 900,
		ServerRSSBytes: 100 << 20, idleDeltasMeasured: true, HeapBytesPerConn: 9000, GoroutinesPerConn: 2.1,
	}
	for name, tc := range map[string]struct {
		lim  limits
		r    report
		want string // substring of the only violation; "" means none
	}{
		"all within":        {limits{maxP99: 20 * time.Millisecond, minThroughput: 500, maxRSSMB: 200, maxHeapPerConn: 10000, maxGoroutinesPerConn: 3}, measured, ""},
		"p99 over":          {limits{maxP99: 10 * time.Millisecond}, measured, "p99"},
		"throughput under":  {limits{minThroughput: 1000}, measured, "throughput"},
		"rss over":          {limits{maxRSSMB: 50}, measured, "RSS"},
		"heap over":         {limits{maxHeapPerConn: 8000}, measured, "heap per connection"},
		"goroutines over":   {limits{maxGoroutinesPerConn: 2}, measured, "goroutines per connection"},
		"no latency":        {limits{maxP99: time.Second}, report{}, "no latency"},
		"no rss":            {limits{maxRSSMB: 100}, report{}, "process_resident_memory_bytes"},
		"no idle deltas":    {limits{maxHeapPerConn: 1}, report{}, "not measured"},
		"nothing requested": {limits{}, report{}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			got := tc.lim.check(tc.r)
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("unexpected violations: %q", got)
				}
				return
			}
			if len(got) != 1 || !strings.Contains(got[0], tc.want) {
				t.Fatalf("got %q, want one violation mentioning %q", got, tc.want)
			}
		})
	}
}
