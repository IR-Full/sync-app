# What is next

A living backlog, not a handoff. It replaces `HANDOFF-2026-09-27.md`, which
described a broken build that builds clean and a list of unfinished work that was
finished — a dated snapshot goes stale the day after it is written, and a stale
one costs more than it saves because it sends the next person to re-fix what is
already fixed.

Known gaps in what is *built* live in [`server/SECURITY.md`](server/SECURITY.md),
which leads with them by design. This file is the forward queue: things that are
not defects, just not done.

---

## Verified state (2026-09-27)

Everything green, from a clean run:

```
server   gofmt -l . · go build ./... · go vet ./... · go test ./...   clean
         go test -race (gateway, store/memory, pkg/e2e, fanout)        ok
web      tsc --noEmit · eslint · vitest                        1140 tests
android  ./gradlew :app:testDevelopmentDebugUnitTest            386 tests
ios      not built locally — no Swift toolchain on this machine (see below)
```

Protocol parity is now checked mechanically on all three clients against
`server/pkg/wire/{constants,types}.go`, so a platform falling behind is a failing
test rather than a discovery. That check is what closed the last drift: iOS was
missing seven message types.

---

## Queue

Ordered by value per unit of risk, not by number.

1. **Interop matrix in CI (`e2epeer` + vectors in `testdata/`).** The four
   ratchet implementations are held together by shared constants and per-port
   test vectors; nothing runs one against another. The scripts to do it exist and
   cannot run, because the Go peer binary they drive is not in the repository.
   This is worth doing **first** and on its own: it breaks nothing and it is the
   only thing that would catch a real divergence.

   Note that the associated-data half of this item is already closed, and closed
   in a way that is *not* a breaking change — which the earlier plan assumed it
   would have to be. Each port now authenticates the exact bytes the ratchet
   header travelled as, keeping the same canonical encoding for headers it
   originates. An old peer and a new peer therefore still interoperate in both
   directions, so no transitional "accept either AD" release is needed.

2. **Media in secret chats (P3-3).** Per-file key, ciphertext in blob storage,
   and `media_ref` + key + hash carried *inside* the ratchet ciphertext. Today an
   attachment in a secret chat cannot be sent at all; the thing to avoid is
   letting it fall back to plain `MEDIA_INIT`, which would send it unencrypted —
   worse than refusing. Add the explicit client-side refusal along with the
   feature.

3. **Redis cache for a chat's hot tail (P4-4)** — roughly the last 100 messages,
   invalidated from the outbox that already exists.

4. **Partition `messages` (P4-3).**

5. **`cmd/loadtest` in CI (P4-6)** with p50/p99 and RSS thresholds.

6. **Sealed sender (P2-3), then header encryption and a PQ hybrid (P3-5,
   ML-KEM-768).** The PQ hybrid is the strongest novelty here and the cheapest to
   fit: `kdfRootFromX3DH` already concatenates its inputs, so it takes one more.

7. **Calls (P5-1).** Server signalling exists and is honestly documented as
   signalling only; there is no WebRTC on any client and no STUN/TURN
   configuration.

8. **SBP QR codes** render as the payment link's text rather than an image — no
   QR encoder is in any client's dependencies.

9. **iOS safety-number screen** — tracked as P2-1 in `SECURITY.md` because it is
   a gap in a shipped security claim rather than a missing feature, but it is
   small and it belongs on this list too.

---

## Premium: what is sold vs what is delivered

**Read this before adding a premium feature.** The tier currently advertises nine
entitlements and enforces two.

Checked on 2026-09-28 by grepping every entitlement field for a use outside the
model, the gateway's wire mapping, and generated protobuf getters:

| Entitlement | Enforced? | Where |
|---|---|---|
| `SecretChats` | ✅ | `handlers_secretchat.go:64` refuses without it |
| `CustomThemes` | ✅ | client-side, the accent picker in appearance settings |
| `MaxUploadBytes` | ❌ | **contradicted** — see below |
| `MaxPinnedChats` | ❌ | nothing reads it |
| `Folders` | ❌ | nothing reads it; there are no folders |
| `AdvancedSearch` | ❌ | nothing reads it; search has no filters to gate |
| `PriorityDelivery` | ❌ | nothing reads it, though the QoS lanes it needs exist |
| `VoiceTranscription` | ❌ | nothing reads it; there is no transcription |
| `Badge` | ❌ | nothing reads it; no client renders a badge |

`MaxUploadBytes` is the one that is actively wrong rather than merely absent.
`media.New` caps every upload at a hard-coded `100 << 20` (`media.go:63`), which
is exactly what the FREE tier promises — so a Premium account is told it gets
4 GiB and is refused at 100 MiB. That is a paid promise the code declines to
keep, and it is a refund conversation rather than a missing feature.

So the order below is deliberate: **finish what is already being charged for
before adding anything new.** A tier that under-delivers does not get fixed by
having more items on the list.

### Stage 1 — make the existing tier true

1. **`MaxUploadBytes`.** Pass the caller's entitlement into `media.InitUpload`
   instead of the constructor constant, and keep the constant as the ceiling no
   tier may exceed. The HTTP PUT handler must re-check the size it actually
   receives — the ticket's declared size is client-asserted, and a signed ticket
   for 100 MiB must not accept 4 GiB of body.
2. **`MaxPinnedChats`.** `handlePin` counts existing pins and refuses past the
   ceiling with `ErrPremiumRequired`, which the clients already render as an
   upgrade prompt rather than a dead end.
3. **`Badge`.** The cheapest honest one: a flag on the profile body, a marker
   next to the name in the three clients. It is cosmetic, and cosmetic status is
   most of why people buy a tier.
4. **`PriorityDelivery`.** The lanes already exist (`conn.lane`), so this is
   choosing `outHi` over `outMid` for an entitled sender. Worth measuring before
   claiming: under normal load the lanes are empty and the effect is nil, so
   either advertise it as "under load" or drop it from the list.
5. **Decide about `Folders`, `AdvancedSearch`, `VoiceTranscription`.** Each is a
   real feature that does not exist yet. Either build it or take it out of
   `PremiumEntitlements` — listing an entitlement nobody can exercise is the same
   defect as the four above, just less obvious.

### Stage 2 — features worth adding, cheapest first

Ordered by value per unit of work, and each is grounded in something the codebase
already has rather than invented from scratch.

6. **Chat wallpapers.** A `media_ref` per chat, reusing the upload pipeline, the
   signed URLs and the GC that already exist. Natural companion to the accent
   palettes and the same shape of work: one new per-member setting, no protocol
   surface beyond a field on `CHAT_FLAGS`.
7. **Reserved usernames.** `@handle` is already unique and case-insensitive
   (`000010_usernames_invites`). Short handles are the scarce good every
   messenger ends up selling; the uniqueness index that makes them sellable is
   already there.
8. **Hide last-seen while still seeing others.** Privacy settings exist
   (`model.Privacy`), and the asymmetry — you see them, they do not see you — is
   exactly what people pay for elsewhere. One flag, one check in the presence
   audience filter (`presence_audience.go`).
9. **Longer edit and delete windows.** There is no window today, so this is a
   *new* limit on the free tier rather than a gift to the paid one. Worth stating
   plainly: introducing a restriction to sell its removal is a different
   product decision from adding a feature, and it annoys existing users.
10. **Larger groups / more invite links.** `CountMembersWithRole` and the invite
    tables make the ceilings cheap to enforce. Costs the server almost nothing,
    which is a reason to price it low rather than a reason to include it.
11. **Multi-account.** The session layer is already per-device with a device id
    the server assigns, so a second account is mostly a client-side store split.
    Big UI job, small protocol job.
12. **Message translation.** An external paid service, like transcription — so it
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
change happens in `.github/workflows/ios-ci.yml` on `macos-14`. Structural checks
are the substitute and not a replacement: protocol/implementation agreement,
exhaustive `switch` over `SyncAppClient.Event` and `ErrorCode`, protobuf field
numbers against `body.proto`, and brace balance on edited files. Anything touching
Swift should say plainly that CI is the first compiler to see it.

### Environment notes worth not rediscovering

* **`make` is not in PATH** — run the `Makefile` target's commands by hand. The
  `proto` target in particular must keep its `scripts/split-pb.py` calls: the
  generated files are checked in already split by declaration kind, and skipping
  the split leaves the same declarations in two files and the package stops
  compiling.
* **Regenerating protobuf is idempotent** — a clean regenerate against the current
  `.proto` produces no diff, so any diff it does produce is yours. Worth
  confirming before an edit rather than after.
* **Bash heredocs break on apostrophes in prose** (`unexpected EOF`). Non-trivial
  scripted edits are more reliable written to a file and run than piped through
  `bash <<EOF`.
* **Python here prints in cp1251** — a `print()` containing `→` or Cyrillic raises
  `UnicodeEncodeError`. Keep script output ASCII.
* The Synapse→SyncApp rename in `d1ed835` shows as staged renames in
  `git status`; that is the commit, not pending work.
