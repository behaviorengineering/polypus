# Polypus config reference

Paths relative to Polypus repo root unless noted.

## Ports

| Service | Default | Role |
|---------|---------|------|
| Gateway | `127.0.0.1:1320` | Public OpenAI `/v1/*`; `POLYPUS_BASE_URL` |
| MLX | `127.0.0.1:1322` | Local TTS/STT (Apple Silicon) |
| cf-adapter | `127.0.0.1:1323` | Cloudflare Workers AI shim |
| LM Studio | `127.0.0.1:1234` | External; chat, vision, embed |
| Phoenix UI | `127.0.0.1:6006` | Trace viewer |
| Phoenix OTLP | `127.0.0.1:4317` | gRPC collector |

## Environment (common)

```env
POLYPUS_HOST=127.0.0.1
POLYPUS_PORT=1320
POLYPUS_BASE_URL=http://127.0.0.1:1320
POLYPUS_MLX_HOST=127.0.0.1
POLYPUS_MLX_PORT=1322
POLYPUS_PHOENIX=1
POLYPUS_OTEL=1
INFERENCE_CLOUD_CASE=1   # Consilium stack/.env; starts cf-adapter
CF_AI_API_KEY=...
CF_ACCOUNT_ID=...
```

Speech defaults (MLX profile): `POLYPUS_DEFAULT_MODEL`, `POLYPUS_DEFAULT_STT_MODEL`, `POLYPUS_DEFAULT_VOICE`.

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
    base_url: http://127.0.0.1:1323
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

Client header `X-Polypus-Timeout` (duration or seconds) clamps to `timeouts.min`..`timeouts.max` (5s to 900s).

## Model ids

- Gateway rewrites ids as `backend_id/downstream-model`.
- Examples: `cf_local/@cf/google/gemma-4-26b-a4b-it`, `lm_studio/allenai/olmocr-2-7b`.
- No prefix → capability default backend applies.

## Inventory vs enabled

| HTTP | Returns |
|------|---------|
| `GET /v1/models` | **Enabled** only (`models.allow` when set) |
| `GET /v1/models?view=inventory` | Full synced upstream catalog |
| POST inference | 400 `model_not_allowed` if not in allow list |

Optional cache: `POLYPUS_MODELS_CACHE` or `.polypus/models-inventory.json`.

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

- `reject_non_loopback_backends: true` in case mode.
- `require_cloud_opt_in: true` for `cf_local` when cloud profile active.

Consilium `stack/ai.yaml` points at gateway only; backend table stays in Polypus.
