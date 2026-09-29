# SyncApp

A Telegram-shaped messenger built from the protocol up: a custom binary wire
format, a realtime gateway that runs either as one process or as ten, and three
independent client implementations that are held to the same protocol by test
rather than by review.

Nothing here is a wrapper around someone else's chat SDK. The frame format, the
envelope, the Double Ratchet, the storage layer and the fanout are all in this
repository, which is the point of it.

| | | Lines | Tests |
|---|---|---|---|
| [`server/`](server/) | Go 1.26 — gateway, domain services, storage, fanout | ~76 000 | 806 test functions |
| [`client/`](client/) | TypeScript, Next.js, feature-sliced | ~34 000 | 1 140 |
| [`android/`](android/) | Kotlin, Compose, Hilt, Room, clean architecture | ~23 000 | 386 |
| [`ios/`](ios/) | Swift, SwiftUI, SPM targets per layer | ~21 000 | 189 |

---

## Start here

| If you want to | Read |
|---|---|
| Understand the design and why it is shaped this way | [`server/ARCHITECTURE.md`](server/ARCHITECTURE.md) ([ru](server/ARCHITECTURE.ru.md)) |
| Know what is defended, what is not, and what was once claimed and false | [`server/SECURITY.md`](server/SECURITY.md) ([ru](server/SECURITY.ru.md)) |
| Follow a message from a keypress to a peer's screen | [`server/GUIDE.md`](server/GUIDE.md) ([ru](server/GUIDE.ru.md)) |
| Run the server | [`server/README.md`](server/README.md) ([ru](server/README.ru.md)) |
| Run a client | [`client/README.md`](client/README.md), [`android/README.md`](android/README.md), [`ios/README.md`](ios/README.md) ([ru](ios/README.ru.md)) |
| See what is queued next | [`NEXT.md`](NEXT.md) |

`SECURITY.md` is the one to read before trusting anything: it opens with the open
defects rather than the feature list, and each closed item names the test that
holds it closed rather than the commit that claimed it.

---

## The protocol

A frame is eight bytes of header and a payload:

```
+--------+--------+--------+--------+--------------------+==================+
| MAGIC(2)        | VER(1) | FLAGS  | LENGTH (4)         | PAYLOAD (LENGTH) |
+--------+--------+--------+--------+--------------------+==================+
  0x53 0x43         0x01     bits     uint32, ≤ 16 MiB     envelope bytes
```

The payload is an envelope — a varint-packed header (type, seq, ack, request id,
body length) plus a protobuf body. Framing and envelope are separate layers so
message semantics can change without touching the transport parser, and so each
can be fuzzed on its own.

`seq`/`ack` give in-order, gap-detecting, resumable delivery per connection;
`request_id` gives request/response correlation independent of ordering, so many
requests can be in flight at once. There are 116 message types
(`server/pkg/wire/constants.go`), and the rule that makes the protocol extensible
— an unknown type is skipped in silence — is also what makes client drift
expensive to notice. So each client carries a parity test that reads the Go
source and compares (number → name) pairs against it:

* `client/src/shared/api/protocol/msg-type.parity.test.ts`
* `android/.../network/protocol/MsgTypeParityTest.kt`
* `ios/SyncAppKit/Tests/NetworkTests/MsgTypeParityTests.swift`

The same protocol rides TCP, WebSocket (one frame per binary message) and QUIC.

## Two deployments, one core

`cmd/server` runs everything in one process. `cmd/gatewayd` plus nine service
daemons (`authd`, `chatd`, `messaged`, `presenced`, `keydird`, `fanoutd`,
`searchd`, `notifyd`, `moderationd`) run the same code split behind gRPC. Both are
assembled by one package, `server/internal/wiring`: `Monolith` and `Fleet` build
the same `gateway.Services` and differ only in whether the domain half is local
structs or gRPC clients. A test holds every field of it set in both topologies,
and another boots the split in-process and exercises the features the edge
serves.

With no infrastructure configured, everything falls back to in-memory stores, so
`go run ./cmd/server` works with nothing installed. Postgres, Redis and NATS are
picked up from environment DSNs when present.

## End-to-end encryption

X3DH and a Double Ratchet (`server/pkg/e2e`), ported to all three clients and
pinned against the Go implementation with fixed vectors. Signed prekeys are
mandatory — a bundle without a valid signature is refused at publish and at use —
and the ratchet stages state on a copy, adopting it only once the AEAD says the
message is genuine, so a forged frame costs a discarded copy instead of a
destroyed session.

The server is a blind relay: it moves ciphertext and an authenticated header and
can decrypt neither.

---

## Quick start

```bash
# Server, fully in-memory, no infrastructure needed
cd server && go run ./cmd/server

# Web client (npm or bun — both lockfiles are committed)
cd client && npm install && npm run dev

# Two interactive terminal clients, to watch the protocol work
cd server && go run ./cmd/client -user alice -pass secret123
cd server && go run ./cmd/client -user bob   -pass secret123
```

Before deploying anything, set `SYNCAPP_REQUIRE_TLS`: it is the switch that means
"this is production", and a process that declares it refuses to boot with a
development media secret, an empty WebSocket origin allow-list, or absent per-IP
connection caps. It reports every violation at once rather than one per restart.

## Checks

```bash
cd server  && gofmt -l . && go build ./... && go vet ./... && go test ./...
cd client  && npx tsc --noEmit && npx eslint src && npx vitest run
cd android && ./gradlew :app:testDevelopmentDebugUnitTest
cd ios/SyncAppKit && swift test        # macOS only
```

CI runs all four ([`.github/workflows/all.yml`](.github/workflows/all.yml)), with
the race detector, CVE scanning and SAST on the server.
