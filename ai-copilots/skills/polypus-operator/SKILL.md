---
name: polypus-operator
description: >-
  Operates Polypus inference gateway: health, config, allow-list, smoke tests,
  model harness, thinking policy, Phoenix traces. Use when Polypus is down,
  models are rejected, chat returns empty content, or downstream inference jobs
  fail. Not for case intake or timeline.
---

# Polypus operator

**Moral:** Diagnose with probes, then fix config or supervision. HTTP clients call only `:1320`.

## Start every task

1. Confirm workspace: Polypus repo root (or this tree nested under `providers/polypus/` in a parent monorepo).
2. For homelab Compose deploy: clone **gitlab.com/xynova/polypus-local** and load its `ai-copilots/`; wire **operatorconfig** via `go list -m -f '{{.Dir}}' github.com/behaviorengineering/operatorconfig` and that module's `ai-copilots/BOOTSTRAP.md` (do not copy operatorconfig skill bodies here).
3. Load shard when needed: [config-reference.md](config-reference.md), [troubleshooting.md](troubleshooting.md), [harness.md](harness.md), [thinking-policy.md](thinking-policy.md), [windows-gitlab-deploy.md](windows-gitlab-deploy.md) (pointer to polypus-local + keyring).
4. Run health before deep edits: `curl -sf http://127.0.0.1:1320/health | jq .` (upstream probe: `/health/backends`)

## Supervision (MUST)

| Action | Command |
|--------|---------|
| Start | `go tool task serve` |
| Stop | `go tool task serve-down` |
| Detached | `./scripts/pc-up.sh -D` |

**MUST NOT** start `bin/polypus` or MLX in ad-hoc Cursor shells.

## Decision flows

Offer numbered options. One probe per turn when possible.

### 1. Is Polypus up?

```bash
curl -sf http://127.0.0.1:1320/health | jq .
```

- Fail → offer `go tool task serve`.
- OK but backend red → open [troubleshooting.md](troubleshooting.md) for that backend.

### 2. Which models are enabled?

```bash
curl -sS http://127.0.0.1:1320/v1/apis | jq '.apis[] | {id, models, schema}'
curl -sS http://127.0.0.1:1320/v1/apis/openai/models | jq '.data[].id'
curl -sS http://127.0.0.1:1320/v1/apis/systemone/models | jq '.data[].id'
curl -sS 'http://127.0.0.1:1320/v1/apis/openai/models?view=inventory' | jq '.data[].id'
```

Compare to `config.yaml` `backends.*.models.allow`. OpenAI surface = chat/embed/audio/router ids; SystemOne surface = `models.systemone_allow` (default `typesafe/jev`). Inventory view = last call.

### 3. Smoke all Cloudflare channels

Hermetic L1 smoke builds `cmd/polypus`, starts a temp gateway with a mock Cloudflare backend, and runs probes (no `go tool task serve`):

```bash
go tool task smoke-all
# or: go test -tags=integration -count=1 ./internal/smoke/integration
```

Runs chat (granite-4.0-h-micro), TTS (aura), STT (nova), and systemone (`typesafe/jev`). On **push to main**, CI runs the same package with `POLYPUS_SMOKE_LIVE=1` and secrets `CF_AI_API_KEY` / `CF_ACCOUNT_ID` against real Workers AI.

### 3a. Smoke chat only (L1 transport)

```bash
go tool task smoke-chat
```

Default model: `cf_local/@cf/ibm-granite/granite-4.0-h-micro` (override with `POLYPUS_CHAT_SMOKE_MODEL`).

### 3b. Smoke named router (when `routers:` configured)

Run after step 3a when `/v1/models` lists `router/…` ids or `~/.config/polypus/config.yaml` has `routers:` with a composed (`stage_router`) entry:

```bash
go tool task smoke-router
```

Default model: `router/investigator` (override with `POLYPUS_ROUTER_SMOKE_MODEL`).

- **503 / switchyard unavailable** → check `/health/backends` for `"id":"switchyard"`; ensure Switchyard is in the stack (`POLYPUS_SWITCHYARD=1`, default). Restart with `go tool task serve-down && go tool task serve`.
- **Passthrough only** (e.g. `router/scribe`) → `POLYPUS_ROUTER_SMOKE_MODEL=router/scribe go tool task smoke-router`; Switchyard not required (`POLYPUS_SWITCHYARD=0` OK).

`/health/backends` probing Switchyard is **not** a substitute for this smoke; it only checks `:4000/health`.

Router smoke integration requires Switchyard routing metadata: response header `x-model-router-selected-model` (or body `model` = served leaf). `polypus.Smoke` Options `RequireSelectedModel` and `AllowedSelectedModels` enforce that leaf is present and in the configured capable/efficient set.

**Tracing:** On composed router chat, the gateway `polypus.router` span sets `polypus.downstream_model` when Switchyard returns selected-model metadata. Switchyard child spans already record `gen_ai.request.model` on `libsy.client_call`. Gateway and Switchyard export to `polypus-otelcol` (`POLYPUS_OTLP_ENDPOINT`, default `http://127.0.0.1:4317` when Phoenix and HyperDX containers run); the collector copies all traces to HyperDX and OpenInference / `gen_ai.*` spans to Phoenix.

### 3c. Smoke OpenAI batch facade

Requires `batch_backend` enabled, `batch` capability on a Cloudflare extension backend, and a Workers AI batch-capable model on the allow list. Not part of `go tool task smoke-all` (async poll can take minutes).

```bash
go tool task smoke-batch
```

Default model: `cf_local/@cf/google/gemma-4-26b-a4b-it`. Integration test `TestSmokeBatch` probes upload JSONL (`POST /v1/files`), create a batch (`POST /v1/batches`), poll until terminal, then check output file content for the smoke `custom_id`.

### 3d. Smoke systemone (TypeSafe / Jev)

Requires `systemone_backend` enabled and `typesafe/jev` on the allow list. The CLI dials the gateway; Cloudflare credentials come from config `secrets:` plus keyring or process env on **serve**, not from the smoke shell:

```bash
go tool task smoke-systemone
```

Clients speak TypeSafe wire format at `POST /v1/systemone` (point `TYPESAFE_BASE_URL` at `:1320`).

### 4. Smoke audio

Default path is **cf_local** (`go tool task smoke` / `go tool task smoke-stt` use hermetic mock Cloudflare). MLX model ids (`go tool task smoke-local`, `go tool task smoke-stt-local`, `go tool task smoke-higgs`) use hermetic mock MLX in integration tests (no `go tool task serve`):

```bash
go tool task smoke
go tool task smoke-stt
go tool task smoke-local
go tool task smoke-stt-local
go tool task smoke-higgs
```

### 5. Full model matrix

See [harness.md](harness.md). L1 runs in this repo; L2/L3 may require host tooling.

### 6. model_not_allowed

- POST returns 400 `model_not_allowed`.
- Fix: add model to `models.allow` or use prefixed id (`cf_local/...`, `lm_studio/...`).
- If a host job cites a model id that the allow-list blocks, align host config with Polypus `config.yaml`.

### 7. Empty content / XML parse fail

See [thinking-policy.md](thinking-policy.md). Run L2 harness when host provides it. Check Phoenix http://127.0.0.1:6006 and `logs/inference-failures/<trace_id>.json` (olly dump).

### 8. cf_local down

- Probe: `curl -sS 'http://127.0.0.1:1320/v1/apis/openai/models?view=inventory' | jq '.data | length'`
- Needs: `cf_local` in `config.yaml`, `secrets:` listing `CF_AI_API_KEY` and `CF_ACCOUNT_ID`, and those values in the process environment **or** OS keyring (`polypus secret set`). Env and `stack/.env` still win. Compose / GitLab inject env only (no keyring in the container).

### 9. gemini_studio down

- Probe inventory: `curl -sS 'http://127.0.0.1:1320/v1/apis/openai/models?view=inventory' | jq '.data[] | select(.id | startswith("gemini_studio/"))'`
- Needs: `extension: gemini` backend, `GEMINI_API_KEY` in env or keyring (`polypus secret set GEMINI_API_KEY` when listed under `secrets:`). Serve fail-closes on startup when the gemini remote backend is configured but the bearer env is missing (same pattern as other remotes).

### 10. lm_studio down

- LM Studio is external; user starts it on `:1234`.
- Probe: `curl -sf http://127.0.0.1:1234/v1/models | jq .`

## Config edits

- Bootstrap: `go tool task init` or `polypus init` (writes `~/.config/polypus/config.yaml` if missing). **MUST NOT** treat a manual `cp config.yaml.example` as the default setup.
- Cloudflare (local machine): uncomment `secrets:` (`CF_AI_API_KEY`, `CF_ACCOUNT_ID`) and the `cf_local` backend in that file, then:
  ```bash
  polypus secret set CF_AI_API_KEY
  polypus secret set CF_ACCOUNT_ID
  ```
  `secret set` refuses names that are not listed under `secrets:` in the live config. Omit `secrets:` for MLX-only so the keyring is never queried. Docker Compose / Windows GitLab: put `CF_*` in the container env (SOPS); do not use the host keyring inside Linux containers.
- After YAML `backends`, `routers:`, or secrets change: restart gateway in process-compose TUI (or `go tool task serve-down && go tool task serve`). Runtime allow via `GET /` form or `POST /v1/admin/models/allow` does **not** require restart.
- Gateway access keys (optional): `polypus admin-key generate --name ops --yes`. When at least one key exists, the whole gateway (except `GET /health*`) requires `Authorization: Bearer ppk...`, `X-Api-Key`, or HTTP Basic (password = the key). Empty key store = open. Allow POST is inventory-gated only (no per-route key).
- With `cf_local` configured, serve fail-closes on startup if Cloudflare Model Search ping fails (fix `secret set` / env; MLX-only configs skip this probe). Remote backends (including `extension: gemini` and OpenRouter) must resolve `auth.bearer_env` at startup.
- **MUST NOT** add non-loopback backend URLs when `reject_non_loopback_backends` applies.

## Observability

- Phoenix UI: http://127.0.0.1:6006 (LLM / OpenInference)
- HyperDX UI: http://127.0.0.1:8080 (app traces / logs)
- OTLP ingest (gateway, Switchyard, clients): gRPC `127.0.0.1:4317`, HTTP `127.0.0.1:4318` via `polypus-otelcol` (`otelcol.config.yaml` routing: all traces to HyperDX; OpenInference / `gen_ai.*` spans also copied to Phoenix)
- OTel fan-out integration smoke: `POLYPUS_SMOKE_OTEL=1 go tool task smoke-otel` (Docker `phoenix`, `hyperdx`, `otelcol`; matches `go tool task serve` obs stack). Homelab host: `make smoke-collector` from `polypus-local` after deploy or serve.
- `POLYPUS_OTLP_ENDPOINT` defaults to `http://127.0.0.1:4317` when both `POLYPUS_PHOENIX=1` and `POLYPUS_HYPERDX=1` (set by `pc-up.sh`); use `${POLYPUS_OTLP_ENDPOINT}` for `openinference.endpoint` in client configs
- HyperDX OTel table TTL: `HYPERDX_OTEL_EXPORTER_TABLES_TTL` (default `1h`); set `HYPERDX_OTEL_EXPORTER_RECONCILE_TABLE_TTL=true` once to rewrite existing `otel_*` table TTLs
- HyperDX ClickHouse system-log TTL: 7 days via `hyperdx.clickhouse.config.xml` (separate from OTel retention)
- Failure dumps: `logs/inference-failures/` via [olly](https://github.com/behaviorengineering/olly)
- Disable tracing: `POLYPUS_OTEL=0`
- Skip containers: `POLYPUS_PHOENIX=0`, `POLYPUS_HYPERDX=0`
- Docker probe timeout (default 3s): `POLYPUS_DOCKER_PROBE_TIMEOUT`
- Skip Docker confirm when daemon is down: `POLYPUS_DOCKER_CONTINUE=1`
- `go tool task serve-down` stops processes only; named Phoenix/HyperDX volumes are kept (pack skill `process-compose-docker`)
- Shared practice: skill `process-compose-docker` (cursor-packs)

## Client contract

Downstream apps MUST use `POLYPUS_BASE_URL` only (`http://127.0.0.1:1320`). Backend tables live in `~/.config/polypus/config.yaml`, not in client repos. Model ids in client job configs MUST match allow-list entries with the correct prefix.

### Resilience ownership (breaker vs retries)

**Moral:** Polypus fail-opens; clients sleep-and-retry. Do not put both jobs in one place.

| Concern | Owner | What it does |
|---------|-------|----------------|
| Circuit breaker | Polypus (`internal/gateway/upstream.Board`) | After consecutive dial failures, stops hitting a sick upstream and returns **503** (open / half-open limit). Fixed open window; not exponential backoff. |
| Retries / exponential backoff | HTTP clients | Budgeted retry only on clearly “try later” answers (**503**, **429**, honor `Retry-After` when present). |

**CONSTRAINT:** Polypus MUST own per-upstream circuit breaking for gateway dials. MUST NOT add a gateway-wide sleep-and-retry (exponential backoff) loop around chat or streamed hops.

- Enforcement: dials go through `upstream.Board.Execute`; chat/stream handlers fail fast when the breaker is open.
- Violation: remove gateway retry/sleep; keep fail-open + clear 503.

**CONSTRAINT:** Clients MUST treat retries as their concern. MUST NOT stack a blind exponential loop on every **5xx** while Polypus already shed load with **503**. MUST NOT assume Polypus will replay a request after bytes have started streaming.

- Enforcement: client HTTP stacks retry only on 503/429 (and similar retryable statuses) with a small budget; cancel with the caller context.
- Violation: strip gateway-style retry from the client; keep a budgeted “try later” policy only.

CORRECT:
```text
Breaker open → Polypus returns 503
Client waits (Retry-After or short backoff), retries once or twice, then fails
```

PROHIBITED:
```text
Polypus sleeps with exponential backoff inside the chat hop, and
the client also retries every 502/503 without a budget
```

Bifrost may expose per-provider `MaxRetries` on leaf dials; that is hop-local and optional. It is not a substitute for Polypus’s breaker, and it does not move retry ownership away from clients for end-to-end chat.

## Releases and publish-complete

**CONSTRAINT:** Treat `vX.Y.Z` as **published** only when **Auto patch release** is green through **verify** (GoReleaser binaries, Docker push, GHCR manifest inspect). MUST NOT bump consumer `images.env` or submodule pins on a half-green tag.

- On failure, GitHub opens a `ci-failure` Issue with the Actions run URL and log excerpt. Use that Issue as the local-agent queue (diagnose with `gh run view --log-failed`, fix CI, re-run Docker release if needed).
- Fleet policy: cursor-packs `manage-go-releases` publish-complete gate.

## Refresh this pack

Re-run [../BOOTSTRAP.md](../BOOTSTRAP.md) or edit shards under `ai-copilots/skills/polypus-operator/` only.
