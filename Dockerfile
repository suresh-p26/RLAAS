# ── Build stage ────────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

# Build-time version metadata; passed from CI via --build-arg.
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILT=unknown

RUN apk add --no-cache git ca-certificates

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV PKG=github.com/rlaas-io/rlaas/internal/version

# Build server binary with version metadata embedded.
RUN CGO_ENABLED=0 GOOS=linux \
    go build \
      -ldflags="-s -w \
        -X '${PKG}.Version=${VERSION}' \
        -X '${PKG}.Commit=${COMMIT}' \
        -X '${PKG}.BuildTime=${BUILT}'" \
      -o /bin/rlaas-server ./cmd/rlaas-server

# Build sidecar agent binary.
RUN CGO_ENABLED=0 GOOS=linux \
    go build \
      -ldflags="-s -w \
        -X '${PKG}.Version=${VERSION}' \
        -X '${PKG}.Commit=${COMMIT}' \
        -X '${PKG}.BuildTime=${BUILT}'" \
      -o /bin/rlaas-agent ./cmd/rlaas-agent

# ── Runtime stage ──────────────────────────────────────────────────────────
FROM alpine:3.19 AS runtime

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S rlaas && adduser -S rlaas -G rlaas \
    && mkdir -p /etc/rlaas /var/log/rlaas \
    && chown rlaas:rlaas /var/log/rlaas

COPY --from=builder /bin/rlaas-server /usr/local/bin/rlaas-server
COPY --from=builder /bin/rlaas-agent  /usr/local/bin/rlaas-agent

# Default policy file location inside the container.
COPY --chown=rlaas:rlaas examples/policies.json /etc/rlaas/policies.json

USER rlaas

# HTTP API + Prometheus /metrics are co-located on 8080.
EXPOSE 8080
# gRPC port
EXPOSE 9090

ENV RLAAS_POLICY_FILE=/etc/rlaas/policies.json
ENV RLAAS_GRPC_ADDR=:9090
ENV RLAAS_LOG_LEVEL=info
ENV RLAAS_LOG_FORMAT=json

HEALTHCHECK --interval=10s --timeout=3s --retries=3 \
    CMD wget --spider -q http://localhost:8080/readyz || exit 1

ENTRYPOINT ["rlaas-server"]
