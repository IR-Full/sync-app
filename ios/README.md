# Synapse iOS

A native SwiftUI client for the Synapse gateway — the Go server in [`../server`](../server).

The server is **not REST**. It speaks a custom binary protocol over one long-lived
connection, and this client implements that protocol from scratch: framing,
envelope, protobuf bodies, handshake, session resume, and the streamed-page
convention. Nothing here is guessed; every decision below cites the server code
it came from.

Russian version: [README.ru.md](README.ru.md).

---

## The protocol, as implemented

### 1. Frame — `server/pkg/wire/frame.go`

```
+--------+--------+--------+--------+--------------------+==================+
| 'S'    | 'C'    | VER(1) | FLAGS  | LENGTH (4, BE)     | PAYLOAD (LENGTH) |
+--------+--------+--------+--------+--------------------+==================+
  0x53     0x43     0x01     bits     uint32               envelope bytes
```

The magic is a sync word so the gateway can reject port scans cheaply; `LENGTH`
is capped at 16 MiB so a hostile prefix cannot make either side reserve
gigabytes. Both checks are mirrored client-side — a client parsing a hostile
*server* is the same problem in a mirror. → [`Sources/Network/Wire/Frame.swift`](Sources/Network/Wire/Frame.swift)

Flags carry gzip (bit 0) and zstd (bit 1). **We advertise neither**, and that is
a decision, not an omission: the gateway only compresses when the negotiated
capability set says the peer can decompress, so declining the capability
guarantees every inbound frame is plaintext and lets `Frame.decode` treat a
compressed frame as the protocol violation it would be. Chat frames are a few
hundred bytes; shipping a zstd decoder plus the server's shared dictionary to
save on that is not yet a trade worth making.

### 2. Envelope — `server/pkg/wire/envelope.go`

Five LEB128 unsigned varints, then the body:

```
Type, Seq, Ack, RequestID, len(Body), Body
```

- `Seq` — our per-connection counter, 1-based, reset on every new socket.
- `Ack` — the highest **server** seq we have processed, piggybacked on
  everything we send. This is what lets the gateway trim its replay buffer
  without a dedicated ack frame.
- `RequestID` — correlation, independent of ordering, so many requests can be in
  flight at once. `0` means "unsolicited push".

→ [`Sources/Network/Wire/Envelope.swift`](Sources/Network/Wire/Envelope.swift)

### 3. Bodies — protobuf, not JSON

`server/pkg/wire/protocodec.go` installs the protobuf codec in the package
`init()`, unconditionally. There is no JSON fallback and nothing to negotiate;
the `wire.*Body` structs with their JSON tags are the *Go-side* API, and the
bytes on the wire are protobuf per `server/proto/synapse/v1/body.proto`.

The codec is hand-written — a proto3 reader/writer plus one conformance per body
— rather than generated with SwiftProtobuf. The bodies are ~40 flat messages of
scalars and strings, so a reader/writer pair is less machinery than the
generator it replaces, and the package stays dependency-free (no protoc step
that nobody can run from the repo as checked out).

The rule that matters is implemented in `ProtoReader`: **unknown fields are
skipped by wire type, never rejected.** A server that adds a field tomorrow must
not break a client shipped today.
→ [`Sources/Network/Proto/`](Sources/Network/Proto/)

### 4. Connection lifecycle — `server/internal/gateway/conn.go`

```
dial → HELLO/WELCOME (capability negotiation)
     → AUTH or RESUME (identity)
     → steady state: request/reply by RequestID + unsolicited pushes
     → drop → backoff+jitter → redial, resuming when possible
```

The gateway reads `HELLO` first and closes on anything else. It then accepts
exactly one of `AUTH` or `RESUME` as the next frame — so the choice is made
during the handshake, not opportunistically later.

- **Heartbeat.** The gateway pings every `heartbeat_ms` (20s) and reaps a
  connection at 60s of silence. We answer `PING` with `PONG`, which doubles as
  the client activity that refreshes our presence TTL server-side — the gateway
  only bumps it on inbound *client* frames.
- **Liveness watchdog.** 45s without any frame forces a redial. A suspended
  phone or a flaky link produces a socket the OS has not noticed is dead.
- **Resume.** `RESUME` replays the frames we missed from the Redis replay buffer
  instead of making us refetch history for every open chat. The resume cursor
  survives reconnects and is reset only after a *fresh* `AUTH` — a new session
  restarts the server's sequence at 1, so carrying the old cursor over would ask
  for a replay that does not exist.
- **Recoverable vs terminal.** A refused resume (expired buffer) falls through
  to a full `AUTH` with the bearer token we still hold. A 2xxx auth error does
  not: retrying cannot help, so the app goes to the login screen.

→ [`Sources/Network/Client/SynapseClient.swift`](Sources/Network/Client/SynapseClient.swift)

### 5. Streamed pages

`HISTORY`, `THREAD` and `CHAT_EXPORT` do not answer with a list. The gateway
replays stored messages as **ordinary `NEW` frames** sharing our `RequestID`,
then terminates the page with `HISTORY_OK` carrying the cursor. The shared
`RequestID` is the *only* thing distinguishing a backfilled message from live
fanout (which always carries `RequestID` 0) — get it wrong and every history page
is ingested twice. There is a test for exactly that.

### 6. Errors — `server/pkg/wire/constants.go`

Codes are grouped by class in decimal ranges so a client can react to a code it
has never seen:

| Range | Meaning | Client behaviour |
|-------|---------|------------------|
| 1xxx | transport/protocol | fix framing, retry |
| 2xxx | auth/session | **re-authenticate** — reconnecting cannot help |
| 3xxx | authorization/business | do not retry as-is |
| 4xxx | throttling | back off, honour `retry_after_ms` |
| 5xxx | server | retry with backoff (writes are idempotent) |

`ErrResumeExpired` (1004) is deliberately *not* in the auth range: it is
recoverable, and treating it as a dead session would log the user out on a
routine reconnect.

### 7. Transport

Both are implemented; the choice is a config value, not a code change.

| | When |
|---|---|
| **WebSocket** (`URLSession`, `/ws`) — default | Traverses proxies and captive portals that drop unknown TCP ports; system TLS, cellular fallback |
| **TCP** (`Network.framework`, `:7000`) | No HTTP upgrade or WS masking; `NWConnection` reports Wi-Fi↔LTE changes directly |

QUIC (ALPN `synapse-quic`) is not implemented: `Network.framework` exposes QUIC
streams on iOS 15+, but the gateway requires TLS for it, and the connection
migration it buys matters most on a network we do not yet run. The `Transport`
protocol is where it would go — one file, no other changes.

---

## Two things the protocol does not have

These are stated up front because the app is built *around* them rather than
pretending otherwise.

### There is no "list my chats" message

`store.ListUserChats` exists server-side (`internal/store/store.go:71`) but was
never given a wire type. So the chat list is **assembled locally** from
everything that mentions a chat — inbound `NEW`, `SEND_ACK`, `CHAT_INFO` from a
create, `INVITES.joined_chat` from a join, and handles we resolved — and
persisted in SQLite.

Consequence: **a fresh install on a new device starts with an empty list** and
fills in as traffic arrives. Two honest ways to close that gap, in order of
preference:

1. Add `MsgChatList`/`MsgChats` to the gateway (~40 lines: `ListUserChats` plus
   `Chat.Get` per id). The client change is one method.
2. Have the gateway push a `CHAT_INFO` per chat right after `AUTH_OK`.

### There is no profile API

`display_name` can only be supplied at registration — and `handlers.go:43` passes
an empty string — with no message type to update it afterwards, and no avatar
anywhere in the protocol. So the display name and avatar symbol in Settings are
**device-local**, and the screen says so in as many words rather than implying
they are visible to anyone else. Avatars are coloured monograms derived from the
user id.

Related: `AUTH_OK` returns ids and tokens but no username, and nothing can ask
for one later — so the app remembers the username the person typed at login.

### Also worth knowing

**User search is exact-handle only.** There is no directory and no prefix query.
The resolve rides `PIN_LIST`: it is read-only, exempt from the flood budget, and
its reply carries the *resolved* snowflake — whereas `HISTORY_OK` echoes back
whatever string we sent. `NOT_FOUND` means no such user; `FORBIDDEN` means a
block in either direction. (`SynapseClient.resolveDirectChat`.)

---

## Architecture

```
ios/
├── Sources/
│   ├── Network/       frames, envelope, proto3 codec, transports, client
│   ├── Domain/        entities, repository protocols, use cases  (depends on nothing)
│   ├── Persistence/   SQLite cache, sync engine, repository implementations
│   ├── Presentation/  ViewModels + SwiftUI screens               (Domain only)
│   └── DI/            composition root
├── App/               the app shell (@main, AppDelegate, Info.plist)
├── Config/            Dev / Stage / Prod .xcconfig
├── Resources/
│   ├── Strings/       en.lproj, ru.lproj
│   └── Assets/        colours, app icon
└── Tests/             NetworkTests, DomainTests, PersistenceTests
```

Each layer is a separate SPM target, so the dependency direction is enforced by
the **compiler**, not by convention: `Presentation` cannot reach the wire
protocol, and `Domain` depends on nothing at all. `ErrorMapping` is the single
file that knows both vocabularies — it converts `ProtocolError` into `AppError`
at the repository boundary, which is why no view model needs
`import SynapseNetwork` just to read a status code.

### Offline-first

One rule: **everything the gateway pushes is written to SQLite; the UI reads
only from SQLite.** There is no code path where the screen shows something the
cache does not have, so losing the connection changes how *fresh* the data is
and nothing else.

The send path is the whole story in one method:

1. Write an optimistic row **keyed by the dedup key** and enqueue an outbox row,
   in the same breath. Composing works with the radio off.
2. On `SEND_ACK`, the optimistic row is **promoted in place** — its primary key
   changes from the dedup key to the server snowflake — so the list keeps its
   identity instead of animating the message out and back in.
3. On reconnect, the outbox flushes **oldest first and serially**: the gateway
   allocates a gap-free `chat_seq` in arrival order, and the connection is
   flood-limited to 20 sends/sec, so firing a backlog concurrently would both
   reorder the user's own messages and trip the limiter.
4. A retry whose first attempt actually landed comes back with
   `duplicate = true` and the original message id. That is a success — it is
   exactly what the dedup key is for.

Unread counts are **derived** (`seq > last_read_seq`), not stored, so a read
receipt from another device fixes the badge with no counter to drift.

### Why SQLite directly, not Core Data / GRDB / Realm

The access pattern is query-shaped, not object-graph-shaped: "the 50 messages of
chat X below this cursor, newest first" and "chats by last activity with a
derived unread count" are two indexed SQL statements.

- **Core Data** would express them as fetch requests over a model that does not
  show up in a diff, and its concurrency model (contexts, merge policies) is a
  second concurrency system layered under Swift's own.
- **GRDB** is the right library for this shape and the one I would normally
  reach for — but it buys type-safe row mapping over ~250 lines, in exchange for
  a dependency to fetch, pin and keep in step with a hand-rolled wire codec that
  already has none.
- **Realm** brings its own object model and threading rules, which is a larger
  commitment than the problem.

So: the SQLite that ships with iOS, one `actor` for serialised access (SQLite's
own threading modes are a way to get subtle corruption for free), WAL mode, and
forward-only migrations keyed on `PRAGMA user_version` — the schema version
travels inside the file it describes.
→ [`Sources/Persistence/Database/`](Sources/Persistence/Database/)

### Concurrency

`async`/`await` throughout; `actor` for the three things with shared mutable
state (`SynapseClient`, `Database`, `SyncEngine`); `AsyncStream` for every
observation. View models are `@MainActor ObservableObject` — `@Observable` needs
iOS 17 and the deployment target is 16.

Reads are streams of **whole snapshots**, not deltas. For a chat list and a
message page that is the right trade: the cache is the single source of truth, so
a stream of snapshots cannot drift out of sync with it the way an event log can,
and SwiftUI diffs the snapshot anyway.

### Storage split

| What | Where | Why |
|---|---|---|
| Bearer token, resume token, device id | **Keychain** (`afterFirstUnlockThisDeviceOnly`) | They authenticate as the user. *afterFirstUnlock* so a push-triggered background launch can reconnect on a locked phone; *thisDeviceOnly* so they never ride a backup onto another device |
| Theme, language, notification prefs | `UserDefaults` | Not credentials, and needed synchronously at launch so the first frame is not the wrong theme |
| Chats, messages, outbox, contacts | SQLite | Queried, paged, and wiped on logout |

Logout wipes the cache. A messenger that shows the previous user's chats after a
logout has leaked them, whatever the login screen says.

---

## Running it

### Requirements

- macOS with **Xcode 15+** (Swift 5.9), iOS 16+ simulator or device
- [XcodeGen](https://github.com/yonaskolb/XcodeGen) — `brew install xcodegen`
- The Go server from [`../server`](../server)

There are **no Swift package dependencies**, so the project resolves offline.

### 1. Start the server

```bash
cd ../server && go run ./cmd/server
```

That gives you WebSocket on `:8080/ws`, raw TCP on `:7000`, and in-memory
storage — no Docker needed. The Dev configuration points at exactly this.

### 2. Generate the Xcode project

```bash
cd ios && xcodegen generate
```

```bash
open Synapse.xcodeproj
```

Pick the **Synapse Dev** scheme and run. If you would rather not install
XcodeGen: create an iOS App target in a new project, drag `ios/` in as a local
package, add `SynapseDI` + `SynapsePresentation` to the target, and point the
three build configurations at the `.xcconfig` files.

### 3. Try it

Register `alice` in the simulator, then talk to her from the CLI client:

```bash
cd ../server && go run ./cmd/client -register -user bob -pass secret123
```

In Bob's terminal, `/to @alice` and type. The message appears in the app in real
time; replying from the app arrives in Bob's terminal.

### Environments

| Scheme | Config | Gateway |
|---|---|---|
| Synapse Dev | `Config/Dev.xcconfig` | `ws://localhost:8080/ws` |
| Synapse Stage | `Config/Stage.xcconfig` | `wss://stage.synapse.example/ws` |
| Synapse Prod | `Config/Prod.xcconfig` | `wss://synapse.example/ws` |

Every value reaches the app through `Info.plist` substitution and is read once by
`ServerEnvironment.current` — no URL is hardcoded at a call site. Switching to
the raw TCP transport is `SYNAPSE_TRANSPORT = tcp` in an `.xcconfig`.

`ServerEnvironment.current` additionally refuses to honour
`SYNAPSE_ALLOWS_INSECURE_TLS` when the environment is `prod`, so a mistake in a
config file cannot silently disable certificate validation in a shipped build.

### Tests

⌘U in Xcode, or from a terminal:

```bash
xcodebuild test -scheme Synapse-Package -destination 'platform=iOS Simulator,name=iPhone 15'
```

Not `swift test`: the package declares iOS as its only platform, so the tests
have to be driven through a Simulator destination rather than the macOS host.
`Synapse-Package` is the scheme Xcode generates for a `Package.swift`.

**No Mac?** `.github/workflows/ios.yml` runs the whole thing — tests, app build,
and a screenshot of it launching in the Simulator — on a hosted macOS runner.
`git init && git push` an `ios/` repo (the same way `server/` and `client/` are
separate repos) and it runs on every push. Three suites, 75 tests:

- **NetworkTests** — byte-level framing (magic, big-endian length, oversized
  prefix, truncation), varint boundaries, envelope field order, proto3 golden
  bytes and unknown-field skipping, plus the full client driven against a
  `FakeGateway` that speaks the real protocol: handshake ordering, capability
  advertisement, request correlation with two sends in flight, streamed history
  not leaking into the push stream, `PING`→`PONG`, and `@handle` resolution.
- **DomainTests** — the validation rules that exist because of server behaviour,
  including the one that is easy to get wrong: `MaxTextLen` is a **byte** limit,
  so 4200 Cyrillic characters must be rejected even though they are well under
  8192 *characters*.
- **PersistenceTests** — dedup-key idempotency, optimistic-row promotion, outbox
  ordering and attachment round-tripping, derived unread counts, read markers
  never moving backwards, the paging cursor ignoring unsent rows, local expiry of
  self-destructing messages, draft last-writer-wins, and the migrations: a v1
  database must reach v2 *carrying its queued messages*, not be recreated.

---

## Push notifications

The client side is complete: APNs registration, `PUSH_TOKEN` on connect, and a
deep link from a notification tap into the chat (the server's payload carries
`chat_id` — `internal/notify/notify.types.go`). Turning notifications off sends
an **empty** token, which clears it server-side and stops the push at the source
rather than at the device.

The server side needs one thing you have to provide: `notify.ProviderFor` sends
to a generic HTTP endpoint (`SYNAPSE_PUSH_ENDPOINT`), not to APNs directly, and
defaults to a logger when unset. So pushes only arrive once that endpoint points
at an APNs bridge:

```bash
SYNAPSE_PUSH_ENDPOINT=https://your-apns-bridge/notify \
SYNAPSE_PUSH_KEY=... \
go run ./cmd/server
```

Until then everything works except the delivery itself — which is visible in the
server log rather than silently absent.

## Media

Bytes never travel as protocol frames. Two round trips per direction:

- **Upload** — `MEDIA_INIT` mints a ticket (signed URL + the `media_ref` to
  attach to a message), then HTTP `PUT`. The declared size is part of what the
  ticket signs and the server holds the body to it *exactly*, so the size sent
  at init must be the size uploaded. Limit: 100 MiB (`media.New`'s `maxSize`).
- **Download** — `MEDIA_FETCH` mints a signed, expiring URL; we `GET` it once and
  keep the file in `Caches/media/<ref>`, because the URL expires but the bytes do
  not.

Nothing auto-downloads while scrolling: every fetch costs a signed-URL round trip
first, so an attachment is a tappable card until asked for. The metadata (kind,
size, filename, dimensions) rides with the message precisely so the card can be
drawn without the blob. Our own uploads are cached under the ref the server
returned, so a photo we just sent renders from disk instead of being downloaded
back from the server we sent it to.

An upload needs a connection — the ticket is minted over the protocol — so unlike
a text message it cannot be composed offline. That is said to the user rather
than silently queued. What *does* queue is the message carrying the resulting
ref, which is why the outbox has an attachment column (schema v2).

## Drafts

`DRAFT_SET` is mirrored by the gateway to the user's **other** devices — routed
per-user, never to the chat — so a draft is cross-device continuation, not a
shared scratchpad. Saving is debounced 800 ms (the mirror rides the same
flood-limited connection as messages), and an incoming draft is only adopted when
the local composer is empty: overwriting what someone is typing because another
device typed something is worse than letting the two diverge. Sending clears it
everywhere.

## What is wired but not surfaced in the UI

The protocol client implements these; they have no screen yet, and the reason is
scope rather than difficulty: threads, pins, forwarding, invite links and roles,
scheduled sends, polls, calls, and E2E secret chats. Adding a screen for any of
them is a view model over a method that already exists.
