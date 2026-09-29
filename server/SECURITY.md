# SyncApp — Protocol & System Security Audit

This document is a hands-on security review of the SyncApp protocol and gateway:
the threat model, what is defended today (with the file that does it), what is
deliberately deferred, and concrete recommendations before production. It is
written to be read alongside the code.

Legend: ✅ implemented · 🟡 partial · ⬜ designed / TODO before prod · ❌ **known
defect**.

---

## ⚠️ Open defects (last verified 2026-09-29)

This section is the first thing in the document on purpose. Everything below it
describes defenses that are built; this is where the ones that are **not** go, so
a reader meets them before the feature list rather than after it.

Every line here is checked against the code on the date in the heading, and each
closed item names the test that holds it closed, not a commit. A stale security
document is worse than none: it spends a reader's attention re-fixing what is fixed
and lends its credibility to whatever is still broken.

**Still open:**

| # | Defect | Where | Impact |
|---|--------|-------|--------|
| — | None open. | | |

**Closed.** One line on what was wrong; the fix is in the code the test covers,
and the commit that closed it says how.

| # | What was wrong | Held by |
|---|----------------|---------|
| ✅ P0-1 | The signed-prekey signature was checked only when present, so a hostile directory skipped it by omitting it. | `pkg/e2e` and every port: an unsigned bundle is refused. |
| ✅ P0-2 | `Session.Decrypt` committed ratchet state before authenticating, so one forged frame destroyed a live session; skipped keys were unbounded. | `pkg/e2e` "a forged frame" tests, mirrored in every port; the forged step in `server/testdata/e2e/vectors.json`. |
| ✅ P0-3 | `KEY_PUBLISH` did not bound prekey length, in a directory with no expiry. | `internal/keydir` tests. |
| ✅ P0-4 | `KEY_FETCH`/`KEY_FETCH_ALL` ignored blocks, so a blocked user could enumerate a target's devices. | `internal/gateway` E2E integration tests. |
| ✅ P0-5 | A zstd frame was decompressed before its size was checked, with the compression flag chosen by the sender even before authentication. | `pkg/wire/limits_test.go`; `internal/gateway/wirepolicy_integration_test.go`. |
| ✅ P0-6 | `authd` ran without the second factor: in the split deployment a TOTP account signed in with the password alone. | `internal/wiring` `TestNewAuthEnforcesTheSecondFactor`. |
| ✅ P1-1 | Session revocation and account deletion were unreachable from the protocol, so "log out" left the session valid. | `internal/gateway/session_integration_test.go`. |
| ✅ P1-2 | `HISTORY` and the other amplifying reads were outside flood control. | `internal/gateway/readbudget_test.go`. |
| ✅ P1-3 | `SYNCAPP_REQUIRE_TLS=1` still allowed the dev media secret, an empty origin list and no per-IP caps. | `internal/platform/prodcheck_test.go`. |
| ✅ P1-4 | The per-IP guard ignored forwarded headers, so behind a balancer it never fired or refused everyone. | `internal/gateway/clientip_test.go`. |
| ✅ P1-5 | Frame size was checked only after the frame was in memory; WebSocket had no read limit; 16 MiB frames were accepted before authentication. | `TestWebSocketReadLimit`, `TestInboundPolicyRefusesOversizeBeforeReadingIt`, `TestOversizeFrameBeforeAuthIsRefused`. |
| ✅ P1-6 | A block held only for `@username` targets, not chat ids; a scheduled send ignored a later block. | `internal/gateway/block_integration_test.go`; `internal/schedule` `TestBlockRecheckedAtFireTime`. |
| ✅ P1-7 | YooKassa notifications were checked against an HMAC YooKassa does not send; refunds went to the wrong endpoint. | `internal/billing/provider_yookassa_test.go`, `source_test.go`. |
| ✅ P1-8 | The membership gate on media downloads was wired in neither binary. | `internal/wiring` `TestEdgeServicesAreComplete`. |
| ✅ P1-9 | The split deployment had drifted from the monolith: no secret queue, pins, schedule, invites, billing or trusted proxies at the edge, no scheduler anywhere, and `fanoutd` without presence privacy or mute. | `internal/wiring` `TestEdgeServicesAreComplete`, `TestFleetServesEveryEdgeFeature`, `TestFromEnvRefusesMalformedValues`. |
| ✅ P1-10 | Raw TCP behind a balancer charged every client to the balancer's address. | `internal/gateway` `TestPerIPCapBehindProxyChargesTheClient`, `TestProxyListenerHonoursOnlyTrustedPeers`, `TestProxyHeaderIsSeenThroughTLS`. |
| ✅ P2-1 | iOS showed no safety number and never checked a pin. | `SafetyViewModelTests`, `SafetyMappingTests`; `CryptoTests/VectorsTests.swift`. |
| ✅ P2-2 | Deleting a message kept its attachment metadata and its blob. | `storetest` `DeleteClearsAttachment` (memory and Postgres). |
| ✅ P2-3 | Channel read receipts went to the whole audience; edits and deletions were pushed. | `internal/fanout` `TestReadReceiptsStayPrivateInChannels`, `TestChatKindIsLookedUpOncePerChat`, `TestEditAndDeleteAreDeliveredButNotPushed`; `internal/rpc` `TestChatTypeOverRPC`. |

**Where the clients stand.** `constants.go` declares 116 `MsgType` values, of
which 115 travel (`MsgReserved = 0` is "no type at all"). Each client is held to
that set mechanically rather than by review, because a hand-kept list drifts and
the protocol's own rule is that an unknown type is skipped in silence:

| | Types | Parity test |
|---|---|---|
| Web | 115 / 115 | `client/src/shared/api/protocol/msg-type.parity.test.ts` |
| Android | 115 / 115 | `android/.../protocol/MsgTypeParityTest.kt` |
| iOS | 115 / 115 | `ios/SyncAppKit/Tests/NetworkTests/MsgTypeParityTests.swift` |

Each reads `server/pkg/wire/constants.go` and `types.go` and compares
(number → name) pairs, both halves taken from the server. iOS was missing seven
types until the test existed — account deletion, all four session messages and
the batched history page — which is exactly the drift an unchecked list produces
and exactly what nothing else was going to report.

**End-to-end encryption now ships on all three clients.** The ratchet, X3DH,
safety numbers and trust pinning are ported to `client/src/shared/lib/e2e`,
`android/.../crypto` and `ios/SyncAppKit/Sources/Crypto`, each pinned against
`pkg/e2e` with the shared vectors in `server/testdata/e2e`. All three show a
safety number and refuse to send to a changed identity until a person accepts it.

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
- **TLS 1.3** — optional on TCP + WS (`SYNCAPP_TLS_CERT/KEY` or self-signed dev),
  plus **QUIC** (requires TLS); warns loudly when off. **mTLS helper** (`pkg/mtls`)
  for service-to-service auth.
- **E2E signed prekeys** — Ed25519 signatures on signed prekeys, verified in X3DH
  (MITM-by-directory defense); **multi-device sync** via `KEY_FETCH_ALL`.
  ✅ *Verification is unconditional: a bundle with no signature is rejected, in
  every implementation and at publish time. A check conditional on the bundle
  carrying a signature would make the defense opt-in for the attacker.*
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
  reject floods/reconnect storms *before* handshake (`SYNCAPP_MAX_CONNS_PER_IP` /
  `SYNCAPP_ACCEPT_RATE_PER_IP`), counted by `SYNCAPP_connections_rejected_total`.
- **Mandatory-TLS policy** (`SYNCAPP_REQUIRE_TLS=1`): the server refuses to boot in
  plaintext, so a misconfiguration cannot silently expose cleartext.
- **E2E safety numbers** (`e2e.SafetyNumber`): a symmetric 60-digit fingerprint of
  both parties' identity keys for out-of-band MITM detection at the key directory.
- Media HTTP responses carry `nosniff`, and everything is served as an inert
  attachment — except images, which are served inline under the type their own
  BYTES say they are (`http.DetectContentType`, whitelist: png/jpeg/gif/webp,
  never SVG). A file that merely claims to be a PNG stays an attachment, and an
  exact image type plus `nosniff` means a polyglot is never rendered as markup.
  Without this a picture could not be displayed at all, which is not a security
  property — just a broken avatar. FS media store tightened to 0700/0600;
  WS/HTTP server given a `ReadHeaderTimeout`.
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
  the delivery hub and the routing registry precedes the AUTH_OK reply. A window
  between them would swallow anything addressed to a client that had just
  authenticated — invisible for a chat message, which history backfills, and a
  loss for the E2E relay (see §6).
- **Push-token registration is metered** like every other state-changing frame:
  it writes to the database, and an unmetered write per frame is a flood surface
  however small each one is.
- **TOFU identity pinning** (`pkg/e2e/trust.go`): safety numbers let two people
  DETECT a swapped identity key if they compare one; pinning removes the
  dependence on anyone remembering to. It cannot protect a first contact and does
  not decide on its own — a reinstall and an attack look identical, so it reports
  and the human chooses.
  🟡 *Ported to all three clients (`client/src/entities/secret-chat/trust.ts`,
  `android/.../crypto/Trust.kt`, `ios/SyncAppKit/Sources/Crypto/Trust.swift`). Web
  Android and iOS each show a safety number and refuse to send to a changed identity
  until a human accepts it.*

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
- 🟡 Per-IP **accept rate limiting** and concurrent-connection caps at the accept
  edge (`ipGuard`, `SYNCAPP_MAX_CONNS_PER_IP` / `SYNCAPP_ACCEPT_RATE_PER_IP`), on
  top of multi-accept + `SO_REUSEPORT`. An upstream L4/edge quota is still worth
  adding in front for volumetric floods that never reach the app.
  Two caveats, both real: the guard is **off unless both variables are set**
  (`DefaultConfig` leaves them zero and `REQUIRE_TLS=1` does not demand them), and
  it keys on `RemoteAddr` with no `X-Forwarded-For`/PROXY-protocol support, so
  behind a load balancer it sees one address for the whole world (P1-3, P1-4).
- ✅ WebSocket `CheckOrigin` enforces an **origin allow-list** (`SYNCAPP_ALLOWED_ORIGINS`);
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
- ✅ **Handle lookup is metered**: `PROFILE_GET` resolves `@username` → user, so
  it is treated as an enumeration primitive and charged to the per-connection
  flood budget like a write. It also refuses a lookup when either side has
  blocked the other, so a block cannot be sidestepped by asking for the profile
  directly.

- ✅ **Revocation is reachable from every client.** `SESSION_LIST` → `SESSIONS`
  lists the live sessions of the caller's account (no tokens: the list exists so a
  person can recognise a device, not so one device can obtain another's
  credentials), and `SESSION_REVOKE` → `SESSION_REVOKED` ends one, or all the
  others, or all of them including this one. `ACCOUNT_DELETE` erases the account
  behind a re-confirmed password. This was the largest gap in this section for a
  long time, and it was a gap between a mechanism and a message rather than a
  missing mechanism: `auth.Revoke`, `ListSessions` and `DeleteAccount` were
  implemented and called from nowhere, so "log out" discarded the token locally
  while the session stayed valid until `expires_at` and a lost phone kept access.
- ✅ **Resume tokens rotate, and a replay ends the chain.** A resume consumes the
  token it presented and returns the next one; the consumed one is remembered, so
  a second use is detectable and is treated as theft — two parties hold the chain
  and there is no way to tell which is the owner, so the session ends. The owner
  re-authenticates with a password; whoever copied the token cannot.

**TODO before prod**
- ⬜ Additionally throttle by IP (not only username).
- ⬜ **Risk signals**: device fingerprint, IP reputation, velocity → step-up auth.
- ⬜ Bind tokens to a device/TLS channel to limit token replay if one leaks.

---

## 4. Authorization — `internal/chat`, `internal/message`

- ✅ Every send is authorized by `chat.CanPost` (channel = admins/owner only;
  group/direct = any member); every read/history/search is filtered by
  membership (`IsMember`). Search results are permission-filtered per hit.
- ✅ **Edit is sender-only. Delete is sender-only, or `chat.CanModerate`** —
  owner/admin in a group or channel, either party in a 1:1, where there is no
  hierarchy to appeal to. Moderation is its own predicate rather than a reuse of
  `CanPost`, which in a group is true for **every member**: "may this person add
  to the conversation" and "may this person remove what somebody else said" are
  different questions that agree in exactly one chat type, the channel. Tests
  cover the role matrix per chat type in both `internal/chat` and
  `internal/message`.
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
- ✅ The fetcher is **additionally** verified to be a member of a chat where the
  media was posted (`internal/gateway/media_authz.go`), so a ref that leaked out
  of a chat is no longer sufficient on its own. Avatars are the deliberate
  exception, checked first: a profile picture is fetchable by anyone who can see
  the profile, which `PROFILE_GET` already allows. A backend that cannot resolve
  a blob to its chats keeps the previous behaviour (unguessable ref + signature)
  rather than losing media wholesale.
- ✅ **Inline rendering is decided by content, not by the uploader.** Only
  png/jpeg/gif/webp — recognised from their own leading bytes — are served
  `inline`; everything else, SVG included, keeps `attachment`. `nosniff` is set
  either way, so an exact image type cannot be re-interpreted as markup.

---

## 5. Message integrity, ordering, replay — `internal/store`, `pkg/wire`

- ✅ **Idempotent writes**: unique `(sender_id, dedup_key)` index; a retried send
  resolves to the stored message and **consumes no sequence** (seq bump + insert
  share one transaction), so ordering stays gap-free.
- ✅ **Ordering**: strictly increasing per-chat `seq` via `UNIQUE(chat_id, seq)`.
- ✅ **Transport replay**: monotonic per-connection `Seq` lets the server detect
  duplicates/replays on a live connection; resume tokens bound cross-connection
  replay.
- 🟡 **Flood control**: per-connection token bucket on state-changing messages
  (`stateChanging` + `pkg/ratelimit`), returning `ErrFlood` with a `retry_after`.
  **Reads are outside it entirely** — `HISTORY`, `CHAT_LIST`, `THREAD`, `READ`,
  `PIN_LIST`, `DRAFT_SYNC`, `CONTACT_SYNC`, `INVITE_LIST`, `SCHEDULE_LIST` cost
  nothing. `HISTORY` is the worst of them: one small frame draws up to 100 full
  `NEW` frames plus a database read, and it resolves `@handle` through
  `resolveChat`, which is the very enumeration primitive `PROFILE_GET` was added
  to `stateChanging` to meter (P1-2).

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
- ✅ **Out-of-order & missing messages**: skipped-message keys are stored, bounded
  by `maxSkip = 1000` per call AND by `maxSkippedKeys` in total, evicting oldest
  first. The per-call bound alone was not a bound on memory: every DH ratchet
  step restarts the count, and the web port persists the map.
- ✅ **Nonce safety**: each message key is single-use, and the AEAD key+nonce are
  derived from it via HKDF, so the zero nonce is safe (never reused under a key).
- ✅ **Server is blind**: it stores only public prekeys (`internal/keydir`) and
  relays opaque ciphertext (`MsgSecretSend/Recv`); it can neither derive a shared
  secret nor read a message.

- ✅ **State is committed only after it is authenticated.** `Session.Decrypt`
  stages a DH ratchet step and any skipped-key run on a COPY and adopts it only
  once `aeadOpen` succeeds; an in-order message takes a cheaper branch that holds
  the receiving chain in two locals instead. This is what makes the relay safe to
  expose: `handleSecretSend` is the one send path with no chat membership behind
  it (only a block check), so any authenticated user can address any device, and
  the header they write drives the ratchet. Committing state before the AEAD
  check would let one frame with a random DH key and `PN=N=1000` permanently
  break a live session and force 2000 HMAC derivations, so every implementation
  stages the step on a copy and adopts it only on success.

- ✅ **The prekey signature check is mandatory**: X3DH rejects a bundle that
  carries no `SigningKey`/`SignedPreKeySig`, and `KEY_PUBLISH` refuses to store
  one. Verifying only when the fields are present would be a complete bypass by
  omission.

- ✅ **The directory is gated, bounded and budgeted**: `KEY_FETCH`/`KEY_FETCH_ALL`
  apply the same two-way block as every other way to reach a person, and answer a
  blocked caller exactly as they answer an account with nothing published. Published
  keys are length-checked, and entries expire after `EntryTTL` unless republished.
  Both fetches are also charged to the ACCOUNT (`allowUser(ctx, "keyfetch")`), not
  only to the socket: a fetch CONSUMES one of the target's one-time prekeys, so a
  per-connection bucket alone let a caller open several sockets and drain a device's
  batch of 256 in minutes. Draining reveals nothing, but it forces every later
  session with that device down to the weaker three-DH handshake until its owner
  republishes — somebody else's forward secrecy, degraded from an account that need
  only be unblocked. The two share one budget, because `KEY_FETCH_ALL` is the
  cheaper way to drain and a bucket of its own would double the allowance.

- ✅ **Prekey balance and rotation are reported, not guessed**: `KEY_PUBLISH` is
  answered with `KEY_STATE` (prekeys left, the age of the STORED signed prekey, how
  many of the frame's keys were kept). One-time prekeys are consumed by peers
  fetching bundles, so a device cannot observe its own balance falling — the local
  count drops only when a message decrypts with a key, which misses every fetch that
  never became a message. Without the reply the directory empties while the device
  believes it is full, and X3DH silently drops from four Diffie-Hellmans to three
  for every session started afterwards. The age closes the matching gap for the
  signed prekey: a device cannot know whether its own last rotation landed, so the
  directory reports the age of what it actually holds and the client rotates weekly
  against that, keeping the outgoing key for one generation so a peer that fetched
  the old bundle seconds earlier can still be answered. Clients publish only the
  keys the directory has not acknowledged, because publishing APPENDS: resending the
  whole pool on every connect filed each public key again, and a one-time prekey
  stored twice can be handed to two peers.

- ✅ **E2E ships on all three clients.** `pkg/e2e` is ported to
  `client/src/shared/lib/e2e`, `android/.../crypto` and
  `ios/SyncAppKit/Sources/Crypto` — X3DH, the ratchet with its staged commit,
  safety numbers and TOFU pinning in each. Android holds identity and session
  state under an AndroidKeyStore-wrapped key with transcripts in memory only; iOS
  keeps the identity in the Keychain. Everything else in this section describes
  all three plus `cmd/client`.

  The Swift `Sources/Crypto` directory was for a while not declared as a target in
  `Package.swift`, which means SPM silently compiled none of it — worth recording,
  because `swift build` was green throughout and the E2E implementation was dead
  code that read as shipped.

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
  key and verified unconditionally in X3DH (`pkg/e2e/sign.go`), so a malicious
  directory cannot substitute keys — nor drop the signature to skip the check.
- ✅ **Multi-device**: a sender fetches every device's prekeys via `KEY_FETCH_ALL`
  (peer's devices + its own other devices), establishes a session with each, and
  the server routes per-device ciphertext — Signal-style sender-side fanout, server
  stays blind.
- ✅ **Identity verification (safety numbers)**: `e2e.SafetyNumber` produces a
  symmetric 60-digit fingerprint of both identities (X25519 + Ed25519 keys) via
  Signal-style iterated hashing; comparing it out of band detects a directory
  MITM. Every client also pins identity keys on first use and refuses to send to
  a changed one until a person accepts it.
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
- ❌ Media signing secret and DB DSNs come from env with insecure dev defaults,
  and **nothing stops them reaching production**: `SYNCAPP_REQUIRE_TLS=1` is
  documented as the production switch, but an unset `SYNCAPP_MEDIA_SECRET` is
  only a `log.Warn` and the server boots on
  `dev-insecure-media-secret-change-me` (`internal/platform/tls.go:69`). Whoever
  knows that constant mints valid upload and download URLs for any blob. The
  same is true of an unset `SYNCAPP_ALLOWED_ORIGINS`, which leaves `CheckOrigin`
  returning true for every origin (P1-3). A warning does not stop a deploy;
  refusing to boot does.
  ⬜ Move to **Vault/KMS** in prod; the default media secret must never ship.
- ✅ **RBAC** (admin/moderator roles gate privileged ops like chat export). Roles
  come from `SYNCAPP_ADMIN_USERS`/`SYNCAPP_MODERATOR_USERS` (always held) and from
  the `platform_roles` table, granted and revoked with `cmd/roles`; a gateway
  caches a stored role for 30 s and treats a failed lookup as no role. The
  **append-only audit log** (`internal/audit`) records login, export and every
  role change; an
  **mTLS helper** (`pkg/mtls`) is ready for service-to-service auth when the
  monolith splits.
- ⬜ Encrypted backups; wire mTLS into the actual service mesh once split out.

---

## 9. Summary scorecard

| Area | State |
|------|-------|
| Parser robustness (fuzzed, capped, bomb-safe) | ✅ strong |
| Auth (argon2id, hashed tokens, enum-resistant, login throttle) | ✅ strong |
| Session lifecycle (logout, session list, account deletion) | ❌ **unreachable** — implemented, never wired |
| Authorization (per-op membership checks) | ✅ good |
| Membership & sharing (owner-only roles, last-owner guard, bounded revocable links, two-way blocking) | ✅ good |
| Idempotency / ordering / replay | ✅ strong |
| Flood / abuse control on writes | ✅ strong |
| Flood / abuse control on reads | ❌ **absent** — HISTORY/CHAT_LIST/THREAD/*_SYNC unmetered |
| Per-IP accept guard | 🟡 built, off by default, proxy-blind |
| Supply chain (CI: govulncheck CVE scan + gosec SAST + race + fuzz) | ✅ good |
| E2E primitives (X25519, Ed25519, HKDF, ChaCha20-Poly1305, Double Ratchet) | ✅ correct |
| E2E key authentication (prekey signature enforcement) | ✅ mandatory, enforced at publish and at use |
| E2E ratchet state handling (commit-after-authenticate, bounded skipped keys) | ✅ staged and bounded |
| E2E directory hygiene (block check, key-length validation, entry TTL) | ✅ good |
| E2E coverage across clients | ✅ web, Android, iOS and `cmd/client`, each with a safety-number screen and pinning |
| E2E cross-language interop proof | ✅ every port replays `server/testdata/e2e/vectors.json` byte for byte; CI runs web ↔ Go through a gateway (`server/scripts/secret-interop.sh`) |
| Media (signed URLs, unguessable refs, size caps, AV scan, nosniff) | ✅ strong |
| Transport encryption (TLS 1.3 on TCP/WS/QUIC) | ✅ optional, or enforced via `SYNCAPP_REQUIRE_TLS` |
| Observability (histograms, pprof, OTLP tracing) | ✅ good |
| RBAC + audit log | ✅ good |
| Data retention (outbox, scheduled, replay, media collected) | ✅ good |
| Secrets management (Vault/KMS), mTLS in the mesh | ⬜ before prod |
| Production config fails closed | ✅ `platform.EnforceProduction`, in both `cmd/server` and `cmd/gatewayd` |

**Bottom line.** The transport, storage and abuse-control layers are at a strong,
production-shaped level: a fuzzed and capped parser, sound auth (argon2id with a
flood guard, hashed tokens, enumeration-resistant login), idempotent and ordered
writes, a transactional outbox, RBAC with an audit log, AV scanning, and a CI
pipeline that CVE-scans dependencies and runs SAST, the race detector and fuzzing.
That half of the system is genuinely good.

The end-to-end encryption is built from audited primitives — X25519, X3DH, Double
Ratchet, HKDF, ChaCha20-Poly1305 — and the two gates around them that matter most
are closed and tested: the prekey signature is mandatory (P0-1), and the ratchet
authenticates before it commits (P0-2). All three clients
implement the ratchet, X3DH, safety numbers and pinning, and each shows the number
and blocks a send to a changed key. Compatibility between the four implementations is now tested rather
than assumed: the Go implementation generates `server/testdata/e2e/vectors.json`
(X3DH, a whole conversation with out-of-order delivery and a forged frame, safety
numbers, with every ratchet key recorded), and web, Android and iOS each replay
it and must produce the same bytes. CI also runs the web client against
`cmd/e2epeer` through a real gateway in both directions. Each port authenticates
the bytes the header travelled as rather than a re-encoding of them, so a
serialisation difference cannot break decryption either.

Below the crypto, the lesson that produced this section was the same one twice:
session revocation and account deletion were both fully implemented in the store
and reachable from no message, while this file listed them as defenses. Code that
nothing calls is not a defense, and listing it as one is how this document drifted
far enough to matter. Both are now wired end to end on all three clients, and the
protocol parity tests are what will report the next platform that falls behind —
that check is the durable part of the fix, not the seven types it added to iOS.

The remaining gaps are **deployment-layer**: a secrets manager, mTLS in the real
service mesh, and an upstream L4 flood scrubber. Production configuration itself
now fails closed — `SYNCAPP_REQUIRE_TLS` means production, and a process that
declares it refuses to boot with a development media secret, an empty origin
allow-list or absent per-IP caps, in the split topology as well as the monolith.
