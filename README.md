# Polypus

Local **OpenAI-compatible speech gateway**: TTS and STT on loopback, with alien engines (MLX-Audio first) behind a stable Go API.

**Consilium integration:** submodule at `providers/polypus/` in [cr-case-intake](https://github.com/xynova/cr-case-intake); `make polypus-serve` from repo root.

## Quick start (Apple Silicon)

```bash
make mlx-sync
make serve        # gateway :1320 + MLX backend :1322
make smoke        # → /tmp/polypus-smoke.mp3
make smoke-stt    # TTS then STT round-trip
```

## Layout

```text
polypus/
  cmd/polypus/           # Go gateway binary
  internal/gateway/     # HTTP surface (/health, /v1/models, chat/embed/audio)
  internal/router/      # Bifrost SDK: proxies speech/STT to backends; registry + policy
  config.yaml.example   # optional multi-backend table (A7)
  backends/mlx/         # uv + mlx-audio (host only)
  scripts/serve-all.sh  # start backend + gateway
```

## OpenAI surface

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/health` | Liveness + backend summary |
| `GET` | `/v1/models` | Aggregate catalog (OpenAI Models API) |
| `GET` | `/v1/models/{id}` | Retrieve one model |
| `POST` | `/v1/chat/completions` | Chat + vision |
| `POST` | `/v1/embeddings` | Embeddings |
| `POST` | `/v1/audio/speech` | TTS |
| `POST` | `/v1/audio/transcriptions` | STT |
| `GET` | `/v1/audio/voices` | Voice list |

Model ids from backends are rewritten as `backend_id/downstream-model` so tools can pass them straight into chat/embed/speech.

**Inventory vs enabled**

| Call | Meaning |
|------|---------|
| `GET /v1/models` | **Enabled** only (`models.allow` on each backend when set) |
| `GET /v1/models?view=inventory` | Full synced inventory (all upstream models) |
| POST chat/embed/speech | Rejected with `model_not_allowed` if allow is set and the model is not listed |

Cloudflare models appear through **cf-adapter** (`:1323`), which maps Workers AI **Model Search** into OpenAI `/v1/models`. Polypus never calls Cloudflare directly.

```bash
curl -sS http://127.0.0.1:1320/v1/models | jq .
curl -sS 'http://127.0.0.1:1320/v1/models?view=inventory' | jq .
curl -sS http://127.0.0.1:1323/v1/models | jq .   # raw CF catalogue via adapter
```

Backends without `/v1/models` still seed `POLYPUS_DEFAULT_MODEL` / `POLYPUS_DEFAULT_STT_MODEL` (when allowed). Optional disk cache: `POLYPUS_MODELS_CACHE` / `POLYPUS_ROOT/.polypus/models-inventory.json`.

## Ports

| Service | Default | Notes |
|---------|---------|-------|
| Gateway | `127.0.0.1:1320` | Public OpenAI `/v1/*` surface |
| MLX backend | `127.0.0.1:1322` | Internal; not for Consilium callers |
| CF adapter | `127.0.0.1:1323` | Cloudflare Model Search + inference |

## Environment

```env
POLYPUS_HOST=127.0.0.1
POLYPUS_PORT=1320
POLYPUS_BASE_URL=http://127.0.0.1:1320
POLYPUS_MLX_HOST=127.0.0.1
POLYPUS_MLX_PORT=1322
POLYPUS_DEFAULT_MODEL=mlx-community/Qwen3-TTS-12Hz-1.7B-CustomVoice-bf16
POLYPUS_DEFAULT_STT_MODEL=mlx-community/whisper-large-v3-turbo-asr-fp16
POLYPUS_DEFAULT_VOICE=vivian
```

From Consilium repo root use `make polypus-sync`, `make polypus-serve`, or `make -C providers/polypus …`.

Multi-backend routing: copy `config.yaml.example` → `config.yaml` for extra loopback backends. Model prefix: `backend_id/model` (e.g. `alt_stt/whisper-large-v3`).

## Docker

Gateway image only (MLX requires Apple Silicon on the host):

```bash
make mlx-serve          # host terminal 1
docker compose up       # gateway → host.docker.internal:1322
```

## MLX known issues

See Consilium `docs/planned/core-infrastructure/local-tts.md` § Known stack issues (`serve_launcher.py`, transformers pin).

## License

Part of the Xynova Consilium ecosystem. Breach model: case narration never leaves localhost.
