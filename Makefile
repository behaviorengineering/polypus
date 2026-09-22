.PHONY: help build build-gateway build-smoke install test vet lint tidy ci mlx-sync serve serve-down smoke smoke-local smoke-chat smoke-router smoke-higgs smoke-stt smoke-stt-local smoke-systemone smoke-all switchyard-build docker-build

.DEFAULT_GOAL := help

include ports.env
export POLYPUS_HOST POLYPUS_PORT POLYPUS_MLX_HOST POLYPUS_MLX_PORT POLYPUS_SWITCHYARD_HOST POLYPUS_SWITCHYARD_PORT

PARENT_ROOT := $(abspath $(CURDIR)/..)
ifeq ($(wildcard $(PARENT_ROOT)/stack/.env.example),)
BINARY := bin/polypus
else
BINARY := $(PARENT_ROOT)/bin/polypus
endif

# Nested as providers/polypus: optional parent monorepo is two levels up.
PARENT_MONOREPO_ROOT :=
ifneq ($(wildcard $(abspath $(CURDIR)/../..)/stack/.env.example),)
PARENT_MONOREPO_ROOT := $(abspath $(CURDIR)/../..)
endif
SMOKE_BIN := $(dir $(BINARY))polypus-smoke
POLYPUS_CHAT_SMOKE_MODEL ?= cf_local/@cf/google/gemma-4-26b-a4b-it
POLYPUS_ROUTER_SMOKE_MODEL ?= router/investigator

IMAGE_REPO ?= xynova/polypus
IMAGE_TAG ?= latest
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/behaviorengineering/polypus/internal/cli.version=$(VERSION)
# Nested submodule checkouts (and broken gitdirs) make `go build` VCS stamping fail
# with exit 128; version is already injected via LDFLAGS.
GO_BUILDFLAGS := -buildvcs=false

help:
	@echo "polypus — local OpenAI speech gateway (TTS/STT backends behind loopback)"
	@echo ""
	@echo "  make build              Build $(BINARY) + $(SMOKE_BIN) + bin/switchyard-server"
	@echo "  make build-gateway      Build the polypus gateway binary"
	@echo "  make build-smoke        Build polypus-smoke (multi-channel L1 probes)"
	@echo "  make switchyard-build   Build bin/switchyard-server (Rust; also part of make build)"
	@echo "  make install            Install polypus into GOPATH/bin"
	@echo "  make mlx-sync           uv sync for backends/mlx"
	@echo "  make serve              process-compose TUI: gateway :$(POLYPUS_PORT) + backends + Phoenix :6006 + HyperDX :8080 (POLYPUS_PHOENIX=0 / POLYPUS_HYPERDX=0 to skip)"
	@echo "  make serve-down         Stop this Polypus process-compose project only"
	@echo "  make smoke              TTS smoke via gateway (cf_local default)"
	@echo "  make smoke-local        TTS smoke via MLX"
	@echo "  make smoke-chat         L1 chat smoke via polypus-smoke (cheap gemma)"
	@echo "  make smoke-router       Named router chat smoke (router/investigator by default)"
	@echo "  make smoke-higgs        Higgs v2 TTS smoke (MLX)"
	@echo "  make smoke-stt          TTS then STT round-trip (cf_local)"
	@echo "  make smoke-stt-local    TTS+STT via MLX"
	@echo "  make smoke-systemone    TypeSafe /v1/systemone (skips without CF_AI_API_KEY)"
	@echo "  make smoke-all          chat + TTS + STT + systemone via polypus-smoke (gateway must be up)"
	@echo "  make docker-build       Build $(IMAGE_REPO):$(IMAGE_TAG)"
	@echo "  make test               go test ./..."
	@echo "  make vet                go vet ./..."
	@echo "  make lint               golangci-lint on ./cmd/... ./internal/... ./pkg/..."
	@echo "  make tidy               go mod tidy"
	@echo "  make ci                 tidy check + gofmt + vet + race tests + build"

build: build-gateway build-smoke switchyard-build

build-gateway:
	@mkdir -p $(dir $(BINARY))
	go build $(GO_BUILDFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/polypus

build-smoke:
	@mkdir -p $(dir $(SMOKE_BIN))
	go build $(GO_BUILDFLAGS) -o $(SMOKE_BIN) ./cmd/polypus-smoke

install:
	go build $(GO_BUILDFLAGS) -ldflags "$(LDFLAGS)" -o $(shell go env GOPATH)/bin/polypus ./cmd/polypus

mlx-sync:
	chmod +x backends/mlx/scripts/sync.sh
	./backends/mlx/scripts/sync.sh

serve: build
	chmod +x scripts/pc-up.sh scripts/pc-down.sh scripts/pc-gateway.sh scripts/pc-phoenix.sh scripts/pc-hyperdx.sh scripts/pc-switchyard.sh
	./scripts/pc-up.sh

serve-down:
	chmod +x scripts/pc-down.sh
	./scripts/pc-down.sh

smoke:
	chmod +x scripts/smoke.sh
	./scripts/smoke.sh

smoke-local:
	chmod +x scripts/smoke.sh
	POLYPUS_SMOKE_LOCAL=1 ./scripts/smoke.sh

smoke-chat: build-smoke
	$(SMOKE_BIN) -channels chat -chat-model $(POLYPUS_CHAT_SMOKE_MODEL)

smoke-router: build-smoke
	$(SMOKE_BIN) -channels chat -chat-model $(POLYPUS_ROUTER_SMOKE_MODEL)

switchyard-build:
	@test -f providers/switchyard/Cargo.toml || ( \
		echo "switchyard-build: missing providers/switchyard (run: git submodule update --init providers/switchyard)" >&2; \
		exit 1)
	@command -v cargo >/dev/null 2>&1 || ( \
		echo "switchyard-build: cargo not found; install a Rust toolchain" >&2; \
		exit 1)
	@mkdir -p bin
	cargo install --locked --force --path providers/switchyard/crates/switchyard-server --root .
	@test -x bin/switchyard-server || ( \
		echo "switchyard-build: expected executable bin/switchyard-server after cargo install" >&2; \
		exit 1)

smoke-higgs:
	chmod +x scripts/smoke.sh
	POLYPUS_SMOKE_LOCAL=1 \
	POLYPUS_DEFAULT_MODEL=mlx-community/higgs-audio-v2-3B-mlx-q6 \
	POLYPUS_DEFAULT_VOICE=vivian \
	POLYPUS_SMOKE_OUT=/tmp/polypus-higgs-smoke.mp3 \
	./scripts/smoke.sh

smoke-stt:
	chmod +x scripts/smoke-stt.sh
	./scripts/smoke-stt.sh

smoke-stt-local:
	chmod +x scripts/smoke-stt.sh
	POLYPUS_SMOKE_LOCAL=1 ./scripts/smoke-stt.sh

smoke-systemone: build-smoke
	$(SMOKE_BIN) -channels systemone

smoke-all: build-smoke
	$(SMOKE_BIN) -channels all

docker-build:
	docker build -t $(IMAGE_REPO):$(IMAGE_TAG) .

test:
	go test ./...

vet:
	go vet ./...

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./cmd/... ./internal/... ./pkg/...

tidy:
	go mod tidy

ci:
	@cp go.mod go.mod.bak && cp go.sum go.sum.bak
	go mod tidy
	@diff -u go.mod.bak go.mod && diff -u go.sum.bak go.sum
	@rm -f go.mod.bak go.sum.bak
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:" && gofmt -l . && exit 1)
	go vet ./...
	go test -race -count=1 ./...
	go build $(GO_BUILDFLAGS) ./...
