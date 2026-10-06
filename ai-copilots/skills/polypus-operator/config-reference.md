# Polypus config reference

## Config path

| Priority | Path |
|----------|------|
| 1 | `POLYPUS_CONFIG` (explicit) |
| 2 | `~/.config/polypus/config.yaml` (`$XDG_CONFIG_HOME/polypus/config.yaml` when set) |
| 3 | `$POLYPUS_ROOT/config.yaml` or cwd `config.yaml` (dev fallback) |

Bootstrap: `polypus init` or `make init` (writes `~/.config/polypus/config.yaml` at mode `0600` when missing). Declare `secrets:` in config (env var names only); Polypus loads those from the OS keyring when unset. `polypus secret set` stores a value only if that name is already in `secrets:`. Env and `.env` still win at runtime. Omit `secrets:` for local-only stacks to avoid keyring access. Docker Compose deploy (`config.deploy.yaml.example`) omits `secrets:`; inject `CF_*` via compose env (runner `export-env` / SOPS), not the container keyring.

CI: PRs run hermetic `go test -tags=integration` (temp config from `internal/smoke/integration`). Push to **main** runs live integration with `POLYPUS_SMOKE_LIVE=1` and `CF_AI_API_KEY` / `CF_ACCOUNT_ID` secrets.

## Ports

| Service | Default | Role |
|---------|---------|------|
| Gateway | `127.0.0.1:1320` | Public OpenAI `/v1/*`; `POLYPUS_BASE_URL` |
| Switchyard | `127.0.0.1:4000` | Composed named routers (`stage_router`) |
| MLX | `127.0.0.1:1322` | Local TTS/STT (Apple Silicon) |
| LM Studio | `127.0.0.1:1234` | External; chat, vision, embed |
| Phoenix UI | `127.0.0.1:6006` | Trace viewer |
| Phoenix OTLP | `127.0.0.1:4317` | gRPC collector |

Cloudflare (`cf_local`) has no separate port; it runs in-process when configured with CF credentials. CF TTS/STT enter Bifrost; a PreLLMHook plugin short-circuits them onto `/ai/run` (Workers AI has no `/ai/v1/audio/*`).

## Environment (common)

```env
POLYPUS_HOST=127.0.0.1
POLYPUS_PORT=1320
POLYPUS_BASE_URL=http://127.0.0.1:1320   # client URL; also Switchyard leaf callback when set (Compose: http://gateway:1320)   # client URL; also Switchyard leaf callback when set (Compose: http://gateway:1320)
POLYPUS_MLX_HOST=127.0.0.1
POLYPUS_MLX_PORT=1322
POLYPUS_PHOENIX=1
POLYPUS_OTEL=1
POLYPUS_SWITCHYARD=1          # 0 skips Switchyard process in make serve
POLYPUS_SWITCHYARD_BASE_URL=  # override Switchyard probe/render target (tests/ops)
POLYPUS_SWITCHYARD_CONFIG=    # override generated routes.toml path
CF_AI_API_KEY=...
CF_ACCOUNT_ID=...
```

Speech smoke: cf_local via `make smoke` / `make smoke-stt`; MLX paths via `make smoke-local` / `make smoke-stt-local` / `make smoke-higgs` (hermetic mock MLX in integration tests). Override with `POLYPUS_DEFAULT_MODEL`, `POLYPUS_DEFAULT_STT_MODEL`, `POLYPUS_DEFAULT_VOICE`, or `POLYPUS_SMOKE_OUT` for TTS file output.

## config.yaml structure

```yaml
secrets:
  - CF_AI_API_KEY
  - CF_ACCOUNT_ID
chat_backend:
  enabled: true
  default: cf_local
vision_backend:
  enabled: true
  default: cf_local
embed_backend:
  enabled: true
  default: lm_studio
tts_backend:
  enabled: true
  default: cf_local
stt_backend:
  enabled: true
  default: cf_local
proxy_backend:
  enabled: true
  default: cf_local
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
    capabilities: [chat, vision, tts, stt, voices, systemone]
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

Capability defaults use `*_backend` blocks (`enabled` + `default`): `chat_backend`, `vision_backend`, `embed_backend`, `tts_backend`, `stt_backend`, `proxy_backend`, `systemone_backend`, `batch_backend`. Set `enabled: false` (or omit) to skip a capability. Remote backends (`remote: true`) load when listed in config and their `auth.bearer_env` is set in the environment (possibly filled from the OS keyring for names in `secrets:`). `proxy_backend` covers voices and may inherit `tts_backend.default` when enabled with an empty default. `systemone_backend` fronts `POST /v1/systemone` (TypeSafe/Decider wire; Cloudflare `typesafe/jev` via `/ai/run`). `batch_backend` fronts OpenAI `/v1/files` and `/v1/batches` mapped to Cloudflare Workers AI async batch (`batch` capability on `extension: cloudflare` backends; JSONL under `POLYPUS_BATCH_DIR` or `~/.local/state/polypus/batch`). `POST /v1/batches/{id}/cancel` returns HTTP 501 because Workers AI has no cancel API.

Client header `X-Polypus-Timeout` (duration or seconds) clamps to `timeouts.min`..`timeouts.max` (5s to 900s).

## Model ids

- Gateway rewrites ids as `backend_id/downstream-model`.
- Examples: `cf_local/@cf/google/gemma-4-26b-a4b-it`, `lm_studio/allenai/olmocr-2-7b`, `cf_local/typesafe/jev`.
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
| `POST /v1/admin/models/allow` | Add one model to `models.allow` when it exists in inventory (no separate allow key; optional gateway access key when store is non-empty) |
| `GET /` | Landing hub with allow form (backend + model) posting to `/v1/admin/models/allow` |

Optional cache: `POLYPUS_MODELS_CACHE` or `~/.cache/polypus/models-inventory.json`.

### Runtime allow overlay and gateway access keys

| Env | Default path | Role |
|-----|----------------|------|
| `POLYPUS_MODELS_ALLOW_OVERLAY` | `~/.local/state/polypus/models-allow-overlay.yaml` | Extra `models.allow` entries (additive; not in git config) |
| `POLYPUS_ADMIN_KEYS` | `~/.local/state/polypus/admin-api-keys.json` | Hashed gateway access keys (`polypus admin-key` CLI) |

`POST /v1/admin/models/allow` body: `{"backend":"cf_local","model":"@cf/..."}`. The model must appear in backend inventory; the backend must have `models.allow` configured. No dedicated allow Bearer.

When `admin-api-keys.json` contains at least one key, all routes except `GET|HEAD /health`, `/health/backends`, and `/health/upstreams` require a gateway access key: `Authorization: Bearer ppk.<id>.<secret>`, `X-Api-Key`, or HTTP Basic (password = the full `ppk...` token). Plaintext keys print once on `polypus admin-key generate` or `rotate`.

Overlay POST and key-file changes take effect without restarting `polypus serve`. YAML `backends`, `routers:`, and secrets still require a gateway restart.

## Data directories (XDG)

| Path | Role |
|------|------|
| `~/.config/polypus/config.yaml` | Router config |
| `~/.cache/polypus/models-inventory.json` | Model inventory cache |
| `~/.cache/polypus/switchyard/routes.toml` | Generated Switchyard routes (from `routers:`) |
| `~/.local/state/polypus/models-allow-overlay.yaml` | Runtime allow overlay |
| `~/.local/state/polypus/admin-api-keys.json` | Gateway access key hashes |
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
```

When `reject_non_loopback_backends: false`, local backends may use LAN URLs; direct OpenAI/Anthropic hosts remain blocked.

Host applications point `POLYPUS_BASE_URL` at the gateway only; backend tables stay in `~/.config/polypus/config.yaml`.
