# One image containing every Synapse daemon; each compose service runs a different
# binary. Multi-stage: build static binaries, ship them on distroless.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO off → fully static binaries that run on the distroless base.
RUN CGO_ENABLED=0 go build -o /out/ \
    ./cmd/authd ./cmd/chatd ./cmd/messaged ./cmd/presenced ./cmd/keydird \
    ./cmd/fanoutd ./cmd/notifyd ./cmd/moderationd ./cmd/searchd ./cmd/gatewayd

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/ /usr/local/bin/
# The compose file selects the binary per service via `command:`.
