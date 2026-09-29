#!/usr/bin/env bash
# Secret-chat interop: the web client's X3DH and Double Ratchet against the
# server's own pkg/e2e, through a real gateway, in both directions.
#
#   1. TypeScript initiates, Go (cmd/e2epeer) responds and replies.
#   2. Go initiates, TypeScript responds — including finding which one-time
#      prekey the directory handed out.
#
# Needs Go and Node (client dependencies installed). Runs from anywhere; the
# server is in-memory on ports 17000/18080.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
cleanup() {
  jobs -p | xargs -r kill 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

tcp=127.0.0.1:17000
http=127.0.0.1:18080

# wait_for FILE PATTERN prints the first matching line, or fails after ~30 s.
wait_for() {
  for _ in $(seq 1 150); do
    if line=$(grep -m1 -E "$2" "$1" 2>/dev/null); then
      echo "$line"
      return 0
    fi
    sleep 0.2
  done
  echo "timed out waiting for /$2/ in $1:" >&2
  cat "$1" >&2
  return 1
}

cd "$root/server"
go build -o "$work/server" ./cmd/server
go build -o "$work/e2epeer" ./cmd/e2epeer

SYNCAPP_TCP_ADDR=$tcp SYNCAPP_WS_ADDR=$http SYNCAPP_MEDIA_DIR="$work/media" \
  "$work/server" >"$work/server.log" 2>&1 &
wait_for "$work/server.log" 'gateway listening \(ws\)' >/dev/null

cd "$root/client"

echo "== TypeScript initiates, Go responds"
"$work/e2epeer" -addr "$tcp" -timeout 60s >"$work/peer.log" 2>&1 &
peer=$(wait_for "$work/peer.log" '^READY ' | awk '{print $2}')
npm run --silent test:secret -- "ws://$http/ws" "${peer%%:*}" "${peer#*:}"
grep -q '^PEER_DECRYPTED ' "$work/peer.log" || { cat "$work/peer.log"; exit 1; }

echo "== Go initiates, TypeScript responds"
npm run --silent test:secret-responder -- "ws://$http/ws" >"$work/responder.log" 2>&1 &
responder=$!
target=$(wait_for "$work/responder.log" '^READY ' | awk '{print $2}')
"$work/e2epeer" -addr "$tcp" -initiate-to "$target"
status=0
wait "$responder" || status=$?
cat "$work/responder.log"
exit "$status"
