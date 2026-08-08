# Synapse — microservice fleet

The same system as `cmd/server` (the modular monolith), split into independently
deployable processes that talk **gRPC** (sync path) and **NATS** (async path) over
a shared Postgres/Redis/NATS data plane. Services hold no local state, so they
scale and deploy independently.

## Topology

```
                    clients (TCP / WebSocket / QUIC)
                               │
                          ┌────▼─────┐        gRPC
                          │ gatewayd │───────────────┬───────────┬───────────┐
                          │  (edge)  │               │           │           │
                          └────┬─────┘          ┌────▼───┐  ┌────▼────┐  ┌───▼────┐
                               │                │ authd  │  │  chatd  │  │messaged│
                               │ delivery       └────────┘  └────▲────┘  └───┬────┘
                               │ (bus)                       gRPC│           │ gRPC
                     ┌─────────▼──────────┐   ┌──────────┐       └───────────┘
                     │  presenced keydird │   │ fanoutd  │◄── message.* events
                     └────────────────────┘   │ searchd  │      (NATS bus)
                                               │ notifyd  │
                                               │moderationd│
                                               └──────────┘
        shared data plane:  Postgres (metadata + messages + outbox) · Redis
        (presence/router/keydir/resume) · NATS JetStream (event bus)
```

- **gRPC services:** `authd` (:9001), `chatd` (:9002), `messaged` (:9003,
  co-runs the outbox relay), `presenced` (:9004), `keydird` (:9005).
- **async workers (bus consumers):** `fanoutd`, `notifyd`, `moderationd`,
  `searchd` (indexer).
- **edge:** `gatewayd` — owns connections + protocol; dials the five services;
  keeps media-URL signing, the search query path, and the audit sink local.

## Run

```bash
docker compose -f deploy/microservices/docker-compose.yml up --build
# then (the load balancer is the single public entrypoint on :7000 / :8080):
go run ./cmd/client -addr localhost:7000 -register -user alice -pass secret123
```

## Horizontal scaling

The gateway runs as **N interchangeable replicas behind an HAProxy load
balancer** (service `lb`). Each replica leases a unique snowflake node id from
Redis and registers its live connections in the router (Redis), so a client may
land on **any** replica and still receive messages from users connected to any
other — delivery is routed node-to-node over the bus (`internal/router` +
`deliver.<node>`). Scale the edge in one command:

```bash
docker compose -f deploy/microservices/docker-compose.yml up -d --scale gatewayd=4
```

The domain services and workers scale the same way (`--scale messaged=6`,
`--scale fanoutd=8`, …): the gRPC services sit behind Docker's DNS round-robin,
and the bus workers use NATS **queue groups** (competing consumers), so adding an
instance adds throughput with no coordination. This is verified end-to-end —
`internal/gateway` `TestCrossNodeDelivery` plus a live two-gateway run where a
message sent through one gateway is delivered to a client on the other.

## Run locally without Docker images

Start the infra (`docker compose up -d` at the repo root), then launch each daemon
with the shared backends set — see the env in the compose file
(`SYNAPSE_PG_DSN`/`SYNAPSE_REDIS_ADDR`/`SYNAPSE_NATS_URL` + the `*_ADDR` peers).
The `cmd/server` monolith remains the zero-setup path (`go run ./cmd/server`).

## Notes

- **Shared state is required.** Separate processes do not share in-memory stores,
  so the fleet only works against real Postgres/Redis/NATS (the monolith is the
  in-memory path).
- **Latency tax.** Every send now crosses `gatewayd → messaged → chatd`, so
  per-message throughput is lower than the monolith — the expected cost of the
  split. Scale `messaged`/`fanoutd` horizontally to compensate.
- **mTLS** between services is enabled by setting `SYNAPSE_MTLS_CA/CERT/KEY`
  (see `internal/platform`); left off here for a zero-config local run.
