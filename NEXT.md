# What is next

A living backlog, not a handoff: a dated snapshot goes stale the day after it is
written, and a stale one sends the next person to re-fix what is already fixed.

Known gaps in what is *built* live in [`server/SECURITY.md`](server/SECURITY.md),
which leads with them by design. This file is the forward queue: things that are
not defects, just not done.

---

## Verified state (2026-09-29)

Everything green in CI (`All CI`, every job including iOS on `macos-14`):

```
server   gofmt · vet · test -race · golangci-lint · gosec · govulncheck · fuzz
         integration against Postgres, Redis and NATS (no test skipped)
         secret-chat interop, web ↔ Go through a gateway, both directions
web      tsc --noEmit · eslint · vitest                        1145 tests
android  unit tests · lint · assemble
ios      Swift package tests · SwiftLint · app build
```

Cross-language contracts are checked mechanically: every client replays
`server/testdata/e2e/vectors.json` byte for byte, holds its message types to
`server/pkg/wire`, and (Android, iOS) its bodies to `server/proto`.

---

## Queue

Ordered by value per unit of risk, not by number.

1. **Media in secret chats (P3-3).** Per-file key, ciphertext in blob storage,
   and `media_ref` + key + hash carried *inside* the ratchet ciphertext. Today an
   attachment in a secret chat cannot be sent at all; the thing to avoid is
   letting it fall back to plain `MEDIA_INIT`, which would send it unencrypted —
   worse than refusing. Add the explicit client-side refusal along with the
   feature.

2. **Android: the premium badge and accent palettes.** Both arrive
   (`ProfileBody.premium`, `SubscriptionBody.customThemes`) and nothing renders
   them; `Entitlements.isPremium` accepts any plan name but `free` and ignores
   the period end. Web and iOS show how: `isPremiumActive`, the `ProfileView`
   badge, and the accent row gated on `customThemes`.

3. **Admin API.** Platform roles live in the database and are managed with
   `cmd/roles`; a protocol surface for them (and for the rest of administration)
   is still designed only.

4. **Redis cache for a chat's hot tail (P4-4)** — roughly the last 100 messages,
   invalidated from the outbox that already exists.

5. **Partition `messages` (P4-3).**

6. **Sealed sender (P2-3), then header encryption and a PQ hybrid (P3-5,
   ML-KEM-768).** The PQ hybrid is the strongest novelty here and the cheapest to
   fit: `kdfRootFromX3DH` already concatenates its inputs, so it takes one more.

7. **Calls (P5-1).** Server signalling exists and is honestly documented as
   signalling only; there is no WebRTC on any client and no STUN/TURN
   configuration.

8. **SBP QR codes** render as the payment link's text rather than an image — no
   QR encoder is in any client's dependencies.

---

## Premium: what is sold vs what is delivered

**Read this before adding a premium feature.** The tier advertises nine
entitlements and enforces five.

Checked on 2026-09-29 by grepping every entitlement field for a use outside the
model and the wire mapping:

| Entitlement | Enforced? | Where |
|---|---|---|
| `SecretChats` | ✅ | `handlers_secretchat.go` refuses without it |
| `CustomThemes` | ✅ | client-side, the accent picker (web, iOS); not yet on Android |
| `MaxUploadBytes` | ✅ | `handlers_media.go` passes it to `media.InitUpload` |
| `MaxPinnedChats` | ✅ | `handlers_profile.go` refuses past it |
| `Badge` | ✅ | `ProfileBody.premium`; web and iOS render it, Android not yet |
| `Folders` | ❌ | nothing reads it; there are no folders |
| `AdvancedSearch` | ❌ | nothing reads it; search has no filters to gate |
| `PriorityDelivery` | ❌ | nothing reads it, though the QoS lanes it needs exist |
| `VoiceTranscription` | ❌ | nothing reads it; there is no transcription |

So the order below is deliberate: **finish what is already being charged for
before adding anything new.**

### Stage 1 — make the existing tier true

1. **`PriorityDelivery`.** The lanes already exist (`conn.lane`), so this is
   choosing `outHi` over `outMid` for an entitled sender. Worth measuring before
   claiming: under normal load the lanes are empty and the effect is nil, so
   either advertise it as "under load" or drop it from the list.
2. **Decide about `Folders`, `AdvancedSearch`, `VoiceTranscription`.** Each is a
   real feature that does not exist yet. Either build it or take it out of
   `PremiumEntitlements` — listing an entitlement nobody can exercise is a
   promise the code does not keep.

### Stage 2 — features worth adding, cheapest first

Ordered by value per unit of work, and each is grounded in something the codebase
already has rather than invented from scratch.

3. **Chat wallpapers.** A `media_ref` per chat, reusing the upload pipeline, the
   signed URLs and the GC that already exist. Natural companion to the accent
   palettes and the same shape of work: one new per-member setting, no protocol
   surface beyond a field on `CHAT_FLAGS`.
4. **Reserved usernames.** `@handle` is already unique and case-insensitive
   (`000010_usernames_invites`). Short handles are the scarce good every
   messenger ends up selling; the uniqueness index that makes them sellable is
   already there.
5. **Hide last-seen while still seeing others.** Privacy settings exist
   (`model.Privacy`), and the asymmetry — you see them, they do not see you — is
   exactly what people pay for elsewhere. One flag, one check in the presence
   audience filter (`presence_audience.go`).
6. **Longer edit and delete windows.** There is no window today, so this is a
   *new* limit on the free tier rather than a gift to the paid one. Worth stating
   plainly: introducing a restriction to sell its removal is a different
   product decision from adding a feature, and it annoys existing users.
7. **Larger groups / more invite links.** `CountMembersWithRole` and the invite
    tables make the ceilings cheap to enforce. Costs the server almost nothing,
    which is a reason to price it low rather than a reason to include it.
8. **Multi-account.** The session layer is already per-device with a device id
    the server assigns, so a second account is mostly a client-side store split.
    Big UI job, small protocol job.
9. **Message translation.** An external paid service, like transcription — so it
    is a genuine cost to recover, and it belongs in the same bucket as
    `VoiceTranscription`: build them together or neither.

### What not to sell

- **Anything that weakens a security property.** No "premium gets longer sessions"
  or "premium skips the second factor". The tier must never be a reason to make
  an account less safe.
- **Delivery of messages.** `PriorityDelivery` is already at the edge of this: a
  free tier whose messages are noticeably late is not a funnel, it is a broken
  messenger. Keep the paid advantage to latency under load, never to reliability.
- **Secret chats, on reflection.** Worth reopening as a product question rather
  than a technical one. E2E is the feature this project leads with, and gating it
  means most accounts never use the thing that makes the app worth building.
  Signal gives it away; Telegram gives its secret chats away too and sells
  cosmetics and ceilings. The code supports either choice — it is one flag in
  `FreeEntitlements` — so this is a decision, not work.

---

## How things are checked here

```bash
# server
cd server && gofmt -l . && go build ./... && go vet ./... && go test ./...
go test -race ./internal/gateway/ ./internal/billing/ ./internal/auth/

# web
cd client && npx tsc --noEmit && npx eslint src && npx vitest run

# android — the build variant is REQUIRED in the task name; plain
# :app:testDebugUnitTest is ambiguous because of the product flavours
cd android && ./gradlew :app:compileDevelopmentDebugKotlin \
  :app:testDevelopmentDebugUnitTest :app:lintDevelopmentDebug

# protobuf, after editing proto/syncapp/v1/*.proto
cd server && make proto      # or run the target's commands by hand; see below
```

### iOS is not built on this machine

`which swift swiftc xcodebuild` is empty, so the first real compile of any Swift
change happens in `.github/workflows/ios-ci.yml` on `macos-14` (run it on a
branch with `workflow_dispatch`). Structural checks
are the substitute and not a replacement: protocol/implementation agreement,
exhaustive `switch` over `SyncAppClient.Event` and `ErrorCode`, protobuf field
numbers against `body.proto`, and brace balance on edited files. Anything touching
Swift should say plainly that CI is the first compiler to see it.

### Environment notes worth not rediscovering

* **`make` is not in PATH** — run the `Makefile` target's commands by hand. The
  generated protobuf is checked in exactly as `protoc` writes it.
* **Regenerating protobuf is idempotent** — a clean regenerate against the current
  `.proto` produces no diff, so any diff it does produce is yours. Worth
  confirming before an edit rather than after.
* **Bash heredocs break on apostrophes in prose** (`unexpected EOF`). Non-trivial
  scripted edits are more reliable written to a file and run than piped through
  `bash <<EOF`.
* **Python here prints in cp1251** — a `print()` containing `→` or Cyrillic raises
  `UnicodeEncodeError`. Keep script output ASCII.
* **Local infrastructure for the integration suite**: without Docker, Postgres
  16 and Redis install with apt, and `go install
  github.com/nats-io/nats-server/v2@v2.10.22` gives NATS (`-js` for JetStream).
  Android's crypto package builds as a plain JVM project (Kotlin, BouncyCastle,
  kotlinx.serialization from Maven Central) when the Android SDK is out of reach.
