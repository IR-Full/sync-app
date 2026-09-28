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
