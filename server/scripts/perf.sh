#!/usr/bin/env bash
# Performance regression check: an in-memory server under two loads, each held to
# thresholds. The limits are regression guards, set 3–4× away from a baseline
# measured on 4 vCPUs (the size of a GitHub-hosted runner), not benchmarks:
#
#   throughput ~35 000 msg/s, p99 ~25 ms;
#   idle: 2.0 goroutines and ~32 KiB of heap per connection;
#   RSS 430–810 MiB, most of it argon2id working memory from the registrations.
#
#   1. throughput: 200 connections × 50 messages, send→ack p99 and msg/s;
#   2. idle scale: 2000 idle connections, retained heap and goroutines each,
#      plus the server's resident memory after both.
#
# Override any limit through the environment (PERF_MAX_P99, PERF_MIN_THROUGHPUT,
# PERF_MAX_RSS_MB, PERF_MAX_HEAP_PER_CONN, PERF_MAX_GOROUTINES_PER_CONN). The JSON
# reports go to $PERF_OUT (default: ./perf-results).
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
out=${PERF_OUT:-$PWD/perf-results}
work=$(mktemp -d)
cleanup() {
  jobs -p | xargs -r kill 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT
mkdir -p "$out"

tcp=127.0.0.1:17100
http=127.0.0.1:18180

cd "$root"
go build -o "$work/server" ./cmd/server
go build -o "$work/loadtest" ./cmd/loadtest

# The flood limit is per connection and far below what a load test sends; pprof is
# how the idle run forces a GC before reading retained heap.
SYNCAPP_TCP_ADDR=$tcp SYNCAPP_WS_ADDR=$http SYNCAPP_MEDIA_DIR="$work/media" \
  SYNCAPP_SEND_RATE=100000 SYNCAPP_PPROF=1 \
  "$work/server" >"$work/server.log" 2>&1 &
for _ in $(seq 1 100); do
  curl -fs "http://$http/healthz" >/dev/null 2>&1 && break
  sleep 0.1
done

common=(-addr "$tcp" -metrics "http://$http/metrics" -gc "http://$http/debug/pprof/heap?gc=1")

echo "== throughput"
"$work/loadtest" "${common[@]}" -conns 200 -msgs 50 \
  -max-p99 "${PERF_MAX_P99:-100ms}" \
  -min-throughput "${PERF_MIN_THROUGHPUT:-10000}" \
  -json "$out/throughput.json"

echo "== idle scale"
"$work/loadtest" "${common[@]}" -conns 2000 -idle 3s \
  -max-heap-per-conn "${PERF_MAX_HEAP_PER_CONN:-96000}" \
  -max-goroutines-per-conn "${PERF_MAX_GOROUTINES_PER_CONN:-3}" \
  -max-rss-mb "${PERF_MAX_RSS_MB:-1536}" \
  -json "$out/idle.json"
