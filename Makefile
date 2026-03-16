.PHONY: all build clean test test-unit test-protocol test-integration test-docker lint generate \
       coverage docker docker-build docker-run docker-test fmt vet install deps

BINARY    := lantern
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT    := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS   := -ldflags "-X github.com/pfh/lantern/pkg/config.Version=$(VERSION) \
              -X github.com/pfh/lantern/pkg/config.Commit=$(COMMIT) \
              -X github.com/pfh/lantern/pkg/config.BuildTime=$(BUILD_TIME)"

# ─── Build ────────────────────────────────────────────

all: generate build

build:
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/lantern

build-linux:
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o bin/$(BINARY)-linux-amd64 ./cmd/lantern
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o bin/$(BINARY)-linux-arm64 ./cmd/lantern

build-darwin:
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o bin/$(BINARY)-darwin-amd64 ./cmd/lantern
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o bin/$(BINARY)-darwin-arm64 ./cmd/lantern

build-all: build-linux build-darwin

generate:
	@echo "Run 'templ generate' if you have templ installed"
	@# templ generate ./pkg/web/templates/

# ─── Test ─────────────────────────────────────────────

test:
	go test -race -v ./...

test-unit:
	go test -race -v -run 'Test[^_]' ./internal/... ./pkg/model/... ./pkg/blocker/... ./pkg/config/...

test-protocol:
	go test -race -v ./pkg/dns/... ./pkg/cache/...

test-integration:
	go test -race -v -tags integration ./...

test-short:
	go test -race -short ./...

coverage:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

bench:
	go test -bench=. -benchmem ./...

# ─── Quality ──────────────────────────────────────────

fmt:
	gofmt -s -w .

vet:
	go vet ./...

lint:
	golangci-lint run ./...

check: fmt vet lint test

# ─── Docker ───────────────────────────────────────────

docker-build:
	docker build -t lantern:$(VERSION) -t lantern:latest .

docker-run:
	docker compose up -d

docker-stop:
	docker compose down

docker-logs:
	docker compose logs -f

# ─── Docker Integration Tests ────────────────────────

docker-test:
	docker compose -f docker-compose.test.yml up --build --abort-on-container-exit --exit-code-from test-dns
	@echo ""
	@echo "DNS tests passed. Running DHCP tests..."
	docker compose -f docker-compose.test.yml up --build --abort-on-container-exit --exit-code-from test-dhcp

docker-test-dns:
	docker compose -f docker-compose.test.yml up --build --abort-on-container-exit --exit-code-from test-dns

docker-test-dhcp:
	docker compose -f docker-compose.test.yml up --build --abort-on-container-exit --exit-code-from test-dhcp

docker-test-clean:
	docker compose -f docker-compose.test.yml down -v --remove-orphans

# ─── Install / Clean ─────────────────────────────────

install: build
	install -m 755 bin/$(BINARY) /usr/local/bin/

clean:
	rm -rf bin/ coverage.out coverage.html

deps:
	go mod tidy
	go mod download

# ─── Help ─────────────────────────────────────────────

help:
	@echo "Lantern Build Targets:"
	@echo ""
	@echo "  build          Build binary to bin/lantern"
	@echo "  build-all      Cross-compile for linux + darwin (amd64 + arm64)"
	@echo "  test           Run all tests with race detector"
	@echo "  test-unit      Run unit tests only (netutil, model, blocker, config)"
	@echo "  test-protocol  Run protocol tests (dns, cache)"
	@echo "  test-short     Run tests in short mode (skip slow tests)"
	@echo "  coverage       Generate HTML coverage report"
	@echo "  bench          Run benchmarks"
	@echo "  lint           Run golangci-lint"
	@echo "  check          Run fmt + vet + lint + test"
	@echo "  docker-build   Build Docker image"
	@echo "  docker-run     Start with docker-compose"
	@echo "  docker-stop    Stop docker-compose"
	@echo "  docker-test    Run full Docker integration tests (DNS + DHCP)"
	@echo "  docker-test-dns  Run DNS integration tests only"
	@echo "  docker-test-dhcp Run DHCP integration tests only"
	@echo "  deps           Download dependencies"
	@echo "  clean          Remove build artifacts"
