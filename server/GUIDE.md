# SyncApp: the guide from nothing

This is a detailed walk through the project **for a newcomer** opening this code
for the first time. It reads top to bottom. No prior knowledge of messengers is
needed — only a basic sense of what a program and a network are. By the end you
will understand **every important part**: from the custom binary protocol to
end-to-end encryption.

> One analogy for the whole document: picture **the main post office of a large
> city**. People (clients) send letters (messages). The post office accepts
> them, records them, sorts them into boxes and delivers them. SyncApp is that
> digital post office, but for instant messages.

## Contents

1. [What this is and what it is for](#1-what-this-is-and-what-it-is-for)
2. [Running it in two minutes](#2-running-it-in-two-minutes)
3. [Glossary](#3-glossary)
4. [The architecture, plainly](#4-the-architecture-plainly)
5. [The custom binary protocol, byte by byte](#5-the-custom-binary-protocol-byte-by-byte)
6. [One message end to end: Alice to Bob](#6-one-message-end-to-end-alice-to-bob)
7. [How data is stored](#7-how-data-is-stored)
8. [The event bus and fanout](#8-the-event-bus-and-fanout)
9. [Presence, "typing…", read receipts](#9-presence-typing-read-receipts)
10. [Media: how files travel](#10-media-how-files-travel)
11. [Search](#11-search)
12. [Moderation and anti-spam](#12-moderation-and-anti-spam)
13. [Secret chats: end-to-end encryption](#13-secret-chats-end-to-end-encryption)
14. [Security: what is defended](#14-security-what-is-defended)
15. [A map of the code: which file does what](#15-a-map-of-the-code-which-file-does-what)
16. [Trying it by hand](#16-trying-it-by-hand)
17. [Frequently asked questions](#17-frequently-asked-questions)

---

## 1. What this is and what it is for

This is the **backend of a messenger** at the scale Telegram operates, written
in **Go**. It does: one-to-one chats, groups, channels, delivery and
acknowledgement, online status, "typing…", read receipts, multi-device use,
editing and deleting messages, search, file uploads, push notifications, and
**end-to-end encryption** for secret chats.

Its defining feature is a **custom binary protocol** instead of the usual
HTTP/REST. Why? Because instant messaging cares about two things: **low
latency** and a **persistent connection**. Section 5 covers this in detail.

The project is a **working skeleton (an MVP)**: it genuinely runs, compiles and
passes its tests. Some of the heavier infrastructure (multi-region, a separate
database for messages) is described as a design rather than built, but the core
is real.

---

## 2. Running it in two minutes

All you need is Go 1.26+. The infrastructure (database, Redis) is optional —
everything defaults to running in memory.

```bash
# Terminal 1 — the server
go run ./cmd/server

# Terminal 2 — Alice (the first login registers, via the -register flag)
go run ./cmd/client -register -user alice -pass secret123

# Terminal 3 — Bob
go run ./cmd/client -register -user bob -pass secret123
```

In Alice's window, type:

```
/to @bob
hello, Bob!
```

Bob sees it instantly. From there try `/hist`, `/search hello`,
`/upload path/to/file`, `/typing`. The full list of commands — reactions,
threads, polls, calls, forwarding, self-destruct, scheduled sends, pins,
drafts, invites, roles — is printed on connect and covered in section 16.

---

## 3. Glossary

| Term | In plain words |
|------|----------------|
| **Client** | The user's program (our CLI here; it could be a phone app). |
| **Gateway** | The counter at the post office: it takes every connection and speaks our protocol. |
| **Service** | One self-contained piece of logic (auth, chats, messages…). |
| **Protocol** | The rules by which client and server exchange bytes. |
| **Frame** | One "packet" of bytes in our protocol — an envelope. |
| **Envelope** | What is inside the frame: a message type, some header fields, and a body. |
| **Seq** | A sequence number, so nothing is lost or reordered. |
| **Ack** | An acknowledgement: "I got that". |
| **Event bus** | The internal noticeboard: one service posts, the others read. |
| **Fanout** | Delivering one message to everyone who should receive it. |
| **Snowflake** | A way of minting unique IDs that also sort by time. |
| **E2E** | End-to-end encryption: not even the server can read it. |

---

## 4. The architecture, plainly

Picture the layers top to bottom. The client at the top, the database at the
bottom.

```
   Clients (phone = TCP/QUIC, browser = WebSocket, our CLI)
        │  speaking the binary protocol
        ▼
   ┌──────────────────────────────────────────────┐
   │  GATEWAY  — internal/gateway                 │
   │  • accepts connections                       │
   │  • checks who you are (authorisation)        │
   │  • watches ordering and liveness             │
   └───────┬──────────────────────────────┬───────┘
           │ calls the services           │ delivers the answers
           ▼                              ▲
   Services (the business logic):         │
   auth · chat · message · presence       │
           │ "posts to the noticeboard"   │
           ▼                              │
   EVENT BUS  ─────────────────────►  fanout delivers to the
           │                              subscribed recipients
     ┌─────┼───────┬──────────┐
     ▼     ▼       ▼          ▼
  search  push  moderation  DATABASE
                          (Postgres/Redis/in-memory)
```

**The idea behind the split:** "record a message" and "deliver a message" are
two different jobs with different requirements. Recording has to be reliable
(lose nothing). Delivery has to be fast and wide (in a channel, to millions). If
you mix them, one slow recipient stalls the write for everyone. So an **event
bus** stands between them, like a firewall.

---

## 5. The custom binary protocol, byte by byte

This is the heart of the project. Files: `pkg/wire/`.

### 5.1 Why not HTTP/JSON?

HTTP is like posting **a separate letter for every sneeze** and re-introducing
yourself each time. For a chat, where messages fly back and forth constantly,
that is expensive. What we want is **one persistent connection** with compact
messages flowing both ways. So we designed our own format — small and fast.

### 5.2 The frame — an envelope with a stamp

Every message on the wire looks like this (file `pkg/wire/frame.go`):

```
+--------+--------+---------+-------+--------------------+==================+
| 'S'    | 'C'    | version | flags | length (4 bytes)   | payload          |
| 0x53   | 0x43   | 0x01    | 0x00  | e.g. 0,0,0,17      | (envelope, 17 b) |
+--------+--------+---------+-------+--------------------+==================+
   \_____ 8-byte header ____________________________________/
```

Piece by piece:
- **`S` `C`** (2 bytes) — the magic word. If the first two bytes are anything
  else, this is not our protocol and we stop immediately. Protection against
  junk and port scanners.
- **version** (1 byte) — if the format ever changes, this number goes up.
- **flags** (1 byte) — switches. Bit 0 "payload is compressed", bit 1
  "compressed with zstd" (otherwise gzip); the rest are reserved.
- **length** (4 bytes) — how many bytes the payload occupies. **16 MB maximum**,
  so an attacker cannot write "length = 10 gigabytes" and eat the memory.
- **payload** — the envelope itself (below).

> Why a separate length field? Because TCP is a "stream of bytes with no
> boundaries", like water from a hose. To know where one message ends and the
> next begins, we write the length ourselves. This is called framing.

### 5.3 The envelope — what is inside

Inside the payload sits the **envelope** (`pkg/wire/envelope.go`). Its header is
compact variable-length integers (varints) rather than JSON, to keep it small:

```
Type · Seq · Ack · RequestID · body_length · body
```

- **Type** — what kind of message this is (see the table below).
- **Seq** — this message's sequence number on this connection. Increments by
  one. Lets a gap be noticed and recovered from after a drop.
- **Ack** — "I have processed everything up to this number". It rides along on
  every message, acknowledging receipt for free.
- **RequestID** — the ticket number. A reply carries the same RequestID as its
  request, so the client knows what an answer belongs to even with many requests
  in flight at once (this is called multiplexing).
- **body** — the contents for this particular type. Encoded in **protobuf** (a
  compact binary format) — schemas in `proto/SyncApp/v1/`, generated code in
  `internal/wirepb`, codec in `pkg/wire/protocodec.go`. The codec is
  **swappable** (`wire.SetBodyCodec`): there is a JSON codec for debugging, but
  protobuf is the default. The frame and envelope do not change either way —
  protobuf only touches the body.

### 5.4 Message types

The full list is in `pkg/wire/constants.go` (the numbers themselves) and
`pkg/wire/types.go` (the `MsgType` type and its names). The main ones:

| Type | Direction | Meaning |
|------|-----------|---------|
| `HELLO` / `WELCOME` | client→server / back | Introduction and capability negotiation. |
| `AUTH` / `AUTH_OK` | | Login/registration. |
| `PING` / `PONG` | both | "Are you alive?" — "Alive." |
| `SEND` / `SEND_ACK` | | Send a message / acknowledge it with an ID and a number. |
| `NEW` | server→client | A new message has arrived for you. |
| `READ` / `READ_UPD` | | I have read up to N / notify the others. |
| `TYPING` / `PRESENCE` | | "typing…" / online status. |
| `EDIT` / `DELETE` | | Edit / delete. |
| `HISTORY` / `HISTORY_OK` | | Load older messages. |
| `SEARCH` / `SEARCH_RESULTS` | | Search your own chats. |
| `MEDIA_INIT` / `MEDIA_TICKET` | | Begin a file upload / receive a link. |
| `KEY_PUBLISH` / `KEY_STATE` / `KEY_FETCH` / `KEY_FETCH_ALL` / `SECRET_SEND` | | Secret (E2E) chats; `KEY_FETCH_ALL` returns every device's prekeys, for multi-device sync. `KEY_PUBLISH` is answered with `KEY_STATE` — prekeys left, the age of the stored signed prekey, and how many of the keys just sent were kept — which is the only way a device can learn its own balance, since peers are what consume it. |
| `CHAT_EXPORT` | client→server | Export a chat's members and messages (owner/admin). |
| `RESUME` / `RESUME_OK` | | Restore a connection after a drop. |
| `REACT` / `REACT_UPD` | | Add/remove an emoji reaction / broadcast the new count to everyone. |
| `THREAD` / `THREAD_OK` | | Load the reply thread under a root message. |
| `POLL_CREATE` / `POLL_VOTE` / `POLL_STATE` | | Polls: create, vote, get the live tally. |
| `CALL_INVITE` / `CALL_ACCEPT` / `CALL_STATE` / `CALL_SIGNAL` | | Calls: invite, accept, room state, relaying SDP/ICE (the server does not parse them). |
| `CONTACT_ADD` / `CONTACT_SYNC` / `BLOCK` | | An address book with incremental sync, and blocking. |
| `FORWARD` | client→server | Copy a message into another chat along with its provenance. |
| `SCHEDULE` / `SCHEDULE_LIST` / `SCHEDULE_CANCEL` | | Send later; list and cancel what is scheduled. |
| `PIN` / `UNPIN` / `PINNED` | | Pins: shared per chat, delivered as a complete set. |
| `DRAFT_SET` / `DRAFT_SYNC` / `DRAFTS` | | A draft, private to the user but shared across their devices. |
| `SET_USERNAME` / `INVITE_CREATE` / `INVITE_REVOKE` / `JOIN` / `SET_ROLE` | | A chat's public address, invite links, joining, member permissions. |
| `CHAT_CREATE` / `CHAT_INFO` | | Create a group or channel / describe the created chat. |
| `PUSH_TOKEN` | client→server | Register (or clear) this device's push token. |
| `CHAT_LIST` / `CHATS` | | My chats, paged (the cursor is the last chat's id). The only way a fresh install learns which chats it is in. |
| `PROFILE_GET` / `PROFILE_SET` / `PROFILE` | | Profiles: read one (by id or `@handle` — which doubles as user search), change your own name/avatar, receive the result. |
| `DELIVERED` | server→client | "Your message reached the recipient's device" — the step between "stored" and "read". Raised by whichever gateway actually wrote the frame to the recipient's socket. |
| `ERROR` | server→client | An error with a code. |

### 5.5 The handshake: HELLO → WELCOME → AUTH → AUTH_OK

When a client connects there is a short conversation
(`internal/gateway/conn.go`):

1. The client sends **HELLO**: "I am this version, this device, and I can do
   compression and resume."
2. The server answers **WELCOME**: "agreed, these are the capabilities, ping me
   every 20 seconds."
3. The client sends **AUTH**: username and password (or a token). The `register`
   flag distinguishes "create an account" from "log in"; registration may carry
   a `display_name` straight away (afterwards the name changes only through
   `PROFILE_SET`).
4. The server checks it and answers **AUTH_OK** with an identifier, a token and
   the profile itself (`username`, `display_name`, `avatar_ref`) — a client that
   logged in with a stored token would otherwise know nothing about itself
   beyond its id.

From this point the connection is live and messages can flow.

### 5.6 Reliability: Seq, Ack, reconnection

Networks are unreliable: packets are lost, connections drop (a tunnel, a lift).
How do we not lose messages?

- Every message carries a **Seq** (number). The recipient sees 5, 6, **8** — so
  7 was lost and can be asked for again.
- **Ack** rides on every reply: "received up to N". The sender keeps the
  unacknowledged ones and resends them after a drop.
- On a drop the client reconnects and sends **RESUME** with its last Ack. The
  server continues from there, out of a short **replay buffer in Redis**
  (`internal/replay`); if the buffer is not deployed, the client picks up what
  it missed through `HISTORY`.

### 5.7 Three more important things

- **Heartbeat:** the server sends `PING` every 20 seconds. If a client stays
  silent too long (60 s) the connection is closed, so dead connections are not
  held open.
- **Backpressure and QoS lanes:** outbound traffic is split by priority —
  control > messages > "typing…"/presence, each with its own bounded queue.
  Under load the ephemeral lane is dropped first; if the durable queue
  overflows, the connection is closed (the client re-syncs afterwards) rather
  than slowing everyone down.
- **Compression:** large messages (>1 KB) are compressed with **zstd against a
  shared dictionary** (pre-trained on the protocol's frequent tokens, so small
  frames compress better), or with gzip. Decompression is capped — protection
  against a zip bomb.

---

## 6. One message end to end: Alice to Bob

Putting it together. Alice writes "hello" to Bob.

```
1. Alice → SEND {chat, idempotency_key, "hello"}
                                   │
2. Gateway: "is Alice allowed to write here?" → yes
                                   │
3. message.Send:
     • takes the chat's next seq number        ┐ one transaction —
     • stores the message in the database      ┘ all of it or none
     • posts "message.created" to the event bus
                                   │
4. Gateway → SEND_ACK to Alice {message ID, seq, time}   ← "reached the server"
                                   │
                       (the event bus)
                                   │
5. fanout reads the event → finds the chat's members → to Bob:
     • Bob online  → NEW {…"hello"…}  straight down his connection
     • Bob offline → a push-notification job
                                   │
6. Later Bob reads it → READ → Alice receives READ_UPD (the read ticks)
```

**What is guaranteed here:**
- The message **will not be lost** (database first, event second).
- Ordering within a chat is **strict** (the seq number grows with no holes).
- A resend (Alice pressed send twice on a bad connection) **will not create a
  duplicate** — the idempotency key handles that (see section 7).

---

## 7. How data is stored

Files: `internal/model` (structures), `internal/store` (interfaces),
`internal/store/postgres/migrations/` (versioned golang-migrate SQL migrations,
applied at startup). The main tables: users, devices, sessions, chats, members,
**messages**, read state, **outbox**.

Three ideas worth understanding:

### 7.1 Snowflake IDs — identifiers that carry information
Every message needs a unique number. We use **Snowflake** (`pkg/id`): a 64-bit
number with **time + node number + counter** packed into it. The advantages: it
is unique without coordination between servers, and it can be **sorted by time**
as a plain number.

### 7.2 Ordering within a chat — `seq`
Every chat has a `last_seq` counter. When a new message arrives the counter goes
up by one and becomes that message's number in that chat. A unique index on
`(chat_id, seq)` in the database guarantees **no holes and no duplicate
numbers**. Clients display messages in `seq` order.

### 7.3 Idempotency — protection against duplicates
The client attaches an **idempotency key** (a random string) to every send. The
database has a unique index on `(sender, key)`. If the same message arrives a
second time the database returns the one already stored, and **no new seq number
is spent** (allocating the number and inserting happen in one transaction). So
resends caused by a bad network do not breed copies.

### 7.4 Group commit — writing fast without giving up durability
The bottleneck in writing is the flush to disk (fsync): one expensive operation
per message. **Group commit** (`store/postgres/batch.go`) coalesces the messages
that arrive within a ~2 ms window into **one transaction with one fsync**
(statements are pipelined through `pgx.Batch`). Measured, this took a single
node from **230 to 3760 msg/s** (p50 latency 832 → 45 ms) **without sacrificing
durability** — seq ordering stays exact, and a failed batch falls back to
writing one at a time.

> The storage layer is **swappable**: everything runs in memory by default
> (`store/memory`), but there is a real **PostgreSQL** implementation
> (`store/postgres`). The interfaces in `store/store.go` make it possible later
> to move the messages table into a separate wide-column database
> (Cassandra/Scylla) without rewriting the logic.

---

## 8. The event bus and fanout

The **event bus** (`pkg/eventbus`) is the internal noticeboard. The message
service pins up "message X was created", and the other services (delivery,
search, moderation, push) read it and each do their own work. Nobody waits on
anybody directly.

By default the bus runs in memory; in production it runs on **NATS**
(`eventbus/nats.go`). The important events (`message.*`, `notify.*`) go through
**durable JetStream consumers**: if a worker dies, it resumes from where it
stopped after a restart and loses nothing. To keep an event from being lost
between the database write and the publish at all, there is a **transactional
outbox** (`internal/outbox`): the event is written to the `outbox` table in **the
same transaction** as the message, and a separate relay (`FOR UPDATE SKIP
LOCKED` + `LISTEN/NOTIFY`) publishes it to the bus.

**Fanout** (`internal/fanout`) is what reads "message.created" and **distributes**
the message to every member of the chat who currently has a connection open (on
all of their devices — multi-device). Anyone not online gets a push-notification
job. If a recipient is attached to **another node**, fanout finds their node in
the Redis registry (`internal/router`) and sends the delivery there over the bus
(`deliver.<node>`) — this is how horizontal scaling across many servers works.

---

## 9. Presence, "typing…", read receipts

Files: `internal/presence`.

- **Online / last seen** is kept in Redis (or in memory) with an expiry: the
  server refreshes the key on an interval; if the server dies and stops
  refreshing, the key disappears on its own and the person goes offline. No
  janitor needed.
- **"typing…"** is ephemeral and stored nowhere: it is simply relayed over the
  event bus to the others in the chat.
- **Read receipts** — `READ` moves the user's read cursor in the chat (forward
  only), and the other members receive `READ_UPD`.

---

## 10. Media: how files travel

Files: `internal/media`. The governing rule: **files do NOT travel over the
binary protocol**. Only a short reference (`media_ref`) goes over the protocol.

How it works:
1. The client sends `MEDIA_INIT` (name, size).
2. The service returns a **signed upload link** (`MEDIA_TICKET`). The signature
   is an HMAC with an expiry: it cannot be forged, and it goes stale after 15
   minutes.
3. The client **uploads the bytes over ordinary HTTP PUT** to that link —
   straight into storage (a directory locally; S3/cloud plus a CDN in
   production).
4. The client sends an ordinary message carrying the `media_ref`. The recipient
   asks `MEDIA_FETCH` and gets a signed download link.

This keeps the heavy bytes off the protocol and lets storage scale separately.

---

## 11. Search

Files: `internal/search`. The indexer **subscribes to message events** and
builds an inverted index — a dictionary of `word → which messages contain it`.
A `SEARCH` query finds the matches and **filters by permission**: you will only
find messages in chats you belong to. Secret (E2E) chats are not indexed — the
server sees only ciphertext. (`KEY_FETCH_ALL` used to hand **any** user's device
list to anyone and ignored blocking — a metadata leak rather than a content one;
both directory lookups now apply blocking and answer a blocked user exactly as
they would about an account that published nothing.) With a database, the index
lives in a **shared Postgres tsvector (GIN)** so every node reads and writes one
index; there is an in-memory variant for single-node development, and the same
interface will later accept OpenSearch for better ranking.

---

## 12. Moderation and anti-spam

Two layers:

1. **Flood control on the connection** (`pkg/ratelimit` + gateway): a token
   bucket. Every expensive action (sending, searching, uploading) spends a
   token; tokens refill over time. Go over and you get an `ERROR` with code
   `FLOOD` and a "try again in N ms".
2. **The moderation service** (`internal/moderation`) subscribes to message
   events, checks forbidden words and spam rate, and records incidents. It
   **observes and records** without slowing delivery down.

---

## 13. Secret chats: end-to-end encryption

The most crypto-heavy part. Files: `pkg/e2e`, `internal/keydir`. The goal: **not
even the server can read the conversation**. Only proven, standard algorithms
are used — **no home-made cryptography**.

> ⚠️ **Read this section together with its caveat.** The algorithms and the way
> they are assembled are correct. The audit of 2026-09-18 found two defects that
> meant the section's goal was not actually met — the prekey signature was
> verified only if one was sent, and the ratchet rewrote session state before
> it had established that the message was genuine — and **both are fixed**.
> Secret chats now exist in all three clients. What remains is narrower: **iOS
> has no screen that shows a safety number**, so the pinning it implements warns
> nobody. Details: [SECURITY.md](SECURITY.md) §6.

### 13.1 The building blocks (standard algorithms)

- **X25519** — a way for two people to agree on a shared secret even while being
  watched (elliptic-curve mathematics). Diffie–Hellman.
- **HKDF-SHA256** — a key mill: turns one secret into many keys.
- **ChaCha20-Poly1305** — encryption with an integrity check (AEAD): if a byte
  was substituted, decryption fails.

### 13.2 X3DH — the first handshake

The problem: Bob may be offline when Alice wants to start a secret chat. The
solution: Bob publishes **public prekeys** to the server in advance
(`internal/keydir`). Alice takes them and computes a **shared secret** with
several X25519 operations. Bob later computes **exactly the same secret** from
his private keys. The server sees only public keys and cannot derive the secret.

### 13.3 The Double Ratchet — one step per message

Then the double ratchet begins (as in Signal). The idea: **a new key for every
message**, and old keys cannot be recovered. Even if your phone is stolen today,
yesterday's messages cannot be read (this is forward secrecy), and after a
couple of messages the channel heals itself from a compromise.

The implementation (`pkg/e2e/ratchet.go`) is **covered by tests**:

- a back-and-forth conversation decrypts correctly;
- messages that arrive out of order still open (skipped keys are retained, but
  no more than 1000 — protection against a hostile header);
- flip a single bit of ciphertext and decryption is refused;
- identical text produces **different** ciphertext (the keys are unique).

**Separately — why the order of operations matters here.** The frame header is
written by whoever sent it, and any user can send you a secret frame. `Decrypt`
used to advance the ratchet according to that header first and authenticate the
contents only afterwards — so by the time AEAD said "forgery", the session state
had already been rewritten, and one forged frame was enough to **irreversibly
destroy your live session** with a genuine correspondent. Now, as the Double
Ratchet specification requires, changes are applied to a copy and committed only
after a successful decryption; messages that arrive in order take a cheaper
branch with nothing to copy. The 1000-skipped-key limit applies to one call, and
above it there is a total limit with oldest-first eviction — without it the map
grew without bound, because every ratchet step starts the count again.

### 13.4 The server's role, and multi-device sync

The server merely **relays sealed envelopes** (`SECRET_SEND` → `SECRET_RECV`)
between devices and stores the **public** prekeys of every device. It can
neither derive a key nor read a message.

**Secret chats sync across your devices** — on Signal's model, a session per
device. How it works: before sending, the client asks `KEY_FETCH_ALL` for the
prekeys of **all** of the correspondent's devices **and all of your other**
devices, establishes a separate E2E session with each, and sends one ciphertext
per device. The server routes each to its device address. That is how the
message reaches the correspondent's second phone and your tablet — while the
server still sees only ciphertext. Searching secret chats remains impossible on
the server (it has no plaintext), which is an inherent property of E2E rather
than a limitation of the sync.

---

## 14. Security: what is defended

In brief (the full audit is in [SECURITY.md](SECURITY.md)):

- **Passwords** — argon2id (slow, brute-force resistant), compared in constant
  time; for users that do not exist we spend the time anyway, so which usernames
  are taken is not revealed.
- **Session tokens** are stored in the database **only as SHA-256** — a database
  leak yields no working tokens.
- **The protocol parser** is defended: the magic word, the 16 MB limit,
  protection against zip bombs, and it is **fuzzed** (2M+ runs on random input —
  not one crash).
- **Against slowloris**: strict handshake timeouts for the unauthenticated, plus
  a cap on concurrent argon2 hashes, so a flood of logins cannot eat the memory.
- **Flood control** on the connection — but only for messages that **change
  state**. Reads (`HISTORY`, `CHAT_LIST`, `THREAD`, the syncs, the lists) cost
  nothing, and `HISTORY` answers one small request with a hundred full messages
  while also resolving people by `@name` along the way — free username
  enumeration. **Login throttling** by name (against brute force) works;
  **moderation** on content exists.
- **Media links** are signed and expire; a forgery is rejected; uploaded files
  go through an **antivirus scan** (a hook, whose default catches the EICAR test
  virus).
- **E2E** on standard algorithms, with **mandatory** verification of the prekey
  signature (Ed25519) — a bundle without a signature is refused both on publish
  and on use.
- **TLS 1.3** on TCP/WS/QUIC (optional; `SYNCAPP_REQUIRE_TLS=1` makes the server
  refuse to start at all without TLS); **RBAC** roles (admin/moderator) and an
  **append-only audit log** for sensitive operations (login, chat export).
- **Anti-DoS at accept**: a cap on connections and their rate from one IP cuts
  off floods and reconnect storms before the handshake.
- **Safety numbers for E2E**: two users can compare a 60-digit fingerprint out
  of band and satisfy themselves that the server did not substitute keys
  (defence against MITM).
- **Resilience**: a circuit breaker plus a local fallback if Redis goes away.
- **CI with security scans**: every push runs govulncheck (known CVEs in
  dependencies), gosec (static analysis), the race detector, and parser fuzzing.

**What is written but not reachable:** logging out, viewing your own sessions
and deleting your account are impossible — `auth.Revoke`, `ListSessions` and
`DeleteAccount` are implemented, but no protocol message leads to them. "Log
out" on the client simply forgets the token, and the session lives until it
expires (30 days).

What is deliberately left for later (deployment concerns, not code): a secrets
manager (Vault/KMS), taking mTLS into a real service mesh, an upstream L4 flood
scrubber — all listed honestly in SECURITY.md. One thing worth knowing
separately: today `SYNCAPP_REQUIRE_TLS=1` requires only TLS — it does **not**
require a real `SYNCAPP_MEDIA_SECRET` or `SYNCAPP_ALLOWED_ORIGINS`, so a
production start with the dev media secret or with "any origin" goes through
with nothing but a warning in the log.

---

## 15. A map of the code: which file does what

Packages follow one convention: `<name>.go` is behaviour, `<name>.types.go` is
structures and interfaces, `<name>.constants.go` is constants. So "what this is"
and "what it does" read separately, and you find things by filename rather than
by scrolling.

```
pkg/wire/            ← THE CUSTOM PROTOCOL (start here)
  frame.go             the frame: magic, version, length, compression
  envelope.go          the envelope: type, seq, ack, requestID, body
  constants.go         message type numbers, capabilities (Cap), error codes
  types.go             the types themselves (MsgType and its String)
  messages.types.go    the body structures per message
  protocodec.go        protobuf body codec (the default; JSON for debugging)
  compress.go          zstd + the shared dictionary (gzip as a fallback)
  codec.go, wsconn.go  reading/writing over TCP, WebSocket and QUIC
pkg/id/                Snowflake identifiers
pkg/eventbus/          the event bus (in-memory + NATS JetStream)
pkg/ratelimit/         the token bucket against floods
pkg/breaker/           circuit breaker for external dependencies
pkg/mtls/              mutual-TLS helper for service-to-service links
pkg/e2e/               end-to-end encryption (X3DH + Double Ratchet + Ed25519 signatures)
proto/ + internal/wirepb/  protobuf schemas and generated code

internal/model/        domain structures (User, Chat, Message…)
internal/store/        database interfaces + memory/ and postgres/ (group commit, migrations)
internal/auth/         registration, login, sessions, argon2id, token hashing
internal/chat/         chats, members, permissions (cached), seq allocation
internal/message/      writing/reading + the command broker (create/edit/delete)
internal/fanout/       fanning events out to connections (with per-node routing)
internal/router/       the "user → node" registry for multi-node (memory + Redis)
internal/keydir/       the directory of public prekeys for E2E (memory + Redis)
internal/presence/     online/last seen/"typing…" (memory + redis)
internal/delivery/     the "user → their connections" table on a node
internal/media/        upload/download, signed links, AV scanning
internal/search/       the indexer and permission-filtered search (Postgres tsvector)
internal/moderation/   forbidden words + spam rate
internal/notify/       push notifications (swappable provider)
internal/audit/        the append-only audit log (login, export)
internal/reaction/     emoji reactions (one per person, toggled)
internal/poll/         polls and the live tally
internal/call/         call signalling: rooms, roster, SDP/ICE relay
internal/contact/      address book and blocking (incremental sync)
internal/schedule/     scheduled sending (the dispatcher) + the self-destruct sweeper
internal/pin/          chat pins and cross-device drafts
internal/invite/       public chat addresses, invite links, roles
internal/rpc/          gRPC server/client adapters for the service split
internal/platform/     shared bootstrap for the cmd/*d daemons (stores, bus, mTLS)
internal/outbox/       the transactional outbox relay → bus
internal/replay/       the replay buffer for resume (memory + Redis)
internal/nodeid/       distributed lease of a node number (Redis)
internal/metrics/ · tracing/   Prometheus histograms + OpenTelemetry
internal/gateway/      THE GATEWAY: connections, handshake, authorisation, routing
  gateway.go             TCP accept (+ multi-accept), the shared reaper, RBAC, delivery
  conn.go                one connection's state, seq/ack, QoS lanes, timeouts
  handlers.go            the dispatcher: message type → handler, flood control
  handlers_*.go          the handlers themselves, by topic: media/e2e/calls,
                         membership (creation, invites, roles), social
                         (contacts, forwarding, pins, drafts),
                         profile (chat list and profiles)
  quic.go                the QUIC transport over UDP

cmd/server/            everything running together (one process)
cmd/gatewayd/ authd/ chatd/ messaged/ presenced/ keydird/   the same services over gRPC
cmd/fanoutd/ notifyd/ moderationd/ searchd/                 the bus workers
cmd/client/            the interactive terminal client (TCP/WS/QUIC/TLS)
cmd/loadtest/          the load harness (throughput + send→ack percentiles)

internal/store/postgres/migrations/   the versioned SQL schema (golang-migrate)
docker-compose.yml     Postgres + Redis + NATS for the grown-up mode
deploy/observability/  Prometheus + Tempo + Grafana
ARCHITECTURE.md        the full design document (18 sections)
SECURITY.md            the security audit
GUIDE.md               this file
GUIDE.ru.md            this file in Russian
```

**A recommended reading route for a newcomer:**
`pkg/wire/frame.go` → `pkg/wire/envelope.go` → `pkg/wire/constants.go` →
`internal/gateway/conn.go` → `internal/gateway/handlers.go` →
`internal/message/message.go` → `internal/fanout/fanout.go`. After that you
understand a message's whole journey.

---

## 16. Trying it by hand

- **Watch the protocol at work:** start the server and a client (section 2). The
  server logs every connection and event.
- **Run all the tests:**
  ```bash
  go test ./...
  ```
  The interesting ones are `internal/gateway/integration_test.go` (two real
  clients through the gateway: delivery, ordering, idempotency, search, secret
  relay, forward provenance and the self-destruct deadline),
  `internal/rpc/message_test.go` (the same path across a real gRPC hop, as in
  the service fleet) and `pkg/e2e/e2e_test.go` (encryption).
- **Try the product features** straight from the client. The full command list
  is printed on connect; the most interesting are the ones where the server's
  behaviour shows:
  ```
  /ttl 30                       everything you send from now on self-destructs
  hello                         "(deleted)" arrives 30 seconds later — the sweeper fired
  /forward <msgID> <chatID>     a copy marked "↪ from whom and from where"
  /schedule +2m later           a scheduled send; /scheduled lists, /unschedule cancels
  /pin <msgID>                  the pin reaches every member as a complete set
  /draft a draft                arrives on all YOUR devices and nobody else's
  /invite 5 +24h                a link good for 5 joins and a day; /invites, /revoke <code>
  /join <code>                  join by link (or /join @handle by the chat's address)
  ```
- **Fuzz the parser** (throw random bytes at it and confirm it does not crash):
  ```bash
  go test ./pkg/wire -fuzz=FuzzParser -fuzztime=30s
  ```
- **Load test** (how many messages per second one node carries):
  ```bash
  SYNCAPP_SEND_RATE=100000 go run ./cmd/server
  go run ./cmd/loadtest -addr localhost:7000 -conns 200 -msgs 50
  ```
  It reports throughput and send→ack latency percentiles (p50/p95/p99).
- **The grown-up mode** with a database: `docker compose up -d`, then start the
  server with `SYNCAPP_PG_DSN`, `SYNCAPP_REDIS_ADDR`, `SYNCAPP_NATS_URL`
  (see [README.md](README.md)).
- **Observability:** `docker compose -f deploy/observability/docker-compose.yml up -d`
  brings up Prometheus + Tempo + Grafana; `/metrics` serves latency histograms,
  and `SYNCAPP_PPROF=1` enables the profiler at `/debug/pprof/`.

---

## 17. Frequently asked questions

**Why Go?** Excellent concurrency (goroutines) for thousands of simultaneous
connections, fast, simple to deploy (a single binary).

**What format are message bodies in?** **Protobuf** by default (a compact binary
format; schemas in `proto/SyncApp/v1/`). The codec is swappable: there is a JSON
variant for debugging. The frame and envelope are binary regardless of the body
codec.

**Can this be shipped to production?** The core, yes — it is production-shaped
and load-tested (group commit: ~3760 msg/s per node). Before going live you need
the rest of what SECURITY.md lists (making TLS mandatory, a secrets manager, and
so on) and to catch up on connection scale (see the roadmap in ARCHITECTURE.md).

**Where is the "real" message database?** Postgres today (and in-memory for
demos). The `MessageStore` interface is deliberately shaped so Cassandra/Scylla
can be substituted later without rewriting the logic — described in
ARCHITECTURE.md, section 8.

**What is the difference between a cloud chat and a secret one?** The server
reads a cloud chat (to sync it across devices, search it, moderate it) — like
Telegram. A secret chat is end-to-end encrypted and the server sees only
ciphertext — like Signal. Cloud chats are the default; secret chats are opt-in.

---

That is all. You now understand the project as a whole, from the first byte of a
frame to end-to-end encryption. If anything is still unclear, open the
corresponding file from section 15: nearly every one carries a detailed comment
at the top.
