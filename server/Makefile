# Synapse developer tasks. On Windows, run these from Git Bash, or copy the
# underlying `go`/`docker` commands from the README if `make` is unavailable.

GO ?= go

.PHONY: build test test-race vet tidy fuzz ci vulncheck sec run-server run-server-infra client-alice client-bob infra-up infra-down

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

# Fuzz the binary protocol parser (Section 16). Ctrl-C to stop.
fuzz:
	$(GO) test ./pkg/wire -run=xxx -fuzz=FuzzParser -fuzztime=30s

# Regenerate protobuf + gRPC code from proto/synapse/v1/*.proto. Requires protoc,
# protoc-gen-go, and protoc-gen-go-grpc:
#   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#   go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
# body.proto → envelope bodies (internal/wirepb); services.proto → gRPC service
# stubs for the microservice split (internal/rpc/pb).
proto:
	protoc --go_out=. --go_opt=module=github.com/synapse-chat/synapse proto/synapse/v1/body.proto
	protoc --go_out=. --go_opt=module=github.com/synapse-chat/synapse \
	       --go-grpc_out=. --go-grpc_opt=module=github.com/synapse-chat/synapse \
	       proto/synapse/v1/services.proto

# Run the server fully in-memory (no infra needed).
run-server:
	$(GO) run ./cmd/server

# Run the server against the Docker infra (Postgres/Redis/NATS).
run-server-infra:
	SYNAPSE_PG_DSN="postgres://synapse:synapse@localhost:5432/synapse?sslmode=disable" \
	SYNAPSE_REDIS_ADDR="localhost:6379" \
	SYNAPSE_NATS_URL="nats://localhost:4222" \
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
