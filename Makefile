.PHONY: help build build-gateway build-chat-smoke install test vet tidy ci mlx-sync serve serve-down smoke smoke-chat smoke-router smoke-higgs smoke-stt smoke-all switchyard-build docker-build

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
CF_ADAPTER_BIN :=
CHAT_SMOKE_BIN := $(dir $(BINARY))polypus-chat-smoke
POLYPUS_CHAT_SMOKE_MODEL ?= cf_local/@cf/google/gemma-4-26b-a4b-it
POLYPUS_ROUTER_SMOKE_MODEL ?= router/investigator

IMAGE_REPO ?= xynova/polypus
IMAGE_TAG ?= latest
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/behaviorengineering/polypus/internal/cli.version=$(VERSION)

help:
	@echo "polypus — local OpenAI speech gateway (TTS/STT backends behind loopback)"
	@echo ""
	@echo "  make build         Build $(BINARY)"
	@echo "  make mlx-sync      uv sync for backends/mlx"
	@echo "  make serve         process-compose TUI: gateway :$(POLYPUS_PORT) + backends + Phoenix :6006 (POLYPUS_PHOENIX=0 to skip)"
	@echo "  make serve-down    Stop this Polypus process-compose project only"
	@echo "  make smoke         curl TTS smoke test via gateway"
	@echo "  make smoke-chat    L1 chat transport smoke (polypus-chat-smoke)"
	@echo "  make smoke-router  Named router smoke (router/investigator by default)"
	@echo "  make smoke-higgs   Higgs v2 TTS smoke (narration alternative)"
	@echo "  make smoke-stt     TTS then STT round-trip via gateway"
	@echo "  make smoke-all     TTS + STT smoke"
	@echo "  make docker-build  Build $(IMAGE_REPO):$(IMAGE_TAG)"
	@echo "  make test          go test ./..."
	@echo "  make vet           go vet ./..."
	@echo "  make ci            tidy + gofmt + vet + race tests + build"

build: build-gateway build-chat-smoke

build-gateway:
	@mkdir -p $(dir $(BINARY))
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/polypus

build-chat-smoke:
	@mkdir -p $(dir $(CHAT_SMOKE_BIN))
	go build -o $(CHAT_SMOKE_BIN) ./cmd/polypus-chat-smoke

install:
	go build -ldflags "$(LDFLAGS)" -o $(shell go env GOPATH)/bin/polypus ./cmd/polypus

mlx-sync:
	chmod +x backends/mlx/scripts/sync.sh
	./backends/mlx/scripts/sync.sh

serve: build
	chmod +x scripts/pc-up.sh scripts/pc-down.sh scripts/pc-gateway.sh scripts/pc-phoenix.sh scripts/pc-switchyard.sh
	./scripts/pc-up.sh

serve-down:
	chmod +x scripts/pc-down.sh
	./scripts/pc-down.sh

smoke:
	chmod +x scripts/smoke.sh
	./scripts/smoke.sh

smoke-chat: build-chat-smoke
	$(CHAT_SMOKE_BIN) -model $(POLYPUS_CHAT_SMOKE_MODEL)

smoke-router: build-chat-smoke
	$(CHAT_SMOKE_BIN) -model $(POLYPUS_ROUTER_SMOKE_MODEL)

switchyard-build:
	@mkdir -p bin
	cargo install --locked --path providers/switchyard/crates/switchyard-server --root ./bin

smoke-higgs:
	chmod +x scripts/smoke.sh
	POLYPUS_DEFAULT_MODEL=mlx-community/higgs-audio-v2-3B-mlx-q6 \
	POLYPUS_DEFAULT_VOICE=vivian \
	POLYPUS_SMOKE_OUT=/tmp/polypus-higgs-smoke.mp3 \
	./scripts/smoke.sh

smoke-stt:
	chmod +x scripts/smoke-stt.sh
	./scripts/smoke-stt.sh

smoke-all: smoke smoke-stt

docker-build:
	docker build -t $(IMAGE_REPO):$(IMAGE_TAG) .

test:
	go test ./...

vet:
	go vet ./...

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
	go build ./...
