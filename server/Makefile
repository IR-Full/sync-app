# SyncApp developer tasks. On Windows, run these from Git Bash, or copy the
# underlying `go`/`docker` commands from the README if `make` is unavailable.

GO ?= go

.PHONY: build test test-race vet tidy fuzz perf interop ci vulncheck sec run-server run-server-infra client-alice client-bob infra-up infra-down test-integration test-integration-up

build:
	$(GO) build ./...

test:
	$(GO) test ./...

# Race detector: mirrors CI. Run this before pushing concurrency changes.
test-race:
	$(GO) test -race -count=1 ./...

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

# Local mirror of the CI gates (see .github/workflows/ci.yml).
ci: vet build test-race vulncheck sec

# CVE scan of dependencies + stdlib (reachable code only).
vulncheck:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...

# Static security analysis. G115 excluded (see .github/workflows/ci.yml rationale).
sec:
	$(GO) run github.com/securego/gosec/v2/cmd/gosec@latest -severity medium -exclude-generated -exclude=G115 ./...

# Performance regression check with thresholds (scripts/perf.sh; nightly in CI).
perf:
	bash scripts/perf.sh

# Secret-chat interop, web client ↔ pkg/e2e through a gateway (needs Node).
interop:
	bash scripts/secret-interop.sh

# Fuzz the binary protocol parser (Section 16). Ctrl-C to stop.
fuzz:
	$(GO) test ./pkg/wire -run=xxx -fuzz=FuzzParser -fuzztime=30s

# Regenerate protobuf + gRPC code from proto/syncapp/v1/*.proto.
#
# Requires protoc plus the generator plugins, PINNED to the version the checked-in
# output was produced with. A newer protoc-gen-go emits a different API shape, so
# an unpinned regenerate turns into an unrelated rewrite of 6000 lines:
#   go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2
#   go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
#
# body.proto -> envelope bodies (internal/wirepb); services.proto -> gRPC service
# stubs for the microservice split (internal/rpc/pb). The output is checked in as
# protoc writes it, one file per .proto.
proto:
	protoc --go_out=. --go_opt=module=github.com/SyncApp-chat/SyncApp proto/syncapp/v1/body.proto
	protoc --go_out=. --go_opt=module=github.com/SyncApp-chat/SyncApp \
	       --go-grpc_out=. --go-grpc_opt=module=github.com/SyncApp-chat/SyncApp \
	       proto/syncapp/v1/services.proto
	$(GO) build ./...

# Run the server fully in-memory (no infra needed).
run-server:
	$(GO) run ./cmd/server

# Run the server against the Docker infra (Postgres/Redis/NATS).
#
# The prefix matters and used to be wrong here. These were spelled SYNAPSE_*,
# the pre-rename name - but nothing reads that: the code reads SYNCAPP_*, and
# envcfg's legacy fallback is SYNCAPP_, the uppercase form of the NEW name. So
# every one of these was ignored and the server fell through to its in-memory
# stores (platform.go treats an empty SYNCAPP_PG_DSN as "no database"), making
# this target silently identical to run-server - infra up, nothing persisted,
# and no message anywhere saying so.
run-server-infra:
	SYNCAPP_PG_DSN="postgres://syncapp:syncapp@localhost:5432/syncapp?sslmode=disable" \
	SYNCAPP_REDIS_ADDR="localhost:6379" \
	SYNCAPP_NATS_URL="nats://localhost:4222" \
	$(GO) run ./cmd/server

# Interactive clients (open in separate terminals).
client-alice:
	$(GO) run ./cmd/client -user alice -pass secret123

client-bob:
	$(GO) run ./cmd/client -user bob -pass secret123

infra-up:
	docker compose up -d

infra-down:
	docker compose down

# --- integration tests -------------------------------------------------------
#
# Several packages have real tests that skip without a live dependency:
#
#   SYNCAPP_TEST_REDIS_ADDR   keydir, router, presence
#   SYNCAPP_TEST_NATS_URL     eventbus
#   SYNCAPP_TEST_PG_DSN       store/postgres, search
#   SYNCAPP_TEST_SHARD_DSNS   platform (needs two or more)
#
# They were written and then never run, because nothing connected the compose
# file to the variable names. That is what these two targets are for: the whole
# point of an integration test that skips silently is that nobody notices it
# has stopped being exercised.

PG_HOST ?= localhost
PG_PORT ?= $(or $(SYNCAPP_PG_PORT),5432)
REDIS_PORT ?= $(or $(SYNCAPP_REDIS_PORT),6379)
NATS_PORT ?= $(or $(SYNCAPP_NATS_PORT),4222)
PG_USER ?= syncapp
PG_PASS ?= syncapp
PG_BASE_DSN = postgres://$(PG_USER):$(PG_PASS)@$(PG_HOST):$(PG_PORT)

# The sharded test needs two or more DSNs. Two databases on one server satisfy
# it: what it asserts is that Load builds one outbox per shard, not that the
# shards live on different hosts.
SHARD_DB_1 = syncapp_shard_1
SHARD_DB_2 = syncapp_shard_2

# Brings the dependencies up, waits for them to be usable, then creates the two
# shard databases. Idempotent - a second run finds them and || true absorbs the
# "already exists" error.
test-integration-up:
	docker compose up -d --wait
	@for db in $(SHARD_DB_1) $(SHARD_DB_2); do \
		docker compose exec -T postgres psql -U $(PG_USER) -d $(PG_USER) \
			-c "CREATE DATABASE $$db" >/dev/null 2>&1 || true; \
	done
	@echo "infra ready"

# Runs the whole suite with every dependency wired in. -count=1 because a
# cached PASS from a run where these tests skipped would defeat the purpose.
test-integration: test-integration-up
	SYNCAPP_TEST_REDIS_ADDR=$(PG_HOST):$(REDIS_PORT) \
	SYNCAPP_TEST_NATS_URL=nats://$(PG_HOST):$(NATS_PORT) \
	SYNCAPP_TEST_PG_DSN="$(PG_BASE_DSN)/$(PG_USER)?sslmode=disable" \
	SYNCAPP_TEST_SHARD_DSNS="$(PG_BASE_DSN)/$(SHARD_DB_1)?sslmode=disable,$(PG_BASE_DSN)/$(SHARD_DB_2)?sslmode=disable" \
	$(GO) test -count=1 ./...
