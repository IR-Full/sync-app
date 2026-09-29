# Working in this repository

A messenger with a Go server (`server/`), a web client (`client/`, which has its
own `CLAUDE.md`), an Android client (`android/`) and an iOS client (`ios/`), held
to one binary protocol.

## Comments and docs

- A comment says what the code does now and why it is shaped that way. Write the
  reason as a present-tense consequence ("without X, Y happens"), not as a story
  of what was broken before ("this used to…", "until now…", "the previous
  version…"). History belongs in the commit message.
- A closed security defect gets one row in `server/SECURITY.md`: what was wrong,
  and the test that holds it closed. Keep `SECURITY.ru.md` in step.
- Keep comments proportionate. The load-bearing "why" earns a paragraph; a
  getter does not need one.
- A Go file holds one concern with its constants, types and functions together.
  Do not split files by declaration kind.

## Where things are assembled

- The service graph for both deployments (monolith and split) is built in
  `server/internal/wiring`. A new dependency of the gateway goes into
  `wiring.NewEdge`, and `TestEdgeServicesAreComplete` fails until both topologies
  set it.
- Configuration is read once, by `wiring.FromEnv`. Read a new `SYNCAPP_*`
  variable there, not in a `main`.

## Checks

```bash
cd server  && gofmt -l . && go vet ./... && go test ./...
cd client  && npx tsc --noEmit && npx eslint src && npx prettier --check . && npx vitest run
cd android && ./gradlew :app:testDevelopmentDebugUnitTest
cd ios/SyncAppKit && swift test        # macOS only
```

Cross-language contracts are pinned by files the server generates:
`server/testdata/e2e/vectors.json` (regenerate with
`go test ./pkg/e2e -run TestVectors -update`) is replayed by every client, and
each client checks its message types (and bodies, on Android and iOS) against
`server/pkg/wire` and `server/proto`. `server/scripts/secret-interop.sh` runs the
web client against the Go implementation through a real gateway.
