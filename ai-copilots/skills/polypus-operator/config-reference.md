# Polypus config reference

## Config path

| Priority | Path |
|----------|------|
| 1 | `POLYPUS_CONFIG` (explicit) |
| 2 | `~/.config/polypus/config.yaml` (`$XDG_CONFIG_HOME/polypus/config.yaml` when set) |
| 3 | `$POLYPUS_ROOT/config.yaml` or cwd `config.yaml` (dev fallback) |

Bootstrap: `cp config.yaml.example ~/.config/polypus/config.yaml`

## Ports

| Service | Default | Role |
|---------|---------|------|
| Gateway | `127.0.0.1:1320` | Public OpenAI `/v1/*`; `POLYPUS_BASE_URL` |
| Switchyard | `127.0.0.1:4000` | Composed named routers (`stage_router`) |
| MLX | `127.0.0.1:1322` | Local TTS/STT (Apple Silicon) |
| LM Studio | `127.0.0.1:1234` | External; chat, vision, embed |
| Phoenix UI | `127.0.0.1:6006` | Trace viewer |
| Phoenix OTLP | `127.0.0.1:4317` | gRPC collector |

Cloudflare (`cf_local`) has no separate port; it runs in-process when `INFERENCE_CLOUD_CASE=1`. CF TTS/STT enter Bifrost; a PreLLMHook plugin short-circuits them onto `/ai/run` (Workers AI has no `/ai/v1/audio/*`).

## Environment (common)

```env
POLYPUS_HOST=127.0.0.1
POLYPUS_PORT=1320
POLYPUS_BASE_URL=http://127.0.0.1:1320
POLYPUS_MLX_HOST=127.0.0.1
POLYPUS_MLX_PORT=1322
POLYPUS_PHOENIX=1
POLYPUS_OTEL=1
POLYPUS_SWITCHYARD=1          # 0 skips Switchyard process in make serve
POLYPUS_SWITCHYARD_BASE_URL=  # override Switchyard probe/render target (tests/ops)
POLYPUS_SWITCHYARD_CONFIG=    # override generated routes.toml path
INFERENCE_CLOUD_CASE=1   # enables cf_local remote backend (set in host environment)
CF_AI_API_KEY=...
CF_ACCOUNT_ID=...
```

Speech smoke defaults to cf_local (`make smoke` / `make smoke-stt`). MLX: `POLYPUS_SMOKE_LOCAL=1` or `make smoke-local` / `make smoke-stt-local`. Override with `POLYPUS_DEFAULT_MODEL`, `POLYPUS_DEFAULT_STT_MODEL`, `POLYPUS_DEFAULT_VOICE` (or `POLYPUS_CF_*` for cloud ids).

## config.yaml structure

```yaml
default_chat_backend: cf_local
default_vision_backend: cf_local
default_embed_backend: lm_studio
default_tts_backend: cf_local
default_stt_backend: cf_local

timeouts:
  min: 5s
  max: 900s
  chat: 120s
  chat_thinking: 600s
  vision: 300s
  embed: 60s
  speech: 180s
  backends:
    cf_local:
      chat: 60s

backends:
  cf_local:
    remote: true
    extension: cloudflare
    base_url: https://api.cloudflare.com/client/v4/accounts/${CF_ACCOUNT_ID}/ai/v1
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [chat, vision, tts, stt, voices]
    models:
      sync: true
      allow: [...]
  lm_studio:
    base_url: http://127.0.0.1:1234/v1
    capabilities: [chat, vision, embed]
    models:
      sync: true
      allow: [...]
```

When `INFERENCE_CLOUD_CASE` is unset, remote backends are stripped at load time so local-only dev still works.

Client header `X-Polypus-Timeout` (duration or seconds) clamps to `timeouts.min`..`timeouts.max` (5s to 900s).

## Model ids

- Gateway rewrites ids as `backend_id/downstream-model`.
- Examples: `cf_local/@cf/google/gemma-4-26b-a4b-it`, `lm_studio/allenai/olmocr-2-7b`.
- Named routers: `router/<name>` (e.g. `router/investigator`, `router/scribe`).
- No prefix → capability default backend applies.

## Named routers

Configure under `routers:` in `config.yaml`. Public model id is always `router/<yaml-key>`.

| Route type | Handler | Switchyard TOML |
|------------|---------|-----------------|
| `passthrough` | Polypus leaf proxy | omitted |
| `stage_router` | HTTP to Switchyard `:4000` | emitted |
| `llm_classifier` (`mode: custom`) | HTTP to Switchyard `:4000` | emitted |

Generated Switchyard config: `~/.cache/polypus/switchyard/routes.toml` (override with `switchyard.config_path`). Regenerated at gateway startup and by `polypus switchyard-render`. See [docs/switchyard/llm-classifier-custom.md](../../../docs/switchyard/llm-classifier-custom.md) for custom classifier fields.

```yaml
switchyard:
  base_url: http://127.0.0.1:4000

routers:
  investigator:
    capability: chat
    route:
      type: stage_router
      picker: efficient_first
      confidence_threshold: 0.5
      capable: cf_local/@cf/zai-org/glm-4.7-flash
      efficient: lm_studio/qwen2.5-7b
  scribe:
    capability: chat
    route:
      type: passthrough
      target: cf_local/@cf/google/gemma-4-26b-a4b-it
```

Backend id `router` is reserved. Composed routers return **503** when Switchyard is down (no fallback).

Build Switchyard: `make switchyard-build` (Rust toolchain per `providers/switchyard/rust-toolchain.toml`).

## Inventory vs enabled

| HTTP | Returns |
|------|---------|
| `GET /v1/models` | **Enabled** only (`models.allow` when set) |
| `GET /v1/models?view=inventory` | Full synced upstream catalog |
| POST inference | 400 `model_not_allowed` if not in allow list |

Optional cache: `POLYPUS_MODELS_CACHE` or `~/.cache/polypus/models-inventory.json`.

## Data directories (XDG)

| Path | Role |
|------|------|
| `~/.config/polypus/config.yaml` | Router config |
| `~/.cache/polypus/models-inventory.json` | Model inventory cache |
| `~/.cache/polypus/switchyard/routes.toml` | Generated Switchyard routes (from `routers:`) |
| `~/.local/state/polypus/process-compose.sock` | process-compose control socket |

## Capabilities routing

| Capability | Route | Typical backend |
|------------|-------|-----------------|
| chat | `POST /v1/chat/completions` | `cf_local`, `lm_studio` |
| vision | `POST /v1/chat/completions` (images) | `cf_local`, `lm_studio` |
| embed | `POST /v1/embeddings` | `lm_studio` |
| tts | `POST /v1/audio/speech` | `mlx_local`, `cf_local` |
| stt | `POST /v1/audio/transcriptions` | `mlx_local`, `cf_local` |

Router picks: (1) model prefix `backend_id/...`, else (2) `default_*_backend`.

## Policy

Optional `policy:` block (defaults shown):

```yaml
policy:
  reject_non_loopback_backends: true   # local backends must bind loopback
  require_cloud_opt_in: true           # remote backends need INFERENCE_CLOUD_CASE=1
```

When `require_cloud_opt_in: false`, remote backends stay loaded without `INFERENCE_CLOUD_CASE=1` (non-case / dev profiles only). When `reject_non_loopback_backends: false`, local backends may use LAN URLs; direct OpenAI/Anthropic hosts remain blocked.

Host applications point `POLYPUS_BASE_URL` at the gateway only; backend tables stay in `~/.config/polypus/config.yaml`.
