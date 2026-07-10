.PHONY: help build install test vet tidy mlx-sync mlx-serve serve smoke smoke-higgs smoke-stt smoke-all docker-build

include ports.env
export POLYPUS_HOST POLYPUS_PORT POLYPUS_MLX_HOST POLYPUS_MLX_PORT

PARENT_ROOT := $(abspath $(CURDIR)/..)
ifeq ($(wildcard $(PARENT_ROOT)/stack/.env.example),)
BINARY := bin/polypus
else
BINARY := $(PARENT_ROOT)/bin/polypus
endif

IMAGE_REPO ?= xynova/polypus
IMAGE_TAG ?= latest

help:
	@echo "polypus — local OpenAI speech gateway (TTS/STT backends behind loopback)"
	@echo ""
	@echo "  make build         Build $(BINARY)"
	@echo "  make mlx-sync      uv sync for backends/mlx"
	@echo "  make serve         Gateway :$(POLYPUS_PORT) + MLX backend :$(POLYPUS_MLX_PORT)"
	@echo "  make mlx-serve     MLX backend only (debug)"
	@echo "  make smoke         curl TTS smoke test via gateway"
	@echo "  make smoke-higgs   Higgs v2 TTS smoke (narration alternative)"
	@echo "  make smoke-stt     TTS then STT round-trip via gateway"
	@echo "  make smoke-all     TTS + STT smoke"
	@echo "  make docker-build  Build $(IMAGE_REPO):$(IMAGE_TAG)"
	@echo "  make test          go test ./..."
	@echo "  make vet           go vet ./..."

build:
	@mkdir -p $(dir $(BINARY))
	go build -o $(BINARY) ./cmd/polypus

install:
	go build -o $(shell go env GOPATH)/bin/polypus ./cmd/polypus

mlx-sync:
	chmod +x backends/mlx/scripts/sync.sh
	./backends/mlx/scripts/sync.sh

mlx-serve:
	chmod +x backends/mlx/scripts/serve.sh
	./backends/mlx/scripts/serve.sh

serve: build
	chmod +x scripts/serve-all.sh
	./scripts/serve-all.sh

smoke:
	chmod +x scripts/smoke.sh
	./scripts/smoke.sh

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
