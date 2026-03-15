# Build stage
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build server binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /bin/rlaas-server ./cmd/rlaas-server

# Build sidecar agent binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /bin/rlaas-agent ./cmd/rlaas-agent

# -----------------------------------------------------------
# Runtime stage — minimal scratch-based image
# -----------------------------------------------------------
FROM alpine:3.19 AS runtime

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S rlaas && adduser -S rlaas -G rlaas

COPY --from=builder /bin/rlaas-server /usr/local/bin/rlaas-server
COPY --from=builder /bin/rlaas-agent  /usr/local/bin/rlaas-agent

# Default policy file location inside the container
RUN mkdir -p /etc/rlaas
COPY examples/policies.json /etc/rlaas/policies.json

USER rlaas

# HTTP API port
EXPOSE 8080
# gRPC port
EXPOSE 9090

ENV RLAAS_POLICY_FILE=/etc/rlaas/policies.json
ENV RLAAS_GRPC_ADDR=:9090

ENTRYPOINT ["rlaas-server"]
