# Synapse — Protocol & System Security Audit

This document is a hands-on security review of the Synapse protocol and gateway:
the threat model, what is defended today (with the file that does it), what is
deliberately deferred, and concrete recommendations before production. It is
written to be read alongside the code.

Legend: ✅ implemented · 🟡 partial · ⬜ designed / TODO before prod.

---

## 0. Recent hardening (this pass)

Several phased correctness/scale/ops/security passes landed the following.
Sections below reflect the new state:

- **Single-writer Seq** — all outbound frames go through one writer, so Seq matches
  wire order (no spurious gap detection).
- **Transactional outbox → JetStream durable consumers** — events are committed
  with the message (`FOR UPDATE SKIP LOCKED` + `LISTEN/NOTIFY`) and relayed to
  durable consumers, so no event is lost on crash and a down worker resumes.
- **Group-commit writes** — concurrent inserts coalesce into one fsync without
  dropping durability (230→3760 msg/s), so a write burst can't be used to force a
  durability/throughput tradeoff.
- **TLS 1.3** — optional on TCP + WS (`SYNAPSE_TLS_CERT/KEY` or self-signed dev),
  plus **QUIC** (requires TLS); warns loudly when off. **mTLS helper** (`pkg/mtls`)
  for service-to-service auth.
- **E2E signed prekeys** — Ed25519 signatures on signed prekeys, verified in X3DH
  (MITM-by-directory defense); **multi-device sync** via `KEY_FETCH_ALL`.
- **RBAC** (admin/moderator) + **append-only audit log** (`internal/audit`) for
  login/export; **media AV scan** (EICAR hook); **auth hash-concurrency semaphore**
  (argon2 OOM-flood guard); **circuit breaker** + local fallback on Redis outage.
- **Observability** — Prometheus **histograms**, `pprof`, **OpenTelemetry OTLP**
  tracing propagated through the bus.
- **Brute-force + new-chat throttles**, **origin allow-list**, **write deadlines**,
  **graceful drain**, **shared per-node reaper**, **boundary id validation**,
  **capability media refs**, **zstd+dictionary compression**, **QoS lanes**.
- **CI security gates** (`.github/workflows/ci.yml`): race-tested suite,
  **govulncheck** (CVE scan of deps + stdlib) and **gosec** (SAST) on every push,
  plus a parser fuzz. The first run cleared **7 reachable CVEs** by dependency
  upgrade — notably a pgx SQL-injection (GO-2026-5004), an x/text infinite-loop
  DoS, and a quic-go panic reachable from the QUIC listener.
- **Per-IP accept guard**: per-source-IP accept-rate + concurrent-connection caps
  reject floods/reconnect storms *before* handshake (`SYNAPSE_MAX_CONNS_PER_IP` /
  `SYNAPSE_ACCEPT_RATE_PER_IP`), counted by `synapse_connections_rejected_total`.
- **Mandatory-TLS policy** (`SYNAPSE_REQUIRE_TLS=1`): the server refuses to boot in
  plaintext, so a misconfiguration cannot silently expose cleartext.
- **E2E safety numbers** (`e2e.SafetyNumber`): a symmetric 60-digit fingerprint of
  both parties' identity keys for out-of-band MITM detection at the key directory.
- Media HTTP responses now carry `nosniff` + attachment disposition; FS media
  store tightened to 0700/0600; WS/HTTP server given a `ReadHeaderTimeout`.
- **Amplification throttles**: typing indicators are the cheapest frame to send
  and the most expensive to serve (one per chat member, on every node), so they
  now pass a per-connection and a per-chat bucket; call signaling, which bypasses
  the send bucket by design, carries its own ceiling.
- **Device ids are bound to their owner**: the id is asserted by the client in
  HELLO, so an upsert scoped to the owner stops a second account from rewriting
  someone else's device row — in particular clearing its push token. A squatter
  is silently handed a fresh id rather than an error, so the id space is not an
  existence oracle.
- **Upload tickets are single-use and size-bound** (see §7).
- **Bounded caches**: the chat authorization view and the fanout member list are
  swept and capped, so neither grows with the number of chats a node has ever
  touched.
- **Per-user budgets for expensive actions** (export, search, media tickets,
  invite links): the per-connection bucket limits a socket, which a client
  sidesteps by opening another one. The Redis-backed limiter is one Lua script
  (a bucket that can be read twice before either write lands is not a limit) and
  fails OPEN — a limiter that turns a Redis blip into an outage of search, media
  and export has done more damage than the abuse it guards against.
- **Push tokens are scoped twice**: a client may register a token only for the
  account it authenticated as AND the device it is connected as, and a token the
  provider reports as dead (APNs 410 / FCM 404) is dropped rather than retried
  forever.
- **A connection is routable before it is told it is connected.** Registration in
  the delivery hub and the routing registry now precedes the AUTH_OK reply. The
  window between them used to swallow anything addressed to a client that had
  just authenticated — invisible for a chat message, which history backfills, and
  permanent for the E2E relay, which is fire-and-forget by design (see §6).
- **Push-token registration is metered** like every other state-changing frame:
  it writes to the database, and an unmetered write per frame is a flood surface
  however small each one is.
- **TOFU identity pinning** (`pkg/e2e/trust.go`): safety numbers let two people
  DETECT a swapped identity key if they compare one; pinning removes the
  dependence on anyone remembering to. It cannot protect a first contact and does
  not decide on its own — a reinstall and an attack look identical, so it reports
  and the human chooses.

---

## 1. Threat model

Who we defend against, and where:

| Adversary | Capability | Primary defenses |
|-----------|-----------|------------------|
| Network attacker (MITM) | read/modify bytes on the wire | TLS 1.3 at edge ✅ (TCP/WS/QUIC, optional by config) + AEAD for E2E ✅ |
| Malicious/buggy client | send arbitrary frames | fuzzed parser ✅, size caps ✅, auth gate ✅, rate limits ✅ |
| Unauthenticated attacker | hold/exhaust resources | handshake/idle deadlines ✅, argon2 hash-concurrency cap ✅, accept limits 🟡 |
| Curious/compromised server | read message content | E2E secret chats ✅ (cloud chats are readable by design) |
| Database thief | steal the DB at rest | argon2id passwords ✅, hashed tokens ✅ |
| Abusive user | spam/flood/harass | flood control ✅, moderation ✅, RBAC + audit log ✅ |
| Replay attacker | resend captured frames | per-conn seq ✅, idempotency keys ✅, AEAD nonces ✅ |
| Login brute-forcer | guess passwords | per-username login throttle ✅ |

---

## 2. Transport & framing

**Frame parser hardening** — `pkg/wire/frame.go`
- ✅ Magic bytes (`SC`) reject non-protocol traffic and port scans immediately.
- ✅ Explicit `uint32` length prefix is **capped at 16 MiB** (`MaxPayloadSize`)
  *before* allocation, so a hostile length cannot trigger a giant `make`.
- ✅ Compression is length-bounded on decompress (`gzipDecompress` uses a
  `LimitReader` at `MaxPayloadSize+1`) — **zip-bomb resistant**.
- ✅ The parser is **continuously fuzzed** (`FuzzParser`, `wire_test.go`): 2M+
  executions with **zero panics**. Malformed input always returns an error.
- ✅ Envelope decoding validates every varint and bounds body length against the
  remaining buffer (`envelope.go`), so truncated/lying headers error cleanly.

**Slow-loris / resource exhaustion** — `internal/gateway/conn.go`
- ✅ Unauthenticated peers get a `HandshakeTimeout` (10 s) read deadline on both
  the HELLO and AUTH reads. A peer that connects and stalls is dropped.
- ✅ Authenticated connections get an `IdleTimeout` (60 s) read deadline refreshed
  each frame; missed heartbeats reclaim the connection.
- ✅ Backpressure: each connection has a bounded outbound queue; a client that
  cannot keep up is dropped (it resyncs via history) instead of stalling fanout.

**TODO before prod**
- ✅ **TLS 1.3** at the edge for raw TCP, WSS, and QUIC (the custom protocol rides
  *inside* TLS — we never invent transport crypto). Optional by config; MVP can run
  plaintext locally. Make it mandatory-by-policy in prod.
- ✅ Per-IP **accept rate limiting** and concurrent-connection caps at the accept
  edge (`ipGuard`, `SYNAPSE_MAX_CONNS_PER_IP` / `SYNAPSE_ACCEPT_RATE_PER_IP`), on
  top of multi-accept + `SO_REUSEPORT`. An upstream L4/edge quota is still worth
  adding in front for volumetric floods that never reach the app.
- ✅ WebSocket `CheckOrigin` enforces an **origin allow-list** (`SYNAPSE_ALLOWED_ORIGINS`);
  empty = allow-any is dev-only.

---

## 3. Authentication & sessions — `internal/auth/auth.go`

- ✅ **Passwords**: argon2id (memory-hard, 64 MiB, t=1, p=4), random 16-byte
  salt, **constant-time** comparison (`crypto/subtle`).
- ✅ **User-enumeration resistance**: unknown-user login still runs a dummy
  argon2id verify so response timing does not reveal account existence.
- ✅ **Tokens at rest**: session and resume tokens are 256-bit random values;
  **only their SHA-256 is stored** (`hashToken`). A database leak exposes no
  usable bearer token. SHA-256 (not argon2) is correct here because the input is
  already full-entropy, so it is not brute-forceable.
- ✅ **Explicit register vs login** (`AuthBody.Register`): the server never
  silently creates an account on a failed login — closing an
  enumeration/account-squatting vector present in the earlier MVP.
- ✅ **Brute-force throttle**: password logins are rate-limited **per username
  across all connections** (`gateway.loginLimiter`, token bucket ~1/s, burst 5),
  so an attacker cannot bypass it by opening a fresh connection per guess. A
  bucket (not a hard lockout) means a legitimate user is never permanently locked
  out by someone spamming their username.
- ✅ **Revocation**: opaque tokens give O(1) server-side revocation
  (`revoked_at`), unlike stateless JWTs. Expiry is enforced on every use.

**TODO before prod**
- ⬜ Additionally throttle by IP (not only username).
- ⬜ **Risk signals**: device fingerprint, IP reputation, velocity → step-up auth.
- ⬜ Bind tokens to a device/TLS channel to limit token replay if one leaks.

---

## 4. Authorization — `internal/chat`, `internal/message`

- ✅ Every send is authorized by `chat.CanPost` (channel = admins/owner only;
  group/direct = any member); every read/history/search is filtered by
  membership (`IsMember`). Search results are permission-filtered per hit.
- ✅ Edit/delete check ownership (sender) or chat admin rights.
- ✅ **Blocking cuts traffic in BOTH directions** (`internal/contact`): if either
  side blocked the other, the direct chat does not resolve — so a blocked sender
  cannot message, and cannot read the replies by reopening the chat either. A
  one-directional block would be a false promise.
- ✅ **Membership rights** (`internal/invite`): role changes are **owner-only** (an
  admin who could demote the owner could seize the chat), and demoting the last
  owner is refused, so a chat can never become unadministrable. A join or demotion
  invalidates the cached authorization view immediately rather than after its TTL.
- ✅ **Invite links are credentials, and treated as such**: 128 bits of entropy,
  revocable, boundable by use count and expiry, and redeemed **atomically** (the
  validity checks live in the `UPDATE`'s `WHERE`), so a link capped at N uses
  cannot be over-redeemed by concurrent joins. Re-opening a link you already used
  is idempotent and spends no use.
- ✅ **Public handles are deliberately weaker than links**: a handle grants
  discoverability, not membership. Uniqueness is **case-insensitive** (unique index
  on `LOWER(username)`) and the alphabet is bounded — otherwise `News` could shadow
  `news` and handles would become a phishing surface.
- ✅ **Pins are an admin action** in groups/channels (a pin is visible to everyone)
  and open to either party in a 1:1, where both are equals. **Drafts are private**:
  keyed by `(user_id, chat_id)` and routed per-user, never to the chat.
- ✅ Chat-scoped requests that carry an id instead of resolving one (handles,
  links, roles) validate it before it reaches the store, so a malformed id is a
  `BAD_ARG`, not an internal error leaking a database complaint.
- ✅ **Media download**: `media_ref` is now a capability token (snowflake + 128
  bits of crypto-random), so it cannot be guessed or enumerated; combined with the
  signed URL this makes access sound.
- 🟡 Production should **additionally** verify the fetcher is a member of a chat
  where the media was posted (defense in depth against a leaked ref).

---

## 5. Message integrity, ordering, replay — `internal/store`, `pkg/wire`

- ✅ **Idempotent writes**: unique `(sender_id, dedup_key)` index; a retried send
  resolves to the stored message and **consumes no sequence** (seq bump + insert
  share one transaction), so ordering stays gap-free.
- ✅ **Ordering**: strictly increasing per-chat `seq` via `UNIQUE(chat_id, seq)`.
- ✅ **Transport replay**: monotonic per-connection `Seq` lets the server detect
  duplicates/replays on a live connection; resume tokens bound cross-connection
  replay.
- ✅ **Flood control**: per-connection token bucket on state-changing messages
  (`stateChanging` + `pkg/ratelimit`), returning `ErrFlood` with a `retry_after`.

---

## 6. End-to-end encryption (secret chats) — `pkg/e2e`

Uses **only standard, audited primitives** — no home-grown crypto:

| Purpose | Primitive |
|---------|-----------|
| Key agreement (DH) | **X25519** (`crypto/ecdh`) |
| Initial handshake | **X3DH** (identity + signed prekey + one-time prekey) |
| Per-message keys | **Double Ratchet** (DH ratchet + symmetric-key ratchet) |
| Key derivation | **HKDF-SHA256** |
| Chain-key step | **HMAC-SHA256** |
| Message encryption | **ChaCha20-Poly1305** (AEAD) |

- ✅ **Forward secrecy**: every message uses a fresh key derived from the chain;
  the test asserts identical plaintext yields different ciphertext.
- ✅ **Post-compromise security**: the DH ratchet heals the session after a key
  compromise once both sides send again.
- ✅ **Authentication / tamper detection**: AEAD with the ratchet header as
  associated data; the test flips a bit and asserts decryption fails.
- ✅ **Out-of-order & missing messages**: skipped-message keys are stored (bounded
  by `maxSkip = 1000` to stop a malicious header forcing unbounded work).
- ✅ **Nonce safety**: each message key is single-use, and the AEAD key+nonce are
  derived from it via HKDF, so the zero nonce is safe (never reused under a key).
- ✅ **Server is blind**: it stores only public prekeys (`internal/keydir`) and
  relays opaque ciphertext (`MsgSecretSend/Recv`); it can neither derive a shared
  secret nor read a message.

- 🟡 **The relay does not store and forward.** Ciphertext addressed to a device
  that is not reachable at that instant is dropped, not queued — the server holds
  no copy, which is also why it cannot replay one. That is a deliberate posture
  (nothing to seize), and it makes REACHABILITY part of the security story: the
  connection is registered before AUTH_OK precisely so a device that has just
  authenticated cannot be addressed into a gap. A production deployment that wants
  offline secret messages needs an explicit encrypted queue with its own retention
  rules; silently adding one would change what the server is holding.

**TODO before prod (well-known items, not crypto flaws)**
- ✅ **Signed prekey signature**: `SignedPreKey` is Ed25519-signed by the identity
  key and **verified in X3DH** (`pkg/e2e/sign.go`), so a malicious directory can't
  substitute keys.
- ✅ **Multi-device**: a sender fetches every device's prekeys via `KEY_FETCH_ALL`
  (peer's devices + its own other devices), establishes a session with each, and
  the server routes per-device ciphertext — Signal-style sender-side fanout, server
  stays blind.
- ✅ **Identity verification (safety numbers)**: `e2e.SafetyNumber` produces a
  symmetric 60-digit fingerprint of both identities (X25519 + Ed25519 keys) via
  Signal-style iterated hashing; comparing it out of band detects a directory
  MITM. TOFU pinning of identity keys is the remaining client-side follow-up.
- ⬜ Persist ratchet state securely on device (OS keystore).

---

## 7. Media — `internal/media`

- ✅ Bytes never traverse the binary protocol (only a short `media_ref` does),
  shrinking the protocol attack surface.
- ✅ Upload/download URLs are **HMAC-signed with an expiry** and verified in
  **constant time** (`hmac.Equal`); a forged signature is rejected (tested, 403).
- ✅ `media_ref` is an unguessable capability token (snowflake + 128 bits random),
  resistant to enumeration.
- ✅ Object refs are server-generated and `filepath.Base`-sanitized (no path
  traversal into the store).
- ✅ Upload size is bounded (`maxSize`, 100 MiB) with a `LimitReader`, and the
  size DECLARED at `InitUpload` is part of the signed material and enforced on the
  body — otherwise a ticket for a one-kilobyte avatar would accept a hundred
  megabytes and any quota decided at ticket time would be decoration.
- ✅ Uploads are **create-only** (`O_EXCL`): a signed PUT stays valid for its
  whole TTL, so without this the holder could keep replacing the bytes behind a
  `media_ref` recipients already hold — swapping content under a message after
  the fact. The write is atomic, so concurrent PUTs cannot both land.

**TODO before prod**
- ✅ **Virus/malware scanning** on upload via a `Scanner` hook (`internal/media`,
  default catches EICAR; wire ClamAV/ICAP in prod). Content-type validation is the
  same seam.
- ⬜ Per-user upload quotas; known-bad hash matching.

---

## 8. Secrets & operations

- ✅ No plaintext passwords or bearer tokens at rest.
- 🟡 Media signing secret and DB DSNs come from env with insecure dev defaults —
  ⬜ move to **Vault/KMS** in prod; the default media secret must never ship.
- ✅ **RBAC** (admin/moderator roles gate privileged ops like chat export) and an
  **append-only audit log** (`internal/audit`) record login + export events; an
  **mTLS helper** (`pkg/mtls`) is ready for service-to-service auth when the
  monolith splits.
- ⬜ Encrypted backups; wire mTLS into the actual service mesh once split out.

---

## 9. Summary scorecard

| Area | State |
|------|-------|
| Parser robustness (fuzzed, capped, bomb-safe) | ✅ strong |
| Auth (argon2id, hashed tokens, enum-resistant, login throttle) | ✅ strong |
| Authorization (per-op membership checks) | ✅ good |
| Membership & sharing (owner-only roles, last-owner guard, bounded revocable links, two-way blocking) | ✅ good |
| Idempotency / ordering / replay | ✅ strong |
| Flood / abuse control (+ per-IP accept guard) | ✅ strong |
| Supply chain (CI: govulncheck CVE scan + gosec SAST + race + fuzz) | ✅ good |
| E2E crypto (standard primitives, tested, prekey signatures, multi-device, safety numbers) | ✅ strong |
| Media (signed URLs, unguessable refs, size caps, AV scan, nosniff) | ✅ strong |
| Transport encryption (TLS 1.3 on TCP/WS/QUIC) | ✅ optional, or enforced via `SYNAPSE_REQUIRE_TLS` |
| Observability (histograms, pprof, OTLP tracing) | ✅ good |
| RBAC + audit log | ✅ good |
| Data retention (outbox, scheduled, replay, media collected) | ✅ good |
| Secrets management (Vault/KMS), mTLS in the mesh | ⬜ before prod |

**Bottom line:** the *protocol and application-layer* security is at a strong,
production-shaped level — hardened parser, sound auth (with an argon2-flood guard),
real E2E with standard primitives incl. prekey-signature verification, multi-device
fanout and safety numbers, idempotency, abuse controls with a per-IP accept guard,
RBAC + audit, AV scanning, and a CI pipeline that CVE-scans dependencies and runs
SAST + the race detector + fuzzing. The remaining gaps are **deployment-layer** (a
secrets manager, wiring mTLS into the real service mesh, an upstream L4 flood
scrubber) — all called out here so none are silent. The client-side E2E follow-up (TOFU
identity-key pinning) is now built: `pkg/e2e/trust.go`.
