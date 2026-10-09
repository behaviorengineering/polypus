.PHONY: help build test vet lint tidy ci serve serve-down init mlx-sync

INTEGRATION_TEST := go test -tags=integration -count=1 -timeout 15m ./internal/smoke/integration

CONFIG_EXAMPLE_SRC := config.yaml.example
CONFIG_EXAMPLE_EMBED := internal/config/config.yaml.example

.DEFAULT_GOAL := help

include ports.env
export POLYPUS_HOST POLYPUS_PORT POLYPUS_MLX_HOST POLYPUS_MLX_PORT POLYPUS_SWITCHYARD_HOST POLYPUS_SWITCHYARD_PORT

PARENT_ROOT := $(abspath $(CURDIR)/..)
ifeq ($(wildcard $(PARENT_ROOT)/stack/.env.example),)
BINARY := bin/polypus
else
BINARY := $(PARENT_ROOT)/bin/polypus
endif

PARENT_MONOREPO_ROOT :=
ifneq ($(wildcard $(abspath $(CURDIR)/../..)/stack/.env.example),)
PARENT_MONOREPO_ROOT := $(abspath $(CURDIR)/../..)
endif
SMOKE_BIN := $(dir $(BINARY))polypus-smoke
POLYPUS_CHAT_SMOKE_MODEL ?= cf_local/@cf/ibm-granite/granite-4.0-h-micro
POLYPUS_ROUTER_SMOKE_MODEL ?= router/investigator
POLYPUS_BATCH_SMOKE_MODEL ?= cf_local/@cf/google/gemma-4-26b-a4b-it

IMAGE_REPO ?= xynova/polypus
IMAGE_TAG ?= latest
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/behaviorengineering/polypus/internal/buildinfo.Version=$(VERSION)
GO_BUILDFLAGS := -buildvcs=false

help: ## List local-dev make verbs
	@grep -E '^[a-zA-Z0-9_.-]+:.*?## ' $(firstword $(MAKEFILE_LIST)) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  %-22s %s\n", $$1, $$2}'
	@printf '\n  Integration smoke (subprocess gateway or Docker obs stack):\n'
	@printf '    1. make smoke\n'
	@printf '    2. make smoke-all\n'
	@printf '    3. POLYPUS_SMOKE_OTEL=1 make smoke-otel\n'
	@printf '  Long-running dev stack: make serve (process-compose; Phoenix, HyperDX, otelcol when Docker is up)\n'

build: ## Build gateway and switchyard-server binaries
	@$(MAKE) --no-print-directory build-gateway switchyard-build

serve: build ## process-compose TUI (gateway, backends, optional observability)
	chmod +x scripts/pc-up.sh scripts/pc-down.sh scripts/pc-gateway.sh scripts/pc-phoenix.sh scripts/pc-hyperdx.sh scripts/pc-otelcol.sh scripts/pc-switchyard.sh
	./scripts/pc-up.sh

serve-down: ## Stop this process-compose project only
	chmod +x scripts/pc-down.sh
	./scripts/pc-down.sh

init: build-gateway ## Write XDG config from example via polypus init
	$(BINARY) init

mlx-sync: ## uv sync for MLX backends
	chmod +x backends/mlx/scripts/sync.sh
	./backends/mlx/scripts/sync.sh

test: ## Unit tests (excludes integration tag)
	go test ./...

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint on cmd, internal, and pkg
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./cmd/... ./internal/... ./pkg/...

tidy: ## Run go mod tidy
	go mod tidy

ci: ## Module CI checks (tidy, gofmt, vet, race tests, build)
	@cp go.mod go.mod.bak && cp go.sum go.sum.bak
	go mod tidy
	@diff -u go.mod.bak go.mod && diff -u go.sum.bak go.sum
	@rm -f go.mod.bak go.sum.bak
	@$(MAKE) --no-print-directory check-config-example
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:" && gofmt -l . && exit 1)
	go vet ./...
	go test -race -count=1 ./...
	go build $(GO_BUILDFLAGS) ./...

sync-config-example:
	@cp $(CONFIG_EXAMPLE_SRC) $(CONFIG_EXAMPLE_EMBED)

check-config-example:
	@test -f $(CONFIG_EXAMPLE_EMBED) || (echo "missing $(CONFIG_EXAMPLE_EMBED); run: make sync-config-example" && exit 1)
	@cmp -s $(CONFIG_EXAMPLE_SRC) $(CONFIG_EXAMPLE_EMBED) || (echo "config example drift: edit $(CONFIG_EXAMPLE_SRC) then make sync-config-example" && exit 1)

build-gateway: sync-config-example
	@mkdir -p $(dir $(BINARY))
	go build $(GO_BUILDFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/polypus

build-smoke:
	@mkdir -p $(dir $(SMOKE_BIN))
	go build $(GO_BUILDFLAGS) -o $(SMOKE_BIN) ./cmd/polypus-smoke

install:
	go build $(GO_BUILDFLAGS) -ldflags "$(LDFLAGS)" -o $(shell go env GOPATH)/bin/polypus ./cmd/polypus

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

smoke:
	$(INTEGRATION_TEST) -run TestSmokeTTS

smoke-local:
	$(INTEGRATION_TEST) -run '^TestSmokeTTSLocal$$'

smoke-chat:
	$(INTEGRATION_TEST) -run TestSmokeChat

smoke-router:
	$(INTEGRATION_TEST) -run '^TestSmokeRouter$$'

smoke-landing:
	$(INTEGRATION_TEST) -run '^TestGatewayLanding$$'

smoke-batch:
	$(INTEGRATION_TEST) -run TestSmokeBatch

smoke-higgs:
	POLYPUS_SMOKE_OUT=/tmp/polypus-higgs-smoke.mp3 \
	$(INTEGRATION_TEST) -run '^TestSmokeHiggs$$'

smoke-stt:
	$(INTEGRATION_TEST) -run TestSmokeSTT

smoke-stt-local:
	$(INTEGRATION_TEST) -run '^TestSmokeSTTLocal$$'

smoke-systemone:
	$(INTEGRATION_TEST) -run TestSmokeSystemOne

smoke-all:
	$(INTEGRATION_TEST) -run TestSmokeAll

smoke-otel:
	POLYPUS_SMOKE_OTEL=1 $(INTEGRATION_TEST) -run TestSmokeOtelFanout

test-integration:
	$(INTEGRATION_TEST)

docker-build:
	docker build -t $(IMAGE_REPO):$(IMAGE_TAG) .
