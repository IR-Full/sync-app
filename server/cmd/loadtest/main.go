// Command loadtest drives the gateway with many concurrent connections to
// measure send→ack latency and throughput — turning "scales to X" from a claim
// into a number. Each connection registers a unique user, then sends messages to
// its own self-chat (isolating the write+ack path) and records the time to each
// SEND_ACK.
//
// Two modes:
//
//   - throughput (default): each connection sends -msgs messages and records
//     send→ack latency percentiles.
//   - idle scale (-idle 30s): open -conns connections and hold them idle,
//     reporting the server's per-connection cost (goroutines and heap bytes)
//     scraped from /metrics before vs. after. This is the connection-scale rung:
//     it answers "how many idle connections fit in a node" rather than "how fast
//     can one node write".
//
// Usage:
//
//	go run ./cmd/loadtest -addr localhost:7000 -conns 200 -msgs 50
//	go run ./cmd/loadtest -addr localhost:7000 -conns 5000 -idle 30s
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/synapse-chat/synapse/pkg/wire"
)

func main() {
	addr := flag.String("addr", "localhost:7000", "gateway TCP address")
	conns := flag.Int("conns", 100, "concurrent connections")
	msgs := flag.Int("msgs", 50, "messages per connection")
	idle := flag.Duration("idle", 0, "idle-scale mode: hold connections open for this long and report per-conn server cost (e.g. 30s)")
	metricsURL := flag.String("metrics", "http://localhost:8080/metrics", "server /metrics endpoint (idle mode)")
	gcURL := flag.String("gc", "http://localhost:8080/debug/pprof/heap?gc=1", "pprof heap URL used to force a server GC before the loaded scrape (needs SYNAPSE_PPROF=1); empty to skip")
	flag.Parse()

	if *idle > 0 {
		runIdleScale(*addr, *metricsURL, *gcURL, *conns, *idle)
		return
	}

	fmt.Printf("load test: %d conns × %d msgs = %d messages → %s\n", *conns, *msgs, *conns**msgs, *addr)

	// Phase 1 (warmup): open+register all connections with BOUNDED concurrency, so
	// the argon2id registration cost is spread out and does not dominate the
	// measurement (a real fleet has long-lived, already-authed connections).
	fmt.Print("warming up (connect+register)... ")
	sessions := make([]*sess, *conns)
	var (
		warmWG    sync.WaitGroup
		connFails atomic.Int64
		sem       = make(chan struct{}, 16)
	)
	warmStart := time.Now()
	for i := 0; i < *conns; i++ {
		warmWG.Add(1)
		go func(idx int) {
			defer warmWG.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			s, err := openConn(*addr, idx)
			if err != nil {
				connFails.Add(1)
				return
			}
			sessions[idx] = s
		}(i)
	}
	warmWG.Wait()
	fmt.Printf("done (%s, %d ready, %d failed)\n", time.Since(warmStart).Round(time.Millisecond), *conns-int(connFails.Load()), connFails.Load())

	// Phase 2 (measure): all ready connections send concurrently.
	var (
		wg  sync.WaitGroup
		lat = make([][]time.Duration, *conns)
	)
	start := time.Now()
	for i := 0; i < *conns; i++ {
		if sessions[i] == nil {
			continue
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ds, err := sessions[idx].sendLoop(*msgs)
			if err != nil {
				connFails.Add(1)
				return
			}
			lat[idx] = ds
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)

	// Aggregate latencies.
	var all []time.Duration
	for _, ds := range lat {
		all = append(all, ds...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })

	fmt.Println("\n=== results ===")
	fmt.Printf("connections failed: %d\n", connFails.Load())
	fmt.Printf("messages acked:     %d\n", len(all))
	fmt.Printf("send wall time:     %s\n", elapsed.Round(time.Millisecond))
	if len(all) > 0 {
		fmt.Printf("throughput:         %.0f msg/s\n", float64(len(all))/elapsed.Seconds())
		fmt.Printf("latency p50:        %s\n", all[len(all)*50/100].Round(time.Microsecond))
		fmt.Printf("latency p95:        %s\n", all[min(len(all)*95/100, len(all)-1)].Round(time.Microsecond))
		fmt.Printf("latency p99:        %s\n", all[min(len(all)*99/100, len(all)-1)].Round(time.Microsecond))
		fmt.Printf("latency max:        %s\n", all[len(all)-1].Round(time.Microsecond))
	}
	if connFails.Load() > 0 {
		os.Exit(1)
	}
}

// runIdleScale opens conns connections, holds them idle for dur, and reports the
// server's per-connection cost by diffing /metrics (go_goroutines,
// go_memstats_heap_alloc_bytes) before and after. This measures the thing that
// actually caps connection fan-in on a node: memory and goroutines per idle
// socket, not throughput.
func runIdleScale(addr, metricsURL, gcURL string, conns int, dur time.Duration) {
	fmt.Printf("idle-scale test: %d connections held for %s → %s\n", conns, dur, addr)

	forceGC(gcURL)
	base, baseOK := scrapeMetrics(metricsURL)
	if baseOK {
		fmt.Printf("baseline: %.0f goroutines, %s heap\n", base.goroutines, humanBytes(base.heapBytes))
	} else {
		fmt.Printf("baseline: /metrics unavailable at %s (per-conn deltas will be skipped)\n", metricsURL)
	}

	fmt.Print("opening connections... ")
	sessions := make([]*sess, conns)
	var (
		warmWG    sync.WaitGroup
		connFails atomic.Int64
		sem       = make(chan struct{}, 32)
	)
	openStart := time.Now()
	for i := 0; i < conns; i++ {
		warmWG.Add(1)
		go func(idx int) {
			defer warmWG.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			s, err := openConn(addr, idx)
			if err != nil {
				connFails.Add(1)
				return
			}
			sessions[idx] = s
		}(i)
	}
	warmWG.Wait()
	ready := conns - int(connFails.Load())
	fmt.Printf("done (%s, %d open, %d failed)\n", time.Since(openStart).Round(time.Millisecond), ready, connFails.Load())

	// Hold every open connection idle, answering server PINGs with PONG so the
	// server's idle-timeout does not reap them mid-measurement.
	var holdWG sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < conns; i++ {
		if sessions[i] == nil {
			continue
		}
		holdWG.Add(1)
		go func(s *sess) {
			defer holdWG.Done()
			s.holdIdle(stop)
		}(sessions[i])
	}

	// Let the server settle, force a GC so registration churn (transient argon2id
	// and protobuf buffers) does not masquerade as retained per-connection memory,
	// then scrape the loaded figure.
	time.Sleep(2 * time.Second)
	forceGC(gcURL)
	loaded, loadedOK := scrapeMetrics(metricsURL)

	fmt.Println("\n=== idle-scale results ===")
	fmt.Printf("connections open:   %d\n", ready)
	if baseOK && loadedOK && ready > 0 {
		dg := loaded.goroutines - base.goroutines
		db := loaded.heapBytes - base.heapBytes
		fmt.Printf("goroutines added:   %.0f (%.2f per connection)\n", dg, dg/float64(ready))
		fmt.Printf("heap added:         %s (%s per connection)\n", humanBytes(db), humanBytes(db/float64(ready)))
		fmt.Printf("projected @ 100k:   %s heap, %.0f goroutines\n", humanBytes(db/float64(ready)*100_000), dg/float64(ready)*100_000)
	}
	fmt.Printf("holding idle for %s (Ctrl-C to stop early)...\n", dur)
	time.Sleep(dur)
	close(stop)
	holdWG.Wait()
	if connFails.Load() > 0 {
		os.Exit(1)
	}
}

// metricSnap is a point-in-time read of the server's runtime metrics.
type metricSnap struct {
	goroutines float64
	heapBytes  float64
}

// scrapeMetrics fetches the Prometheus text endpoint and extracts the Go runtime
// gauges the default registry exposes. Returns false if the endpoint is down.
func scrapeMetrics(url string) (metricSnap, bool) {
	resp, err := http.Get(url) // #nosec G107 -- operator-supplied metrics URL, local measurement tool
	if err != nil {
		return metricSnap{}, false
	}
	defer func() { _ = resp.Body.Close() }()
	var snap metricSnap
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "go_goroutines "):
			snap.goroutines = parseMetric(line)
		case strings.HasPrefix(line, "go_memstats_heap_alloc_bytes "):
			snap.heapBytes = parseMetric(line)
		}
	}
	return snap, true
}

// forceGC hits the pprof heap endpoint with gc=1, which runs a GC before writing
// the profile — so the subsequent /metrics scrape reflects retained memory, not
// uncollected garbage. No-op if gcURL is empty or the endpoint is unavailable.
func forceGC(gcURL string) {
	if gcURL == "" {
		return
	}
	resp, err := http.Get(gcURL) // #nosec G107 -- operator-supplied pprof URL, local measurement tool
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

func parseMetric(line string) float64 {
	f := strings.Fields(line)
	if len(f) < 2 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[len(f)-1], 64)
	return v
}

func humanBytes(b float64) string {
	neg := ""
	if b < 0 {
		neg, b = "-", -b
	}
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%s%.2f GiB", neg, b/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%s%.2f MiB", neg, b/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%s%.2f KiB", neg, b/(1<<10))
	default:
		return fmt.Sprintf("%s%.0f B", neg, b)
	}
}

// sess is an authenticated load-test connection.
type sess struct {
	conn *wire.Conn
	user string
	seq  uint64
}

// holdIdle keeps the connection alive until stop closes, replying to server PINGs
// with PONG so the idle-timeout reaper does not close it during measurement.
func (s *sess) holdIdle(stop <-chan struct{}) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			e, err := s.conn.ReadEnvelope()
			if err != nil {
				return
			}
			if e.Type == wire.MsgPing {
				s.seq++
				_ = s.conn.Send(wire.MsgPong, s.seq, 0, e.RequestID, nil)
			}
		}
	}()
	select {
	case <-stop:
	case <-done:
	}
	_ = s.conn.Close()
}

// openConn dials, handshakes, and registers a unique user.
func openConn(addr string, idx int) (*sess, error) {
	c, err := net.DialTimeout("tcp", addr, 15*time.Second)
	if err != nil {
		return nil, err
	}
	conn := wire.NewConn(wire.NewTCPTransport(c), false)
	user := fmt.Sprintf("lt_%d_%s", idx, randHex())
	if err := conn.Send(wire.MsgHello, 1, 0, 1, wire.HelloBody{ClientVersion: "loadtest", Platform: "cli"}); err != nil {
		return nil, err
	}
	if e, err := conn.ReadEnvelope(); err != nil || e.Type != wire.MsgWelcome {
		return nil, fmt.Errorf("welcome: %v", err)
	}
	if err := conn.Send(wire.MsgAuth, 2, 0, 2, wire.AuthBody{Username: user, Password: "loadtest123", Register: true}); err != nil {
		return nil, err
	}
	if e, err := conn.ReadEnvelope(); err != nil || e.Type != wire.MsgAuthOK {
		return nil, fmt.Errorf("auth: %v", err)
	}
	return &sess{conn: conn, user: user, seq: 2}, nil
}

// sendLoop sends msgs messages to the self-chat, timing each send→ack round trip.
// The first send resolves the chat by @username; the ack returns the concrete
// chat id, reused thereafter (as a real client does).
func (s *sess) sendLoop(msgs int) ([]time.Duration, error) {
	out := make([]time.Duration, 0, msgs)
	target := "@" + s.user
	for i := 0; i < msgs; i++ {
		s.seq++
		t0 := time.Now()
		if err := s.conn.Send(wire.MsgSend, s.seq, 0, uint64(i), wire.SendBody{ChatID: target, DedupKey: randHex(), Text: "load"}); err != nil {
			return nil, err
		}
		for {
			e, err := s.conn.ReadEnvelope()
			if err != nil {
				return nil, err
			}
			if e.Type == wire.MsgSendAck {
				out = append(out, time.Since(t0))
				var ack wire.SendAckBody
				_ = wire.Unmarshal(e.Body, &ack)
				if ack.ChatID != "" {
					target = ack.ChatID
				}
				break
			}
		}
	}
	return out, nil
}

func randHex() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
