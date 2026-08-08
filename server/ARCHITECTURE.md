# Synapse — Backend Architecture & System Design

A Telegram-class messenger backend in Go. This document is the system design
doc; the repository is a runnable MVP of its core. Where a section is only
partially built, it is marked **[implemented]**, **[stubbed]**, or
**[designed — not built]** so the boundary between "working code" and "planned"
is never ambiguous.

**Decisions are stated, not surveyed.** Each section gives one recommended
default and the reason. Deviations are called out as tradeoffs.

---

## Section 1 — Product scope

| Tier | Features | Status |
|------|----------|--------|
| **MVP** (this repo) | 1:1 chats, group/channel model, send/deliver/ack, per-chat ordering, read receipts, typing, presence/last-seen, multi-device delivery, edit/delete, history backfill, idempotent writes, reconnect/resume, binary protocol over **TCP + WS + QUIC** | **[implemented]** |
| **V1** (this repo) | Media pipeline (signed URLs, AV scan), push notifications (pluggable provider), full-text search, moderation/abuse rules, rate/flood limiting, **RBAC + audit log**, **observability stack** (Prometheus/Tempo/Grafana) | **[implemented]** |
| **V1 remaining** | QR desktop login, admin API | **[designed]** |
| **Product surface** (this repo) | Emoji reactions, threads, typed attachments (voice notes, round video notes, files, images), polls with live tallies, **voice/video call signaling**, contacts + blocking, forwarding with provenance, self-destructing messages, scheduled send, pinned messages, cross-device drafts, public chat handles, invite links, admin roles | **[implemented]** |
| **V2** | Secret (E2E) chats — **Double Ratchet crypto [implemented] + server relay + multi-device sync [implemented]**; **JetStream durable replay [implemented]**; multi-region active-active, wide-column message store, federation-grade abuse ML | **[mixed — crypto+durable-bus built, multi-region designed]** |

**Explicitly NOT in the first version** (dangerous complexity to defer):
E2E encryption, multi-region replication, a bespoke wide-column store, exactly-once
semantics, and voice/video **media**. Each multiplies operational surface for little
MVP value. Start Postgres-only, single-region, at-least-once with idempotency.

Two of those deferrals have since been taken up, and the line drawn matters: E2E
crypto landed in full, while for calls only the **signaling** did. Media never
flows through the server — the SDP/ICE payloads it relays are opaque to it, and
carrying the media plane (an SFU, transcoding, bandwidth estimation) remains out
of scope. The rest of the product surface above is message-layer work that reuses
the existing write path, fanout, and authorization rather than adding a tier.

**Must-have vs optional:** durable ordered messaging, auth, and reconnect are
must-have. Typing/presence, edit/delete, and compression are high-value but
non-blocking. Media and search are V1.

---

## Section 2 — High-level architecture

Ten layers, each a seam where responsibility (and failure) is isolated:

```
                              CLIENTS
     (mobile = TCP/QUIC,  web = WebSocket, desktop native = TCP, CLI)
                                 │
                     [1] Client transport layer
            raw TCP ───────┼─── WebSocket (/ws) ─── QUIC (UDP)
                                │
                     [2] Binary protocol layer  (pkg/wire)
                     framing • envelope • seq/ack • compression
                                │
      ┌─────────────────────────▼──────────────────────────┐
      │        [4] REALTIME GATEWAY  (internal/gateway)      │
      │  [3] session/auth/security: handshake, token, resume │
      │  per-conn seq/ack • backpressure • heartbeat • sink  │
      └───────┬───────────────┬───────────────────┬─────────┘
              │ gRPC (logical) │                   │ delivery.Hub
              ▼                ▼                   ▼
   [5] MESSAGING DOMAIN SERVICES               live connections
   auth · chat · message(write/read) · presence
              │  emits events                    ▲
              ▼                                   │ push
   [7] EVENT BUS (pkg/eventbus: NATS/mem) ───► [5] fanout/delivery
              │                                   │
     ┌────────┼─────────────┐                     │ offline
     ▼        ▼             ▼                      ▼
 [9] notify [11] media  [12] search      [6] STORAGE LAYER
  worker    pipeline    indexer       Postgres · Redis · NATS · (S3)
 (impl)     (impl)      (impl, PG FTS)
                                        [10] moderation/audit/RBAC (impl)
```

**Why this separation matters.** The write path (durability, ordering) and the
delivery path (fanout, presence) have opposite characteristics: writes must be
consistent and are low-rate per chat; delivery is best-effort and explosively
high fan-out (a channel post → millions of sends). Coupling them means a slow
consumer stalls a durable write, or a write outage blocks all delivery. The
event bus is the firebreak: `message.Send` returns as soon as the row is durable
and the event is published; everything downstream (fanout, push, search,
moderation) is an independent consumer that can scale, retry, and fail alone.

---

## Section 3 — Custom protocol design **[implemented: `pkg/wire`]**

**Binary schema choice: fixed binary frame header + varint-packed envelope
header + typed body.** Rationale: a fixed header is trivial and fast to parse
and fuzz; varints keep the per-message header ~6–16 bytes; the body is
pluggable. Bodies are **protobuf** (schemas in `proto/synapse/v1/`, generated Go
in `internal/wirepb`, codec in `pkg/wire/protocodec.go`); the codec is swappable
(`SetBodyCodec`, JSON codec kept for debugging) without touching framing/envelope.
We reject "protobuf everywhere" for the frame because self-describing framing +
an explicit length prefix is easier to make robust against hostile input.

### Framing (`frame.go`)

```
+--------+--------+--------+--------+--------------------+==================+
| 0x53   | 0x43   | VER(1) | FLAGS  | LENGTH (4, BE)     | PAYLOAD (LENGTH)  |
| 'S'    | 'C'    | 0x01   | bits   | uint32 ≤ 16 MiB    | envelope bytes    |
+--------+--------+--------+--------+--------------------+==================+
```

- **Magic** `SC` — cheap rejection of garbage/port scans.
- **Version** — bumped only on incompatible framing changes.
- **Flags** — bit0 `COMPRESSED` (gzip); bits 1–7 reserved.
- **Length** — capped at 16 MiB to bound allocations (anti-DoS). Media never
  travels as a frame; only a reference does.
- WebSocket: one binary WS message == one frame (same codec, `wsconn.go`).

### Envelope (`envelope.go`) — all fields LEB128 varints

```
Type · Seq · Ack · RequestID · len(Body) · Body
```

- **Type** (`types.go`): HELLO, WELCOME, AUTH/OK/ERR, PING/PONG, SEND, SEND_ACK,
  NEW, READ, READ_UPD, TYPING, PRESENCE, EDIT, DELETE, HISTORY/OK, T_ACK,
  RESUME/OK, ERROR — plus media (20s), transport control (30s), errors (40),
  secret chats (50s), search (60s), admin export (70s), and the product blocks:
  reactions + threads + polls (80s), call signaling (90s), contacts and blocking
  (96–100), forwarding/scheduling (101–105), pins and drafts (106–112), public
  handles, invite links and roles (113–119), chat creation and push registration
  (120–122). **Numbers are allocated in blocks by
  area and never reused**: a client that does not know a type ignores it, so a
  renumbering would silently change meaning instead of failing loudly.
- **Seq** — per-connection monotonic sender sequence → ordering + gap detection
  + resume.
- **Ack** — highest contiguous Seq the sender has processed, **piggybacked** on
  every frame (cheap cumulative ack; explicit `T_ACK` when idle).
- **RequestID** — request/response correlation, independent of ordering, so many
  requests multiplex over one connection.

### Behaviors

- **Versioning:** frame `VER` for wire breaks; capability bitset (`Cap`) in
  HELLO/WELCOME for feature negotiation. Unknown cap bits are ignored → old
  server + new client agree on the intersection. **Extensibility rule:** never
  renumber a `MsgType` or `Cap` bit; only append.
- **Correlation / ack / retries:** RequestID pairs replies to requests; SEND_ACK
  confirms durability; the client retries unacked SENDs with the **same
  DedupKey** (see idempotency).
- **Sequencing / dedup:** per-connection `Seq` orders transport; per-chat `Seq`
  (server-assigned) orders messages; `(sender, DedupKey)` dedups writes.
- **Heartbeat:** server PINGs every 20 s; any inbound frame refreshes liveness;
  60 s idle → close. Client answers PING with PONG.
- **Reconnect / resume:** on drop, client reconnects, sends HELLO then RESUME
  with its `resume_token` + `last_ack_seq`. Server validates and replays the gap
  (MVP: acknowledges + relies on client `HISTORY` backfill; production keeps a
  short per-session replay buffer in Redis).
- **Backpressure & QoS lanes:** outbound frames go to three priority lanes —
  control (pong/ack/error) > messages > typing/presence. The writer drains
  hi→mid→lo, so an ack never waits behind a fanout backlog; the lo lane is
  droppable under load (losing a "typing…" is fine), a full hi/mid lane drops the
  slow connection (it resyncs via history). **[implemented]**
- **Batching / multiplexing:** RequestID enables multiplexing today; `CapBatching`
  reserves multi-envelope frames for later.
- **Compression:** **zstd with a shared dictionary** when negotiated (`CapZstd`),
  gzip fallback (`CapCompression`); primes the compressor with recurring protobuf
  field tags + common chat tokens so short frames shrink well. Decompress is
  length-bounded (zip-bomb guard).
- **Bodies:** encoded as **protobuf** (`proto/synapse/v1/` → `internal/wirepb`)
  behind a swappable codec; framing/envelope unchanged.
- **Error codes:** ranged (`1xxx` transport, `2xxx` auth, `3xxx` business, `4xxx`
  throttle, `5xxx` server) so clients react by class.

### Sample lifecycle (client send → recipient)

```
Alice SEND{chat,dedup=k,text} ─▶ Gateway
  authorize(CanPost) ─▶ message.Send
    InsertMessage (tx: BumpSeq chat.last_seq → INSERT)  [durable]
    publish message.created (key=chatID)                [event]
  ◀─ SEND_ACK{msg_id, chat_seq, ts, dup=false}          to Alice
                     event bus
  fanout consumes ─▶ members(chat) ─▶ Hub.Route(bob)
      Bob online  ─▶ NEW{...}   to Bob's devices
      Bob offline ─▶ publish notify.push
```

### Anti-replay & idempotency

- **Idempotency:** `(sender_id, dedup_key)` unique index. A retried SEND resolves
  to the stored message (`Duplicate=true`) and **consumes no new chat seq** — the
  seq bump and insert share one transaction, so a dedup loss rolls back the bump
  (no ordering gaps). **[implemented]**
- **Anti-replay (transport):** monotonic per-connection `Seq` lets the server
  reject/deduplicate replays on a live connection. Cross-connection replay is
  bounded by session tokens + resume-token validation. Under TLS (production
  edge), record-layer replay is already handled by the transport. **[implemented
  at app layer; TLS designed]**

---

## Section 4 — Security model

### A. Cloud-chat mode (default) **[implemented: transport + server-side sync]**

- **Auth flow:** username/password → argon2id verify → opaque 256-bit session
  token + resume token (`internal/auth`).
- **Transport crypto:** TLS 1.3 at the edge (raw TCP and WSS). The custom
  protocol rides *inside* TLS — we do not invent transport crypto. **[designed:
  terminate TLS at ingress; MVP runs plaintext for local dev.]**
- **Session keys / device binding:** one session per (user, device); token bound
  to device id; resume token re-establishes without re-auth.
- **Key rotation:** session tokens are rotated by revoke+reissue; TLS certs via
  cert-manager. No long-lived symmetric secrets in the app.
- **Integrity / replay:** TLS gives confidentiality+integrity on the wire;
  per-connection Seq gives app-level replay resistance.
- **Metadata exposure:** server sees everything (it must, to sync/search/moderate).
  This is the deliberate Telegram-cloud tradeoff.
- **What the server can read:** all cloud-chat content. **Cannot** recover
  passwords (argon2id, one-way).

### B. Secret-chat mode (optional) **[crypto implemented: `pkg/e2e`; server relay implemented]**

- **E2E:** Signal-style **X3DH** key agreement + **Double Ratchet**
  (X25519 + HKDF-SHA256 + ChaCha20-Poly1305). All standard, audited primitives —
  no home-grown crypto. Implemented and tested (round-trip, bidirectional,
  out-of-order, tamper-rejection) in `pkg/e2e`. The gateway relays opaque
  ciphertext (`MsgSecretSend/Recv`) and stores only public prekeys
  (`internal/keydir`); it can never derive a key or read a message.
- **Device binding / multi-device sync:** per-device identity keys; a sender
  fetches every device's prekey bundle (`KEY_FETCH_ALL`) for the peer AND its own
  other devices and encrypts a copy to each, so secret chats **sync across all of
  a user's devices** (Signal-style per-device sessions). The server still sees
  only ciphertext, so secret chats remain unindexable.
- **Rotation:** the ratchet rotates message keys every message; forward secrecy +
  post-compromise security.
- **What the server can read:** ciphertext + routing metadata only.

**Ratcheting vs cloud-sync tradeoff:** cloud sync gives multi-device history,
server search, and instant new-device onboarding, at the cost of server-readable
content. Ratcheting gives strong secrecy but no server-side multi-device sync or
search. **Recommendation:** cloud chats are the default product; secret chats are
an opt-in per-conversation mode. This matches user expectations and keeps the
hard crypto off the critical path.

---

## Section 5 — Identity, auth, sessions **[implemented: `internal/auth`]**

- **Registration/login:** argon2id (memory-hard, 64 MiB) password hashing;
  constant-time verify; dummy-hash on unknown user to equalize timing (anti-
  enumeration).
- **Multi-device:** each device gets its own session + delivery cursor; fanout
  targets all of a user's devices.
- **Refresh/revoke:** sessions carry `expires_at` + `revoked_at`; revocation is
  instant server-side (the reason we chose opaque tokens over JWT).
- **Device list / QR login / bot creds:** device rows exist; QR login (desktop
  scans a code that binds a session) and bot tokens are **[designed]**.
- **Risk-based signals:** device fingerprint, IP reputation, velocity → step-up
  auth. **[designed]**
- **Token model — recommendation: opaque session tokens (hybrid-ready).** Opaque
  tokens give O(1) revocation, tiny wire size, and no key distribution — ideal for
  a long-lived realtime connection. Add short-lived JWTs later *only* for
  stateless edge checks if a CDN/edge tier needs them. We reject JWT-as-session
  because realtime needs instant revocation.

---

## Section 6 — Microservices (service catalog)

Built along these boundaries and runnable **two ways**: as a modular monolith
(`cmd/server`, one process, zero-setup in-memory) **or as an independently
deployable fleet** (`deploy/microservices`) — each service a standalone process
speaking **gRPC** on the sync path and **NATS** on the async path. The seam is
`internal/gateway/services.go`: the gateway depends only on service interfaces,
so a local `*auth.Service` and a gRPC `rpc.AuthClient` are drop-in equivalents
and the handler code is byte-identical between the two topologies. Contracts live
in `proto/synapse/v1/services.proto` → `internal/rpc`; the daemons are
`cmd/authd|chatd|messaged|presenced|keydird` (gRPC) and `cmd/fanoutd|notifyd|
moderationd|searchd` (bus workers), fronted by `cmd/gatewayd`. **Impl** = present
in this repo. The split is **verified end-to-end** (client → gatewayd →
authd/chatd/messaged over gRPC with shared Postgres/Redis/NATS); the gRPC hop
trades throughput for independent scaling — a deliberate cost, so the monolith
stays the right default until a service needs to scale on its own.

| Service | Purpose | Sync API | Consumes | Emits | Owns (DB) | Consistency | Scaling | Key failure mode | Impl |
|---|---|---|---|---|---|---|---|---|---|
| **Realtime Gateway** | Terminate conns, protocol, auth/resume, route | binary proto | — | — | none (stateless) | — | horizontal, sticky-ish | conn storm on restart | ✅ |
| **Auth/Session** | Register, login, validate, revoke | gRPC | — | — | users, devices, sessions (PG) | strong | horizontal (read-heavy) | token store down → no new logins | ✅ |
| **Chat** | Chats, membership, authz, seq alloc | gRPC | — | chat.member.* | chats, members, direct_index (PG) | strong | horizontal | seq counter contention (hot chat) | ✅ |
| **Message Write** | Authorize+persist+emit | gRPC | — | message.created/edited/deleted | messages (PG→wide-col) | strong per chat | horizontal by chat shard | write amplification | ✅ |
| **Message Read/Sync** | History, cursors | gRPC | — | — | messages, read_state | read-your-writes | horizontal | hot-chat read fanout | ✅ |
| **Fanout/Delivery** | Event → connected devices; offline→push | — | message.*, chat.typing, message.read | notify.push | none | eventual | horizontal by chat key | slow consumer lag | ✅ |
| **Presence** | Online/last-seen/typing | gRPC | — | user.presence | presence (Redis) | eventual, TTL | horizontal | Redis blip → stale presence | ✅ |
| **Notification** | Push to APNs/FCM | — | notify.push | — | notification_jobs | at-least-once | horizontal | provider outage | ✅ (pluggable provider; LogProvider default) |
| **User/Contact/Graph** | Profiles, contacts, blocks | gRPC | — | — | users, contacts | strong | horizontal | block list stale → unwanted DM lands | ✅ (`internal/contact`; blocks enforced both directions) |
| **Media** | Upload/download, signed URLs, CDN | HTTP + proto tickets | media.uploaded | media.scanned | object store + metadata | eventual | horizontal | scan backlog | ✅ (fs store; scan/transcode designed) |
| **Search Indexer** | Index + query | proto (MsgSearch) | message.* | — | inverted index → ES | eventual | horizontal | index lag | ✅ (in-memory index) |
| **Moderation/Abuse** | Flood, spam, report actions | — | message.* | abuse.action | abuse_events | eventual | horizontal | false positives | ✅ (banned-term + spam-velocity) |
| **Key Directory** | E2E prekey bundles | proto (KeyPublish/Fetch) | — | — | prekeys (Redis/PG) | strong | horizontal | prekey exhaustion | ✅ (in-memory) |
| **Admin** | Ops console, bans | REST | — | audit.* | — | strong | small | privilege misuse | ⬜ designed |
| **Config/Flags** | Feature flags, tunables | gRPC | — | — | config (PG) | eventual | small | stale flag | ⬜ designed |
| **Audit/Compliance** | Append-only audit log | — | audit.*, abuse.* | — | audit_logs (append-only) | strong-ish | horizontal | log gap | ⬜ designed |
| **Observability** | Metrics/traces/logs | — | — | — | Prom/Tempo/Loki | — | — | pipeline overload | ⬜ designed |

Internal transport: **gRPC** for request/response (typed, streaming, deadline
propagation) + **event bus** for async facts. In the MVP these are in-process Go
calls behind the same interfaces, so extraction is mechanical.

**The two topologies must carry the same data, not just the same calls.** A field
present in the domain type but missing from `services.proto` costs nothing in the
monolith and disappears on the fleet — the deployment, not the code, decides what
a user sees. So `SubmitRequest` carries the attachment and the self-destruct TTL,
and the `Message` reply carries attachment, thread root, reply count, forward
provenance and expiry: every field a client can observe. `internal/rpc` is tested
over a real gRPC hop (`message_test.go`) precisely because this class of loss is
invisible to unit tests on either side of it.

---

## Section 7 — Data model **[implemented: `internal/model`, `migrations/`]**

Core entities: `users`, `devices`, `sessions`, `chats`, `chat_members`,
`messages`, `read_state`, `direct_index` (+ designed: `message_versions`,
`delivery_state`, `notification_jobs`, `abuse_events`, `audit_logs`).

Product entities, added by versioned migrations `000003`–`000010`:
`reactions`, `calls` + `call_participants`, `polls` + `poll_votes`, `contacts`,
`scheduled_messages`, `pinned_messages`, `drafts`, `invite_links`, plus columns on
existing tables — `messages.attachment` (JSONB), `messages.thread_root` /
`reply_count`, `messages.fwd_*` and `messages.expires_at`, and `chats.username`.

- **Attachment as a column, not a table.** An attachment has no life of its own:
  it is created with its message, read with it, and deleted with it. A JSONB
  column keeps that one-to-one and spares every history read a join; the bytes
  live in the object store regardless.
- **Forward provenance as plain columns, not a foreign key.** A forward must
  survive the original being deleted, so it stores a *snapshot* of where it came
  from. A reference would either break or resurrect deleted content.
- **Threads via a resolved root.** `thread_root` is computed server-side at write
  time from `reply_to`, so a whole branch shares one root and a thread is a single
  indexed read instead of a recursive walk — and a client cannot forge a root it
  has no access to.
- **Scheduled sends live outside the message log.** A pending send holds no chat
  `seq` (cancelling one would leave a permanent gap in a gap-free sequence) and is
  invisible to history, search and fanout until it fires.
- **Self-destruct is a tombstone, not a delete**, for the same reason: the row
  goes, the sequence position stays. A partial index over `expires_at > 0` keeps
  the reaper's scan to the small expiring set.
- **Everything that only ever grows is collected.** The outbox stages a full copy
  of each message, so a relay that only marks rows sent turns the handoff table
  into a second, permanent message log; published rows are collected past a short
  retention window, in bounded chunks so the DELETE never holds locks the write
  path can feel. Fired scheduled sends and idle replay buffers follow the same
  rule. Media is deleted with its message when nothing else references it (a
  forward carries a copy of the ref) and swept otherwise — "self-destruct" that
  applies only to text is not self-destruct.

- **IDs — Snowflake (`pkg/id`).** 63-bit: 41-bit ms + 10-bit node + 12-bit seq.
  Chosen over ULID/UUIDv7 for message ids: 64-bit ints are cheap to index/store,
  time-sortable, and encode origin node — collision-free across a sharded write
  tier without coordination.
- **Ordering — per-chat `seq`.** `chats.last_seq` is bumped atomically inside the
  message-insert transaction; `UNIQUE(chat_id, seq)` guarantees gap-free order.
  The message `id` (snowflake) is the global identity; `seq` is the per-chat
  order. Clients order by `seq`.
- **Keys/shard keys:** `messages` PK `id`, **shard key `chat_id`** (co-locates a
  chat's log). `read_state` PK `(chat_id, user_id)`. Indexes:
  `messages(chat_id, seq DESC)` for history; partial unique
  `messages(sender_id, dedup_key)` for idempotency.
- **Retention / hot-cold:** recent messages hot (Postgres/Scylla); older tiers to
  cheaper storage / object archive; tombstones kept for audit. **[designed]**
- **Append-only:** `messages` is effectively append + tombstone; `audit_logs` is
  strictly append-only. Edits are in-place in MVP; `message_versions` history is
  designed.

---

## Section 8 — Storage strategy (decision matrix)

| Responsibility | Choice | Why (decision) | Alternatives rejected |
|---|---|---|---|
| Account/chat metadata | **PostgreSQL** | Relational integrity, transactions for seq+insert, easy ops | Mongo (weak tx), Dynamo (early complexity) |
| Message log | **Postgres now → Scylla/Cassandra later** | Start simple; `(chat_id, seq)` partition maps cleanly to wide-column at scale | Kafka-as-store (bad point reads) |
| Presence/typing | **Redis** (TTL keys) | Ephemeral, high-churn, auto-expiring online markers, no cleanup job | Postgres (write amplification) |
| Event bus | **NATS JetStream** | Sub-ms fanout + durable consumers (a down worker resumes on restart) for `message.*`/`notify.*`; core NATS for ephemeral `deliver.<node>`/typing/presence. **[implemented]** | Kafka (higher latency for realtime fanout) |
| Media blobs | **S3-compatible object store + CDN** | Cheap, durable, signed URLs, offloads bytes from app | DB blobs (never) |
| Full-text search | **Postgres tsvector now → OpenSearch later** | Shared GIN index across nodes; interface swaps to OpenSearch for ranking. **[implemented]** | in-memory per-node (breaks multi-node) |

**Partitioning:** messages by `chat_id`; users by `user_id`. **Replication:**
Postgres primary + sync standby (RPO≈0 in-region); Redis replica; NATS clustered.
**Quorum/consistency:** metadata strongly consistent (single primary); messages
strongly consistent *per chat*, eventually consistent globally; presence eventual.
**Backups:** Postgres WAL archiving + PITR; nightly base backups; object store
versioning. **DR:** cross-region async replica, promotable; target RTO ≤ 15 min,
RPO ≤ 1 min. **Cross-region:** async replication + region-pinned chats initially;
active-active is V2. **Postgres-only early tradeoff:** perfectly fine to low
millions of MAU; the first thing to move is the message table (largest, append-
heaviest) once a single primary's write/IO saturates — the `MessageStore`
interface makes that swap local. Two scale-out paths are **implemented** behind
that interface: a **chat_id-sharded message store** (`internal/store/sharded`,
wired via `SYNAPSE_MESSAGE_SHARD_DSNS`) that spreads writes across N Postgres
shards, and an optional **read replica** (`SYNAPSE_PG_REPLICA_DSN`) that serves
history/read-receipt queries off the primary. A shard is a COMPLETE message store, not a partial one: it
carries the full schema and stages its own outbox, which the relay drains per
shard — a relay pointed only at the primary would silently lose the events of
every other shard. Sharding is correct because the
per-chat sequence is co-located with the messages: a dedicated `chat_seq` table
(migration 000002) is UPSERTed on each write, so a shard holding only a chat's
messages+seq+outbox allocates a gap-free seq locally while chat metadata stays
central. Verified live: 8 messages distributed across two shard DBs, each chat on
one shard, zero rows in the primary message table.

**Write throughput — group commit.** The single-node write ceiling is commit
fsync (one tx/message = one fsync). A **group-commit batcher**
(`internal/store/postgres/batch.go`) coalesces concurrent inserts arriving in a
~2ms window into one transaction + one commit, pipelining the statements with
`pgx.Batch` (~2 round-trips for the whole batch). This lifted a measured
single-node run from **230 → 3760 msg/s (p50 832ms → 45ms)** while KEEPING
durability — the `synchronous_commit=off` win without dropping it. Per-chat seq
stays exact; a failed batch falls back to per-message transactions. **[implemented]**

---

## Section 9 — Message flow & consistency **[implemented]**

All message mutations (create/edit/delete) enter through a single **command
broker** (`message.Broker`, `internal/message/broker.go`) — kept inside the
message service, not a separate microservice, because the three ops share one
aggregate and one transaction (seq + insert + outbox). The broker centralizes
validation (max text length, non-empty), tracing, and per-op metrics behind one
typed `Submit(Command)` entry point.

Lifecycle: client SEND → gateway validate → auth/session check → `CanPost`
authz → **write tx { bump chat.last_seq; insert message }** (durable) → publish
`message.created` → fanout to members' devices → recipient inbox (live push or
history sync) → SEND_ACK to sender → READ receipt later. Retries carry the same
DedupKey; duplicates are suppressed by the unique index; offline recipients get a
push job.

**Consistency guarantees:**

- **Strongly consistent:** message durability + per-chat ordering (single tx,
  unique seq); auth/session; membership.
- **Eventually consistent:** delivery, read receipts, presence, search index,
  push.
- **Per-chat ordered:** yes — every client sees the same `seq` order.
- **Globally unordered:** across different chats there is no global order (by
  design; snowflake ids are only *approximately* time-ordered across chats).
- **Delivery semantics:** **at-least-once with idempotent effects.** Writes are
  idempotent via DedupKey; deliveries may repeat and clients dedup by
  `message_id`. We do **not** promise exactly-once on the wire (impossible
  cheaply); we make effects exactly-once via idempotency. Client-visible
  ordering = per-chat `seq`, gap-free.

---

## Section 10 — Realtime infrastructure **[implemented: multi-node gateway; edge TLS optional]**

- **Persistent TCP + WebSocket + QUIC** — same binary protocol over all three
  (native → TCP, browsers → WSS, mobile → QUIC). QUIC (`internal/gateway/quic.go`,
  enable with `SYNAPSE_QUIC=1`) gives **connection migration** (survives WiFi↔LTE
  IP changes without reconnect), no head-of-line blocking, and a faster TLS 1.3
  handshake; the frame codec is stream-generic so all transports share it.
  **[implemented]**
- **Accept scaling** — several accept goroutines per listener (`AcceptLoops`);
  SO_REUSEPORT documented for multi-process. **[implemented]**
- **Balancers / routing — stateless, multi-node gateway.** The gateway holds no
  authoritative state, so **L4 load balancing to any node** works; no sticky
  sessions. Cross-node delivery is real (`internal/router`): each node registers
  its users in a shared registry (Redis), fanout looks up a recipient's nodes and
  publishes a node-targeted delivery on the bus, and the owning node pushes to its
  local Hub. Verified by a two-node test. Shared state (prekeys, search, presence,
  routing, resume buffer) lives in Redis/Postgres so any node serves any user.
  **[implemented]**
- **Session resumption** — resume token + last_ack_seq + a per-session replay
  buffer (`internal/replay`, memory or Redis stream); on RESUME the gateway
  replays exactly the missed frames and continues numbering from the session
  high-water. Falls back to history if no buffer. **[implemented]**
- **Regional gateways / edge termination** — TLS terminated at regional ingress;
  users connect to nearest region. **[designed]**
- **Congestion / mobile QoS** — bounded per-conn queues with **priority lanes**
  (control > messages > typing/presence, lo droppable), zstd compression, small
  headers, heartbeat tuned for radio sleep. **[implemented]**
- **Goroutines per connection** — two (read + write); liveness (idle-close + ping)
  is one shared per-node **reaper** goroutine, not a timer per connection, and
  presence/router TTL refresh is throttled on client activity. Saves ~1M
  goroutines at 1M connections. **[implemented]**
- **Reconnect storms** — jittered client backoff + server accept rate-limit +
  resume (cheap) instead of full re-auth. **[resume implemented; limiter designed]**

---

## Section 11 — Media / file pipeline **[implemented: signed URLs + object store; scan/transcode designed]**

Initiate upload (get signed S3 URL + media_id) → chunked, resumable upload
directly to object store → on completion emit `media.uploaded` → async workers
run **virus scan** (ClamAV hook), **metadata extraction**, **thumbnail/transcode**
→ emit `media.scanned` → message references `media_ref` only. Download via
**signed, expiring CDN URLs** gated by chat-membership access checks. Abuse:
hash-match known-bad content, per-user upload quotas, quarantine on scan failure.
The protocol already carries only a `media_ref`, never bytes — the pipeline is
additive.

---

## Section 12 — Search **[implemented: shared Postgres tsvector + permission filter]**

Indexing pipeline consumes `message.created/edited/deleted` → normalizes → writes
to a **shared Postgres tsvector (GIN) index** so every node reads/writes one
index (in-memory inverted index for single-node dev; OpenSearch is the same
`Backend` interface later). Queries are
**permission-filtered** by the caller's chat membership (never search chats you
aren't in). Ranking: recency-boosted BM25. Reindex jobs replay from the message
store. Deletion propagates via `message.deleted` tombstones. **Secret chats are
not indexable** (server has only ciphertext) — search is a cloud-chat feature.

---

## Section 13 — Observability & SRE **[metrics + tracing + profiling implemented; dashboards via compose]**

- **SLIs/SLOs:** message send→ack p99 < 250 ms; send→delivery (online) p99 < 500 ms;
  gateway availability 99.95%; delivery success 99.99% (excluding offline).
- **Metrics:** Prometheus at `/metrics` (`internal/metrics`) — **histograms** for
  send→ack latency, fanout lag, write-batch and outbox-batch size, plus counters
  (connections, frames in/out, messages by op, errors by code). **[implemented]**
- **Tracing:** OpenTelemetry (`internal/tracing`); W3C trace context **propagated
  through the event bus** (injected into event headers, extracted in fanout), so a
  trace follows send→outbox→fanout. Exporter: stdout (dev) or **OTLP/HTTP**
  (`SYNAPSE_OTLP_ENDPOINT`), else no-op. **[implemented]**
- **Profiling:** `/debug/pprof/` gated by `SYNAPSE_PPROF=1`. **[implemented]**
- **Logging:** structured `slog`; a dedicated **audit** channel (`internal/audit`)
  for security events (login, chat export). **[implemented]**
- **Dashboards:** `deploy/observability` compose brings up Prometheus + Tempo +
  Grafana (provisioned datasources). **[implemented]**
- **Chaos/testing:** kill gateway pods (verify reconnect+resume), inject bus
  latency, packet loss. **Deploys:** canary + blue-green for the gateway (drain
  connections gracefully). **Runbooks:** "gateway overload", "bus lag", "DB
  failover", "reconnect storm".

---

## Section 14 — Security hardening **[core implemented; see SECURITY.md for the audit]**

**Implemented:** per-connection token-bucket flood control (`pkg/ratelimit`,
`ErrFlood`); a **per-username login limiter** (brute-force throttle across all
connections); handshake/idle read deadlines (slow-loris defense); argon2id
password hashing with timing-equalized login and a **hash-concurrency semaphore**
(`SYNAPSE_AUTH_HASH_CONCURRENCY`) so an auth flood can't OOM the node with
parallel 64 MiB hashes; **session/resume tokens hashed at rest (SHA-256)** so a DB
leak yields no usable credentials; explicit register-vs-login (no silent account
creation/enumeration); 16 MiB frame cap + zip-bomb-guarded decompression + a
**fuzzed parser** (2M+ execs, zero crashes); **RBAC** roles (admin/moderator) and
an **append-only audit log** (`internal/audit`) for login/export; moderation
(banned-term + spam-velocity); media URLs HMAC-signed with expiry and
constant-time verification (plus `nosniff`/attachment on serve), an **AV scan**
hook (EICAR) on upload; a **per-IP accept guard** (rate + concurrency caps,
`SYNAPSE_MAX_CONNS_PER_IP`/`_ACCEPT_RATE_PER_IP`) rejecting floods before
handshake; a **mandatory-TLS policy** switch (`SYNAPSE_REQUIRE_TLS`); **mTLS**
helper (`pkg/mtls`); **circuit breaker** guarding external deps; **E2E safety
numbers** (`e2e.SafetyNumber`) for directory-MITM detection; E2E ciphertext the
server cannot read. A **CI pipeline** CVE-scans deps (govulncheck) and runs SAST
(gosec) + the race detector + fuzzing on every push.

**Designed:** hard brute-force lockout, device fingerprint / IP reputation
signals, upstream L4 DDoS scrubber, secret management (Vault/KMS), TOFU
identity-key pinning. Full analysis and threat model in [SECURITY.md](SECURITY.md).

---

## Section 15 — Deployment topology (Kubernetes) **[designed; compose provided]**

```
Internet ─▶ L4 LB / Ingress (TLS 1.3) ─▶ Gateway pool (HPA, PodDisruptionBudget,
                                          graceful conn drain)
   Gateway ─▶ stateless svc Deployments (auth, chat, message, fanout, presence)
   Async:   NATS (JetStream) StatefulSet cluster
   State:   Postgres (primary+standby, operator) · Redis (Sentinel/cluster)
   Blobs:   S3-compatible + CDN
   Obs:     Prometheus · Tempo · Loki · Grafana
   CI/CD:   build → test → fuzz → image → canary → blue-green
   Secrets: Vault/KMS + sealed-secrets
   Envs:    staging / preprod / prod, isolated data planes
```

`docker-compose.yml` provides the local equivalent (Postgres/Redis/NATS).

---

## Section 16 — Go implementation guidance **[implemented patterns]**

- **Repo:** **monorepo** (one module, shared `pkg/`), split into deployables
  later — done here.
- **Contracts:** protobuf/gRPC for service APIs (interfaces stand in for the MVP);
  the wire protocol is versioned separately.
- **Package boundaries:** `pkg/` reusable + dependency-free-ish; `internal/`
  domain; services depend on `store` interfaces, not concrete DBs.
- **Concurrency:** **two goroutines per connection** (reader + writer); liveness
  is driven by **one shared per-node reaper** (map walk + ping) instead of a timer
  goroutine per connection, so goroutine count stays ~2/conn at millions of
  connections. Atomics for seq/liveness; bounded QoS channels for backpressure;
  `sync.Once` for close.
- **Context propagation:** `context.Context` threaded through services and stores;
  **tracing propagates through the bus** (W3C context in event headers).
- **Backpressure / worker pools:** bounded per-QoS outbound queues; fanout via bus
  queue groups (competing consumers); ephemeral lanes drop before durable ones.
- **Group-commit writes:** concurrent inserts coalesce into one pipelined
  transaction + one fsync (`internal/store/postgres/batch.go`) — 230→3760 msg/s
  single node, durability intact. **[implemented]**
- **Event-driven / outbox:** **transactional outbox implemented** — the event is
  written to the `outbox` table in the same tx as the message and a relay
  (`internal/outbox`, `FOR UPDATE SKIP LOCKED` + `LISTEN/NOTIFY`) publishes it to
  **JetStream durable consumers**, so an event is never lost on a crash between
  commit and publish, and a down worker resumes on restart. **[implemented]**
- **Migrations:** **golang-migrate** versioned SQL (`…/migrations/`, `schema_migrations`
  tracked), applied on boot via embedded `iofs`; a migration job in prod. **[implemented]**
- **Testing pyramid:** unit (`pkg/wire`), integration (`internal/gateway` — real
  clients through the gateway, incl. cross-node + QUIC), **fuzz** the parser
  (`FuzzParser`), and a **load-test harness** (`cmd/loadtest`) with a throughput
  mode and an **idle-connection scale mode** (per-conn goroutines + GC-forced
  retained heap: ~2 goroutines / ~24 KiB per idle connection). A **CI pipeline**
  (`.github/workflows/ci.yml`) runs the race-detector suite, **govulncheck** (CVE
  scan), **gosec** (SAST), and a parser fuzz on every push.
  **[unit+integration+fuzz+load+CI implemented]**

---

## Section 17 — First 6 months roadmap

**Build order (proven by this repo through step 4):**
1. Binary protocol + gateway skeleton (TCP/WS, handshake). ✅
2. Auth/sessions + Postgres. ✅
3. Message write path (durable, ordered, idempotent) + event bus. ✅
4. Fanout/delivery + presence + read receipts + CLI client. ✅
5. **Load-prove early:** send→ack throughput/latency harness in place
   (`cmd/loadtest`); group commit lifted a single node 230→3760 msg/s (p50 45ms).
   100k-connection scale is the remaining rung. ✅ (throughput) / ▶ (conn scale)
6. Media pipeline + push notifications (V1). ✅
7. Search + moderation + rate limiting (V1). ✅
8. Multi-node gateway routing + resume buffer + observability stack. ✅
9. Product surface on top of the finished write path: reactions, threads,
   attachments, polls, call signaling, contacts/blocking, forwarding,
   self-destruct, scheduling, pins, drafts, handles, invite links, roles. ✅
   Deliberately last — each is cheap once ordering, authorization and fanout are
   settled, and ruinous to retrofit if they are not.

**Can be stubbed:** notifications, search, media, moderation, admin (interfaces
exist; workers are stubs).
**Must be proven by load test early:** connection scale per gateway pod, fanout
throughput on a hot channel, DB write/seq-bump contention, reconnect-storm
recovery.
**Staffing:** 1 protocol/gateway eng, 1–2 backend (services/storage), 1
SRE/infra, 1 mobile/client, 0.5 security. **Biggest risks:** hot-partition
contention on popular chats/channels; fanout amplification; reconnect storms;
premature E2E/multi-region complexity. Mitigate by load-testing #5 before #6.

---

## Section 18 — Required artifacts (index)

1. **Full architecture explanation** — Sections 2, 6, 9.
2. **ASCII component diagram** — Section 2.
3. **Service catalog table** — Section 6.
4. **Message-flow sequence** — Sections 3 & 9.
5. **Storage decision matrix** — Section 8.
6. **Risk register** — below.
7. **MVP cut list** — below.
8. **Concrete recommendations** — stated inline per section (no "consider
   options"): opaque tokens, argon2id, Snowflake ids, NATS over Kafka for
   realtime, Postgres-first then Scylla for messages, at-least-once + idempotency,
   stateless gateway, cloud-chats default with opt-in Signal-style secret chats.

### Risk register

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Hot chat/channel seq contention | Med | High | Per-chat sequencer; shard hot channels; **group-commit seq batching (implemented)** |
| Postgres commit-fsync write ceiling | High | High | **Group-commit batcher (implemented): 230→3760 msg/s, durability kept** |
| Fanout amplification (channels) | High | High | Async fanout, queue groups, push offload; **hot-chat fanout sharding (implemented): members chunked into parallel shard jobs** |
| Reconnect storm after gateway restart | Med | High | Resume + replay buffer + jittered backoff + accept rate-limit + graceful drain; the retried sends it produces are duplicates, so a failed write batch **bisects** instead of falling back to one transaction per message (which would collapse write throughput exactly at the peak) |
| Multi-node delivery / split-brain | Med | High | Shared router (Redis) + bus node-targeting + SKIP-LOCKED outbox (no double-publish) |
| Slow consumers stalling delivery | Med | Med | Bounded queues, drop-and-resync (implemented) |
| Postgres write saturation | Med | High | **chat_id-sharded message store (implemented, `internal/store/sharded`)** → Scylla; **read replica for history/receipts (implemented, `SYNAPSE_PG_REPLICA_DSN`)** |
| Duplicate delivery confusing clients | High | Low | Client dedup by message_id (at-least-once by design) |
| **Topology drift** — behaviour depends on the deployment: a field the monolith delivers is dropped by the gRPC contract, or an optional store capability (threads, the self-destruct reaper, media reference checks) is not forwarded by a decorator and the FEATURE disappears for whoever enabled sharding | Med | High | Domain fields mirrored in `services.proto`; capabilities forwarded by `internal/store/sharded` with compile-time assertions; both paths tested against the real thing — a gRPC hop and real Postgres shards |
| Premature E2E/multi-region | Med | High | Explicitly deferred to V2 |
| Protocol parser exploited by malformed input | Low | High | Magic + length cap + zip-bomb guard + `FuzzParser` |

### MVP cut list (ship without)

Media, push notifications, search, moderation/abuse ML, secret (E2E) chats,
multi-region, wide-column message store, QR/bot login, transactional outbox,
admin console. All have interfaces/hooks so they slot in without rework.
