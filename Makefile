.PHONY: build test lint vet fmt tidy vuln docker clean

# ── Variables ─────────────────────────────────────────────────
GO         ?= go
GOFLAGS    ?= -race
BIN_DIR    := bin
SERVER     := $(BIN_DIR)/rlaas-server
AGENT      := $(BIN_DIR)/rlaas-agent
COVERAGE   := coverage.out
MIN_COV    := 90

# Build-time version metadata injected into the binary.
VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILT      := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG        := github.com/rlaas-io/rlaas/internal/version
LDFLAGS    := -ldflags="-s -w \
  -X '$(PKG).Version=$(VERSION)' \
  -X '$(PKG).Commit=$(COMMIT)' \
  -X '$(PKG).BuildTime=$(BUILT)'"

# ── Build ─────────────────────────────────────────────────────
build: $(SERVER) $(AGENT)

$(SERVER):
	CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $@ ./cmd/rlaas-server

$(AGENT):
	CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $@ ./cmd/rlaas-agent

# ── Test & Coverage ───────────────────────────────────────────
test:
	$(GO) test $(GOFLAGS) ./...

cover:
	$(GO) test $(GOFLAGS) -coverprofile=$(COVERAGE) -covermode=atomic \
		$$($(GO) list ./... | grep -v 'api/proto')
	$(GO) tool cover -func=$(COVERAGE) | tail -1

# ── Code Quality ──────────────────────────────────────────────
fmt:
	gofmt -w .

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

golangci:
	golangci-lint run ./...

lint: vet fmt tidy golangci

# ── Security ──────────────────────────────────────────────────
vuln:
	$(GO) install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

# ── Docker ────────────────────────────────────────────────────
docker:
	docker build -t rlaas:latest .

docker-compose:
	docker compose --profile full up -d

# ── Clean ─────────────────────────────────────────────────────
clean:
	rm -rf $(BIN_DIR) $(COVERAGE)

# ── Help ──────────────────────────────────────────────────────
help:
	@echo "make build          Build server and agent binaries"
	@echo "make test           Run all tests with race detector"
	@echo "make cover          Run tests with coverage report"
	@echo "make fmt            Format all Go files"
	@echo "make vet            Run go vet"
	@echo "make tidy           Run go mod tidy"
	@echo "make golangci       Run golangci-lint"
	@echo "make lint           Run vet + fmt + tidy + golangci-lint"
	@echo "make vuln           Run govulncheck"
	@echo "make docker         Build Docker image"
	@echo "make docker-compose Start full stack with Redis + Postgres"
	@echo "make clean          Remove build artifacts"
