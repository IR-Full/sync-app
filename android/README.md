# Synapse Android

Native Android client for the **Synapse** gateway (`../server`) — Kotlin, Jetpack
Compose, clean architecture, offline-first.

The server is not REST. It speaks a **custom binary protocol** with protobuf
bodies over TCP, WebSocket and QUIC, and this client implements that protocol from
scratch (`network/protocol/`) against the Go source, not against a guess.

---

## The protocol, as this client speaks it

**Transport: WebSocket** (`ws(s)://host/ws`). One binary WS message carries exactly
one frame, so there is no stream re-assembly; the same host serves the media
endpoints, and OkHttp handles both on one connection pool. Raw TCP (`:7000`) and
QUIC are the server's other doors — QUIC would give a phone connection migration
across WiFi↔LTE, but needs a QUIC library and TLS to be worth it (see
[Not implemented](#not-implemented)).

**Frame** (`pkg/wire/frame.go` → `protocol/Frame.kt`):

```
+--------+--------+--------+--------+--------------------+==================+
| 0x53   | 0x43   | VER(1) | FLAGS  | LENGTH (4, BE)     | PAYLOAD (LENGTH) |
|  'S'   |  'C'   |  0x01  | bits   | uint32 <= 16 MiB   | envelope bytes   |
+--------+--------+--------+--------+--------------------+==================+
```

Flags: `1<<0` gzip, `1<<1` zstd. We advertise `CapCompression` and gunzip inbound
frames (the gateway compresses anything over 256 bytes for peers that negotiated
it — real bytes saved on a mobile uplink). We do **not** advertise `CapZstd`: the
server compresses zstd against a shared raw dictionary we have no copy of, so such
a frame would be undecodable. Outbound frames are sent uncompressed; flags are per
frame, so no symmetry is required.

**Envelope** (`protocol/Envelope.kt`): five unsigned varints —
`type, seq, ack, requestId, bodyLength` — then the body.

- `seq` is our own per-connection counter, restarting at 1 on each socket.
- `ack` piggybacks the highest server `seq` we have seen; it keeps the connection's
  liveness fresh and is the cursor a RESUME replays from.
- `requestId` correlates a reply to a request. **Unsolicited pushes always carry 0,
  and that is the only thing distinguishing a backfilled history message from live
  fanout** — both arrive as `NEW` frames.

**Bodies are protobuf** (`proto/synapse/v1/body.proto`). `pkg/wire/protocodec.go`
installs the protobuf codec in its package `init()`, unconditionally — there is no
JSON to negotiate. Mapped by hand in `protocol/Bodies.kt` with `@ProtoNumber`, via
`kotlinx-serialization-protobuf`, so the build needs no `protoc` and the schema
stays readable next to the code. Two rules keep it compatible: every property has a
default (proto3 omits zero values), and `uint64` maps to `Long` (both varints; ids
travel as decimal strings, so nothing that could overflow arrives as an integer).

**Lifecycle** (`network/SynapseGateway.kt`):

```
dial -> HELLO/WELCOME  (capability negotiation, heartbeat_ms, max_inflight)
     -> AUTH (username/password, explicit register flag) or RESUME (resume_token)
     -> steady state: request/reply by requestId + unsolicited pushes
     -> drop -> jittered backoff -> redial, RESUME when the server allows
```

The gateway PINGs on the negotiated heartbeat (20 s) and tears down a connection
that goes quiet past the idle timeout (60 s), so answering PONG is mandatory. A
watchdog force-recycles the socket after 3 missed heartbeats — a suspended phone or
a cell handover leaves a socket that looks open and is not.

**Paged replies are streams.** `HISTORY` answers with N `NEW` frames sharing the
request id, then `HISTORY_OK` carrying `next_before`. `SynapseGateway.requestStream`
hides the correlation; `HistoryFetcher` owns the paging.

**Idempotency.** Every send carries a client `dedup_key`; the gateway maps
(device, dedupKey) → message id and answers a retry with `duplicate = true` and the
*same* id. That is what makes the offline outbox safe to flush repeatedly — the
foundation of "never lose a message, never duplicate one".

---

## Protocol gaps

Found by reading `server/`, and worked around rather than papered over. Each is
documented at the point in the code where it bites.

| Feature | What the server has | What this client does |
|---|---|---|
| **List my chats** | `store.ListUserChats` exists but **no message type exposes it** | The Room `chats` table **is** the chat list, not a cache of one. Rows appear when a chat announces itself: a `NEW` frame, a `SEND_ACK` resolving `"@username"`, `CHAT_INFO` from a create, `INVITES` from a join. Pull-to-refresh = one newest-page `HISTORY` per known chat. **A fresh install cannot enumerate existing conversations** until something arrives in them. One `MsgChatList` type on the server would close this. |
| **Profile: name, avatar** | `User.DisplayName` in the model; **no read or write over the wire**, no avatar concept at all | Name and photo are stored on this device and labelled as such in Settings. Avatars render as coloured initials. |
| **User search** | Exact `@username` only, resolved implicitly | "Find a person" uses `CONTACT_ADD`, the one request that takes a username and returns a user id (`NOT_FOUND` = no such user). So a lookup necessarily adds a contact. Usernames are recorded locally because the server never sends one back — `CONTACT_SYNC` returns ids and our own private labels. |
| **Chat type/title of an incoming chat** | A `NEW` frame carries a chat id and nothing else | Recorded as `unknown` and labelled from who writes in it: one other sender renders as a direct chat with that person, several as an untitled group. |
| **Presence / last seen** | `MsgPresence` is defined and `presence.publish` writes to the bus, but **nothing subscribes** — no PRESENCE frame ever reaches a client | Not shown. `ServerEvent.PresenceUpdate` is handled, so the day fanout subscribes to `user.presence` this client already renders it. |
| **Unread counts** | Not served | Counted locally from messages against our read cursor (`ChatDao.observeChatList`), so a stored counter cannot drift from the messages it counts. |
| **"Delivered" status** | `SEND_ACK` = durable persistence; `READ_UPD` = read receipt; fanout to a device is **not** acknowledged | Two states, honestly sourced: one tick (persisted) and two (read). No invented third. |
| **Per-chat mute** | `ChatMember.Muted` gates push, but no message sets it | Not offered. Notifications are global, and turning them off clears the push token server-side. |

---

## Architecture

```
app/src/main/java/com/synapse/messenger/
├── core/          Outcome/AppError, DI qualifiers
├── network/       protocol/  Frame, Envelope, MsgType, Cap, ErrorCode, Bodies, BodyCodec
│                  SynapseGateway  — connection lifecycle, request/reply, streams
│                  media/          — signed-URL upload over HTTP
├── database/      Room: entities, DAOs, SynapseDatabase
├── datastore/     SessionStore (tokens, device id), SettingsStore (theme/lang/push/endpoint)
├── data/          mapper/     wire ↔ storage ↔ domain
│                  repository/ Auth, Chat, Message, User, Media
│                  sync/       SyncCoordinator, MessageIngestor, HistoryFetcher,
│                              TypingTracker, NetworkMonitor
├── domain/        model/ repository/ usecase/   (no Android, no protocol)
├── presentation/  auth · chats · chat · newchat · settings · components · theme · navigation
├── push/          FCM service, token registrar, notification + deep link
└── di/            Hilt modules
```

**Layering rules that are actually enforced by the code:** network models are
separate from domain models, translated only in `data/mapper`; repositories hide the
protocol completely (no `"@username"` addressing convention crosses that line); no
ViewModel subscribes to the socket — screens observe Room, and Room is written from
exactly one place (`SyncCoordinator` → `MessageIngestor`).

### Decisions

**Hilt, not Koin.** Single-module Android-only app: Hilt verifies the graph at
compile time (a missing binding is a build error, not a crash on the screen that
needs it) and owns the entry points this app uses — `@HiltViewModel`, and injection
into a framework-constructed `FirebaseMessagingService`. Koin's advantages (no
annotation processing, KMP reach) would matter if this were shared with iOS; `ios/`
is a separate native target.

**OkHttp, not Ktor.** The transport is a binary WebSocket plus signed-URL HTTP for
media. One OkHttp client serves both with a shared connection pool, and its
WebSocket is the most battle-tested on Android. Ktor's multiplatform engine story
buys nothing here.

**kotlinx.serialization for protobuf.** Field numbers are declared with
`@ProtoNumber` beside a KDoc that names the server file they mirror — no `protoc`
step, no generated sources to keep in sync, and the schema is reviewable.

**Offline-first shape.** Room is the single source of truth; sends go to an outbox
keyed by dedup key and are flushed in composition order when the connection becomes
usable. A direct chat with someone new has no server id yet, so it lives under an
`"@username"` placeholder row that the first `SEND_ACK` promotes — which is what
lets a message to a stranger be composed with no network at all.

---

## Running it

### 1. Start the server

```bash
cd ../server && go run ./cmd/server
```

Zero setup: in-memory storage, WebSocket on `:8080/ws`. For durable infra see
`../server/README.md`.

### 2. Build and install

```bash
./gradlew installDevelopmentDebug
```

Requires JDK 17+ (Android Studio's bundled JBR works) and Android SDK 36. Create
`local.properties` with `sdk.dir=/path/to/Android/Sdk` if the IDE has not.

The `development` flavor points at `ws://10.0.2.2:8080/ws` — the emulator's route to
the host machine. On a **physical device**, either edit the flavor or set the URL at
runtime: Settings → Gateway → WebSocket URL (`ws://<your-LAN-ip>:8080/ws`), which
applies on the next connection.

### 3. Environments

| Flavor | Gateway | Endpoint override |
|---|---|---|
| `development` | `ws://10.0.2.2:8080/ws` | allowed |
| `staging` | `wss://staging.synapse.example/ws` | allowed |
| `production` | `wss://synapse.example/ws` | **refused** |

Production ignores the stored override so a stray preference can never send a user's
messages somewhere unintended. Point the flavors at your real hosts in
`app/build.gradle.kts`.

Release signing: drop a `keystore.properties` next to `settings.gradle.kts` with
`storeFile`, `storePassword`, `keyAlias`, `keyPassword`.

---

## Push notifications

Wired, and worth knowing exactly how far the server goes: `internal/notify` **does
not talk to FCM**. It POSTs

```json
{"token": "...", "platform": "android", "title": "New message",
 "body": "...", "chat_id": "...", "message_id": "..."}
```

to `SYNAPSE_PUSH_ENDPOINT` with `Authorization: Bearer $SYNAPSE_PUSH_KEY`. A
deployment therefore needs a small relay that forwards those keys to FCM **as a data
message** (data-only, so this client decides whether to show anything — notifications
off, or a chat already open).

To enable:

1. Put `app/google-services.json` in place. The Google Services plugin is applied
   only when that file exists, so the project builds without it and
   `BuildConfig.PUSH_ENABLED` reflects the truth.
2. Run the relay and set `SYNAPSE_PUSH_ENDPOINT` / `SYNAPSE_PUSH_KEY` on the server.

The client registers its token with `PUSH_TOKEN` on every successful connect and on
FCM rotation, and clears it (empty token) on logout or when notifications are turned
off — stopping pushes at the source rather than discarding them on arrival. A tap
opens `synapse://chat/<chat_id>` and lands in the conversation.

---

## Tests

```bash
./gradlew testDevelopmentDebugUnitTest
```

30 tests, weighted toward the part where a mistake is invisible: the codec. They
assert against byte layouts read from the server's source rather than round-tripping
our own encoder, which would pass just as happily if both sides of this client agreed
on the wrong thing.

**Interop test against a real gateway** — the strongest check available, since it is
the server that has to agree:

```bash
cd ../server && go run ./cmd/server            # terminal 1
SYNAPSE_TEST_WS=ws://localhost:8080/ws ./gradlew testDevelopmentDebugUnitTest   # terminal 2
```

`GatewayInteropTest` then drives a live handshake: HELLO/WELCOME capability
negotiation, AUTH with registration, `SEND` addressed to `"@username"` (checking the
gateway resolves and creates the direct chat), a dedup-key retry returning
`duplicate = true` with the same message id, the streamed `HISTORY` page terminated
by `HISTORY_OK`, `READ`, and the server's heartbeat PING. It skips without the env
var, mirroring the server's own convention for infra-dependent tests.

---

## Not implemented

The protocol supports these; they are out of this client's scope, and none of them
are stubbed or faked:

- **E2E secret chats** (X3DH + Double Ratchet). `CapSecretChat` is deliberately not
  advertised — claiming it would invite ciphertext this client cannot decrypt.
- **Calls** (`CALL_*` signaling; media is peer-to-peer and never touches the server).
- Reactions, threads, polls, pins, cross-device drafts, forwarding, scheduled sends,
  self-destruct composition (received TTL messages *are* honoured and purged),
  message edit/delete, full-text search, invite-link management, roles, chat export.
- **QUIC** and raw TCP transports.

Session tokens live in a DataStore file in app-private storage, excluded from backup
and device transfer (`xml/backup_rules.xml`). Hardware-backed encryption at rest
would be the next step for a production build.
