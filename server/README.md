# Synapse — a Telegram-class messenger backend (Go)

A production-shaped MVP of a real-time messaging platform: a **custom binary
protocol** over TCP, WebSocket, and **QUIC**, a **multi-node realtime gateway**,
and domain **microservices** wired through a **JetStream event bus**, with
Postgres/Redis/NATS persistence and a **Go CLI client**.

This is the runnable core of the system designed in
[ARCHITECTURE.md](ARCHITECTURE.md) (which answers all 18 sections of the design
brief). The code is organized along service boundaries so pieces can be split
into separate deployables later, but it runs today as one process.

## What works end-to-end

- Custom binary framing + envelope protocol (`pkg/wire`), **protobuf bodies** —
  over **TCP, WebSocket, and QUIC** (one stream-generic codec)
- Handshake with capability negotiation, auth, session resume (**Redis replay buffer**)
- 1:1 chats (started by `@username`), group/channel model
- Durable, **idempotent** message writes with **gap-free per-chat ordering**, funneled
  through a single **message command broker** (validation/tracing/metrics)
- **Group-commit writes** — concurrent inserts coalesce into one fsync (~16× throughput,
  durability preserved)
- **Multi-node**: cross-node delivery routing (Redis registry + bus), shared prekey
  directory (Redis) and search index (Postgres tsvector)
- **Huge chats stay affordable**: fanout streams a channel's membership by keyset
  page instead of materializing it, and authorization above a size threshold is a
  primary-key probe rather than a cached copy of every member's role
- Fanout delivery to multi-device recipients; offline → push job
- Read receipts, typing indicators, presence/last-seen
- Message edit/delete (tombstone), paged history + **paged chat export** (owner/admin)
- **Full-text search** (permission-filtered), **media** upload/download (signed URLs, AV scan)
- **Push notifications** worker, **moderation** (banned-terms + spam-velocity), **audit log**, **RBAC** roles
- **Group/channel creation over the protocol**, with member lists resolved from `@username`
- **Push notifications** per device, with jittered retries and dead-token removal
- **Retention**: the outbox, fired scheduled sends and the replay buffer are collected, and
  media blobs are deleted with their message (or swept when nothing references them)
- **Reactions, threads, typed attachments** (voice notes, round video notes, files,
  images) and **polls** with live tallies
- **Voice/video call signaling** (rooms, roster, opaque SDP/ICE relay) — media stays
  peer-to-peer, never through the server
- **Contacts + blocking** (incremental sync; a block stops traffic in both directions)
- **Forwarding** with provenance that survives the original, **self-destructing
  messages** (tombstone reaper), **scheduled send** (claimed with SKIP LOCKED,
  re-authorized at fire time)
- **Pinned messages** (chat-wide, admin-gated) and **drafts** synced across your own
  devices only
- **Public chat handles**, revocable **invite links** (128-bit codes, use/expiry caps,
  atomic redemption), **owner/admin roles** with last-owner protection
- **E2E secret chats**: X3DH + Double Ratchet (`pkg/e2e`), Ed25519 signed prekeys,
  **multi-device sync**; server relays opaque ciphertext
- **Transactional outbox** (FOR UPDATE SKIP LOCKED + LISTEN/NOTIFY) → **JetStream durable consumers**
- **Compression**: zstd + shared dictionary (negotiated), gzip fallback
- **QoS lanes**: control > messages > typing/presence; ephemeral frames droppable under load
- **Reliability**: circuit breaker + local fallback on Redis outage; **versioned migrations** (golang-migrate)
- **Observability**: Prometheus histograms (send→ack, fanout lag), pprof, OpenTelemetry (OTLP) tracing
- **Security hardening**: TLS 1.3, hashed tokens at rest, argon2id (bounded), handshake/idle deadlines,
  per-connection flood control + brute-force throttle, fuzzed parser — see [SECURITY.md](SECURITY.md)
- Per-connection sequencing, acks, backpressure, **one shared liveness reaper** (2 goroutines/conn)
- Snowflake IDs (Redis-leased node id); pluggable storage: **in-memory** (zero setup) or **Postgres + Redis + NATS**

**New here?** Read [GUIDE.md](GUIDE.md) — a from-scratch, beginner-friendly walkthrough
of everything (in Russian).

## Requirements

- Go 1.26+
- (Optional) Docker + Docker Compose for durable infra

## Quick start (zero setup, in-memory)

```bash
# terminal 1 — server (in-memory storage/bus/presence)
go run ./cmd/server

# terminal 2 — Alice (first time: -register creates the account)
go run ./cmd/client -register -user alice -pass secret123

# terminal 3 — Bob
go run ./cmd/client -register -user bob -pass secret123
```

In Alice's terminal:

```
/to @bob
hello bob!
/hist
/search hello
```

Bob sees the message arrive in real time and can reply with `/to @alice`.
Use `-register` on first login to create an account; drop it to log in afterward.

### Client commands

Everything acts on the current target, so a command reads as "do this here".
The exceptions are `/join` (joining is how you *get* a target) and the
membership commands, which take a concrete chat id rather than an `@user`.

| Command                                    | Effect                                                  |
|--------------------------------------------|---------------------------------------------------------|
| `/to @user`                                | target a direct chat with `@user`                       |
| `/to <chatID>`                             | target an existing chat id                              |
| `<text>`                                   | send text to the current target                         |
| `/hist [n]`                                | fetch the last `n` messages (default 20)                |
| `/read <seq>`                              | mark the chat read up to sequence `<seq>`               |
| `/typing`                                  | send a typing indicator                                 |
| `/search <text>`                           | full-text search across your chats                      |
| `/upload <path>`                           | upload a file and post it to the current target         |
| `/react <msgID> <emoji>`                   | toggle an emoji reaction                                |
| `/thread <msgID>`                          | fetch a message's reply branch                          |
| `/forward <msgID> <chat>`                  | copy a message into another chat, with provenance       |
| `/ttl <seconds>`                           | self-destruct what you send from now on (`0` = off)     |
| `/schedule <+2h\|RFC3339> <text>`          | send later                                              |
| `/scheduled` · `/unschedule <id>`          | list / cancel your pending sends                        |
| `/pin <msgID>` · `/unpin <msgID>` · `/pins`| chat-wide pinned messages                               |
| `/draft [text]` · `/drafts`                | cross-device draft (empty text clears it)               |
| `/poll Q\|A\|B` · `/vote <pollID> <n>`     | post a poll / vote in one                               |
| `/contact @user [name]` · `/contacts`      | add a contact / sync the address book                   |
| `/block @user` · `/unblock @user`          | block or unblock, in both directions                    |
| `/group <title> [@user...]`                | create a group (`/channel` for a channel)               |
| `/handle <name\|->`                        | claim (or clear) the chat's public handle (owner)       |
| `/invite [uses] [+24h]` · `/invites`       | mint / list invite links (admin)                        |
| `/revoke <code>`                           | kill an invite link (admin)                             |
| `/join <code\|@handle>`                    | join by invite link or public handle                    |
| `/role <userID> <member\|admin\|owner>`    | promote or demote a member (owner)                      |
| `/call [audio\|video]`                     | start a call; `/accept` `/decline` `/hangup`            |
| `/chats [cursor]`                          | page through the chats you are in                       |
| `/me` · `/who @user`                       | your profile / look a user up by handle or id           |
| `/name <display name>`                     | change your display name (`-` clears the avatar)        |
| `/export <id>`                             | dump a chat's members + messages (owner/admin)          |
| `/quit`                                    | disconnect                                              |

### WebSocket transport

The same protocol runs over WebSocket (browser/edge path):

```bash
go run ./cmd/client -ws ws://localhost:8080/ws -user carol -pass secret123
```

## Running with Docker infra (durable)

```bash
docker compose up -d          # Postgres, Redis, NATS

SYNAPSE_PG_DSN="postgres://synapse:synapse@localhost:5432/synapse?sslmode=disable" \
SYNAPSE_REDIS_ADDR="localhost:6379" \
SYNAPSE_NATS_URL="nats://localhost:4222" \
go run ./cmd/server
```

The server applies versioned migrations (golang-migrate,
`internal/store/postgres/migrations/`) itself on boot — no initdb script needed.

### Microservice fleet (the same system, split apart)

`cmd/server` is the modular monolith. The identical system also runs as
**independently deployable processes** talking gRPC (sync) + NATS (async):
`authd`, `chatd`, `messaged`, `presenced`, `keydird` (gRPC services), `fanoutd`/
`notifyd`/`moderationd`/`searchd` (bus workers), and `gatewayd` (the edge, which
dials the services as gRPC clients satisfying the same `gateway.Services`
interfaces). The gateway handler code is unchanged between the two.

```bash
docker compose -f deploy/microservices/docker-compose.yml up --build
go run ./cmd/client -addr localhost:7000 -register -user alice -pass secret123
```

See [deploy/microservices/README.md](deploy/microservices/README.md) for the
topology, the shared-state requirement, and the gRPC-hop latency tradeoff.

### Observability stack (optional)

```bash
SYNAPSE_OTLP_ENDPOINT=localhost:4318 go run ./cmd/server   # ship traces via OTLP
docker compose -f deploy/observability/docker-compose.yml up -d  # Prometheus + Tempo + Grafana
```

Grafana at http://localhost:3000 (Explore → Prometheus / Tempo). `/metrics` exposes
histograms for send→ack latency and fanout lag; `SYNAPSE_PPROF=1` mounts `/debug/pprof/`.

### QUIC transport (optional)

```bash
SYNAPSE_TLS_SELFSIGNED=1 SYNAPSE_QUIC=1 go run ./cmd/server
go run ./cmd/client -quic -insecure -addr localhost:7000 -register -user dave -pass secret123
```

QUIC (UDP, requires TLS) gives mobile clients connection migration (survives
WiFi↔LTE) and no head-of-line blocking.

### Server environment variables

| Variable                  | Default        | Meaning                                  |
|---------------------------|----------------|------------------------------------------|
| `SYNAPSE_TCP_ADDR`        | `:7000`        | raw-TCP binary-protocol listener         |
| `SYNAPSE_WS_ADDR`         | `:8080`        | WebSocket (`/ws`) + `/healthz`           |
| `SYNAPSE_PG_DSN`          | *(unset)*      | Postgres DSN — enables durable storage   |
| `SYNAPSE_PG_REPLICA_DSN`  | *(unset)*      | read-replica DSN — offloads history/read-receipt queries |
| `SYNAPSE_MESSAGE_SHARD_DSNS` | *(unset)*   | comma list of Postgres DSNs — shard the message write path by chat_id |
| `SYNAPSE_REDIS_ADDR`      | *(unset)*      | Redis addr — enables Redis presence      |
| `SYNAPSE_REDIS_PASSWORD`  | *(unset)*      | Redis password                           |
| `SYNAPSE_NATS_URL`        | *(unset)*      | NATS URL — enables NATS event bus        |
| `SYNAPSE_NODE_ID`         | *(hostname)*   | snowflake node id (0–1023); set explicitly per instance |
| `SYNAPSE_TLS_CERT`/`_KEY` | *(unset)*      | enable TLS 1.3 with a cert/key pair      |
| `SYNAPSE_TLS_SELFSIGNED`  | *(unset)*      | `1` = ephemeral self-signed TLS (dev)    |
| `SYNAPSE_QUIC`            | *(unset)*      | `1` = also listen on QUIC (UDP; requires TLS) |
| `SYNAPSE_REQUIRE_TLS`     | *(unset)*      | `1` = refuse to start without TLS (no silent plaintext) |
| `SYNAPSE_MAX_CONNS_PER_IP`| *(unset)*      | cap concurrent connections per source IP (flood guard) |
| `SYNAPSE_ACCEPT_RATE_PER_IP`| *(unset)*    | cap new connections/sec per source IP (storm guard) |
| `SYNAPSE_ALLOWED_ORIGINS` | *(unset)*      | comma list of allowed WebSocket origins  |
| `SYNAPSE_MEDIA_SECRET`    | dev default    | HMAC key for signing media URLs          |
| `SYNAPSE_ADMIN_USERS`     | *(unset)*      | comma list of platform-admin user ids (RBAC) |
| `SYNAPSE_MODERATOR_USERS` | *(unset)*      | comma list of moderator user ids (RBAC)  |
| `SYNAPSE_TRACE`           | *(unset)*      | `stdout` prints OpenTelemetry spans      |
| `SYNAPSE_OTLP_ENDPOINT`   | *(unset)*      | OTLP/HTTP collector (e.g. `localhost:4318`) |
| `SYNAPSE_PPROF`           | *(unset)*      | `1` mounts `/debug/pprof/`               |
| `SYNAPSE_WRITE_BATCH`     | `on`           | `off` disables group-commit batching     |
| `SYNAPSE_AUTH_HASH_CONCURRENCY` | *(NumCPU)* | max concurrent argon2id hashes (auth-flood guard) |
| `SYNAPSE_SEND_RATE`       | `20`           | per-connection msgs/sec flood limit (raise for load tests) |
| `SYNAPSE_REGION`          | `local`        | region label (multi-region hook)         |

Any subset can be set; unset backends fall back to in-memory.

## Tests & CI

```bash
go test ./...                                   # unit + end-to-end
make ci                                         # vet + build + race tests + govulncheck + gosec
go test ./pkg/wire -fuzz=FuzzParser -fuzztime=30s   # fuzz the protocol parser
```

`.github/workflows/ci.yml` runs the race-detector suite plus **govulncheck**
(CVE scan of dependencies and the stdlib), **gosec** (static security analysis),
and a short parser fuzz on every push/PR.

`internal/gateway/integration_test.go` drives real clients through the gateway
and asserts delivery, ordering, idempotency, **cross-node delivery** (two nodes),
**resume replay**, chat export, forward provenance + self-destruct deadlines
reaching the client, and a **full E2E Double-Ratchet exchange**.
`internal/rpc/message_test.go` runs the same write/read path over a real gRPC hop,
and `internal/gateway/fleet_integration_test.go` drives a gateway whose services
are **all** remote — so the microservice split cannot quietly drop a field the
monolith delivers.
Integration tests needing infra skip unless their env DSN is set
(`SYNAPSE_TEST_PG_DSN`, `SYNAPSE_TEST_REDIS_ADDR`).

**Sharded message store.** `SYNAPSE_TEST_SHARD_DSNS` (two or more comma-separated
DSNs) runs `internal/store/sharded` and `internal/platform` against real shards:
co-location and gap-free per-chat sequence on each backend, one outbox per shard,
and the capabilities that cross shards — the self-destruct reaper and the media
reference check — reaching data the hash placed in a shard nobody named.

Give every DSN a database of its own, including versus `SYNAPSE_TEST_PG_DSN`. The
outbox is a global table that both suites drain, so two of them pointed at one
database delete each other's staged events:

```bash
SYNAPSE_TEST_PG_DSN="postgres://synapse:synapse@localhost:5432/synapse_primary?sslmode=disable" SYNAPSE_TEST_SHARD_DSNS="postgres://synapse:synapse@localhost:5433/synapse?sslmode=disable,postgres://synapse:synapse@localhost:5434/synapse?sslmode=disable"   go test ./...
```

### Load test

```bash
SYNAPSE_SEND_RATE=100000 go run ./cmd/server            # raise the flood cap
go run ./cmd/loadtest -addr localhost:7000 -conns 200 -msgs 50   # throughput mode

# idle-scale mode: hold N connections open, report per-connection server cost
SYNAPSE_PPROF=1 go run ./cmd/server
go run ./cmd/loadtest -addr localhost:7000 -conns 5000 -idle 30s
```

Throughput mode reports p50/p95/p99/max send→ack latency; idle-scale mode reports
goroutines and GC-forced retained heap **per connection** (measured: ~2 goroutines
and ~24 KiB/conn → ~2.3 GiB projected at 100k connections on one node).

## Layout

```
pkg/wire        custom binary protocol (framing, envelope, protobuf codec, zstd, TCP/WS/QUIC)
pkg/id          snowflake id generator
pkg/eventbus    async bus abstraction (in-memory + NATS JetStream)
pkg/e2e         E2E crypto (X3DH + Double Ratchet, Ed25519 prekey signatures)
pkg/ratelimit   token-bucket flood control
pkg/breaker     circuit breaker for external deps
pkg/mtls        mutual-TLS config helper for service-to-service auth
proto/          protobuf schemas → generated internal/wirepb
internal/model  domain entities
internal/store  persistence contracts + memory and postgres (group-commit batcher, migrations)
internal/auth   identity / sessions (argon2id, hashed tokens, hash-concurrency guard)
internal/chat   chats, membership, cached authorization, seq allocation
internal/message message write/read + command broker (create/edit/delete/forward)
internal/fanout  event-driven delivery, routed to owning nodes
internal/router  cross-node user→node registry (memory + Redis, resilient)
internal/keydir  E2E prekey directory (memory + Redis)
internal/media · search · moderation · notify · audit   V1 services
internal/reaction · poll · call   reactions, polls with tallies, call signaling
internal/contact contacts + block list (incremental sync)
internal/schedule deferred sends (dispatcher) + self-destruct reaper
internal/pin     chat-wide pins + per-user cross-device drafts
internal/invite  public handles, invite links, member roles
internal/rpc     gRPC adapters (server + client) for the microservice split
internal/platform daemon bootstrap shared by cmd/*d (stores, bus, mTLS, metrics)
internal/presence online / last-seen / typing (memory + redis)
internal/delivery in-node user→connection hub
internal/outbox  transactional-outbox relay
internal/replay  per-session resume buffer (memory + Redis)
internal/nodeid  distributed snowflake node-id lease (Redis)
internal/metrics · tracing   Prometheus + OpenTelemetry
internal/gateway realtime gateway (handshake, auth, dispatch, QoS, backpressure, reaper, QUIC)
cmd/server      all-in-one runner (modular monolith)
cmd/gatewayd · authd · chatd · messaged · presenced · keydird   gRPC daemons
cmd/fanoutd · notifyd · moderationd · searchd   bus workers
cmd/client      interactive CLI client (TCP/WS/QUIC/TLS)
cmd/loadtest    concurrent send→ack latency/throughput harness
deploy/observability  Prometheus + Tempo + Grafana compose
```

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full design, tradeoffs, service
catalog, message-flow sequence, storage matrix, risk register, and roadmap.
