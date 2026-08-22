.PHONY: help build build-gateway build-chat-smoke install test vet tidy mlx-sync serve serve-down smoke smoke-chat smoke-higgs smoke-stt smoke-all docker-build

include ports.env
export POLYPUS_HOST POLYPUS_PORT POLYPUS_MLX_HOST POLYPUS_MLX_PORT

PARENT_ROOT := $(abspath $(CURDIR)/..)
ifeq ($(wildcard $(PARENT_ROOT)/stack/.env.example),)
BINARY := bin/polypus
else
BINARY := $(PARENT_ROOT)/bin/polypus
endif

# Nested as providers/polypus: Consilium is two levels up (adapter lives in engine/).
CONSILIUM_ROOT :=
ifneq ($(wildcard $(abspath $(CURDIR)/../..)/stack/.env.example),)
CONSILIUM_ROOT := $(abspath $(CURDIR)/../..)
endif
CF_ADAPTER_BIN :=
CHAT_SMOKE_BIN := $(dir $(BINARY))polypus-chat-smoke
POLYPUS_CHAT_SMOKE_MODEL ?= cf_local/@cf/google/gemma-4-26b-a4b-it

IMAGE_REPO ?= xynova/polypus
IMAGE_TAG ?= latest

help:
	@echo "polypus — local OpenAI speech gateway (TTS/STT backends behind loopback)"
	@echo ""
	@echo "  make build         Build $(BINARY)"
	@echo "  make mlx-sync      uv sync for backends/mlx"
	@echo "  make serve         process-compose TUI: gateway :$(POLYPUS_PORT) + backends + Phoenix :6006 (POLYPUS_PHOENIX=0 to skip)"
	@echo "  make serve-down    Stop this Polypus process-compose project only"
	@echo "  make smoke         curl TTS smoke test via gateway"
	@echo "  make smoke-chat    L1 chat transport smoke (polypus-chat-smoke)"
	@echo "  make smoke-higgs   Higgs v2 TTS smoke (narration alternative)"
	@echo "  make smoke-stt     TTS then STT round-trip via gateway"
	@echo "  make smoke-all     TTS + STT smoke"
	@echo "  make docker-build  Build $(IMAGE_REPO):$(IMAGE_TAG)"
	@echo "  make test          go test ./..."
	@echo "  make vet           go vet ./..."

build: build-gateway build-chat-smoke

build-gateway:
	@mkdir -p $(dir $(BINARY))
	go build -o $(BINARY) ./cmd/polypus

build-chat-smoke:
	@mkdir -p $(dir $(CHAT_SMOKE_BIN))
	go build -o $(CHAT_SMOKE_BIN) ./cmd/polypus-chat-smoke

install:
	go build -o $(shell go env GOPATH)/bin/polypus ./cmd/polypus

mlx-sync:
	chmod +x backends/mlx/scripts/sync.sh
	./backends/mlx/scripts/sync.sh

serve: build
	chmod +x scripts/pc-up.sh scripts/pc-down.sh scripts/pc-gateway.sh scripts/pc-phoenix.sh
	./scripts/pc-up.sh

serve-down:
	chmod +x scripts/pc-down.sh
	./scripts/pc-down.sh

smoke:
	chmod +x scripts/smoke.sh
	./scripts/smoke.sh

smoke-chat: build-chat-smoke
	$(CHAT_SMOKE_BIN) -model $(POLYPUS_CHAT_SMOKE_MODEL)

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
