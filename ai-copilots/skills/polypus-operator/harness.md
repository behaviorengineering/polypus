# Polypus model harness

Polypus ships **L1 transport smoke** in this repo. Deeper L2/L3 XML and job-fixture matrices may live in a **host application** that calls `POLYPUS_BASE_URL`; this pack documents Polypus-native probes only.

## Prerequisites

- Default L1 smokes: **no** running gateway (`make smoke-*` builds and starts one with mock backends).
- Live stack checks against your config: `make serve` plus optional `make build-smoke` and `bin/polypus-smoke -base-url http://127.0.0.1:1320`.

## L1 smoke (this repo)

```bash
make smoke-all
go test -tags=integration -count=1 ./internal/smoke/integration
make smoke-chat
POLYPUS_CHAT_SMOKE_MODEL='cf_local/@cf/zai-org/glm-4.7-flash' make smoke-chat
make smoke-router
POLYPUS_ROUTER_SMOKE_MODEL='router/investigator' make smoke-router
make smoke-batch
POLYPUS_BATCH_SMOKE_MODEL='cf_local/@cf/google/gemma-4-26b-a4b-it' make smoke-batch
make smoke
make smoke-stt
make smoke-local
make smoke-stt-local
make smoke-higgs
make smoke-systemone
```

Integration: `internal/smoke/integration` (`-tags=integration`). Hermetic mocks: Cloudflare (default channels), MLX speech (`smoke-local` / `smoke-stt-local` / `smoke-higgs`), Switchyard (`smoke-router`). Optional CLI against a running gateway: `bin/polypus-smoke` (`make build-smoke`). Public API: `pkg/polypus.Smoke`.

Default chat model: `cf_local/@cf/ibm-granite/granite-4.0-h-micro` (override with `POLYPUS_CHAT_SMOKE_MODEL`; use GLM or Gemma for reasoning probes).

Default router model: `router/investigator` (override with `POLYPUS_ROUTER_SMOKE_MODEL`). Integration smoke uses mock Switchyard; production routers need `routers:` in config and `make serve`.

Default batch model: `cf_local/@cf/google/gemma-4-26b-a4b-it` (override with `POLYPUS_BATCH_SMOKE_MODEL`). Opt-in only (`make smoke-batch`); not in `smoke-all` / default live CI.

Default audio models: cf_local Deepgram (`aura-2-en` / `nova-3`). MLX integration smokes use Qwen3 TTS / whisper STT model ids (see `internal/smoke/integration/models.go`). Systemone: `cf_local/typesafe/jev`.

## Tier summary (when host provides harness)

| Tier | Typical probes | Purpose |
|------|----------------|---------|
| **L1** | ping, content_nonempty, thinking_policy | Transport and thinking defaults |
| **L2** | minimal_xml, directives_ack, list_field | Structured XML output |
| **L3** | job-specific fixtures | End-to-end slices per downstream job |

## When to run which tier

| User report | Start with |
|-------------|------------|
| Gateway down / connection errors | Health + `make smoke-chat` |
| Named router / `router/…` fails | Health + `/health/backends` (switchyard) + `make smoke-router` |
| Batch files / batches facade fails | `batch_backend` + allow-list + `make smoke-batch` |
| Live CI batch | Default main live integration skips batch; set `POLYPUS_SMOKE_BATCH=1` to include it |
| Empty content / parse errors | L1 + host L2 if available |
| Specific downstream job fails | Host L3 for that job's model |
| New model on allow-list | L1 on that model, then L2 if used for XML |

## Manifest

Example tier config may ship with your host as `models.harness.yaml`. Auto-discovery can read Polypus `config.yaml` `models.allow`.
