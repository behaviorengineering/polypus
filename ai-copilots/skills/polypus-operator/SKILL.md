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
| Start | `make serve` |
| Stop | `make serve-down` |
| Detached | `./scripts/pc-up.sh -D` |

**MUST NOT** start `bin/polypus` or MLX in ad-hoc Cursor shells.

## Decision flows

Offer numbered options. One probe per turn when possible.

### 1. Is Polypus up?

```bash
curl -sf http://127.0.0.1:1320/health | jq .
```

- Fail → offer `make serve`.
- OK but backend red → open [troubleshooting.md](troubleshooting.md) for that backend.

### 2. Which models are enabled?

```bash
curl -sS http://127.0.0.1:1320/v1/models | jq '.data[].id'
curl -sS 'http://127.0.0.1:1320/v1/models?view=inventory' | jq '.data[].id'
```

Compare to `config.yaml` `backends.*.models.allow`. Enabled list = first call; full upstream = second.

### 3. Smoke all Cloudflare channels

With the gateway up and Cloudflare `secrets:` filled (env or `polypus secret set`):

```bash
make smoke-all
```

Runs chat (glm-4.7-flash), TTS (aura), STT (nova), and systemone (`typesafe/jev`) via `bin/polypus-smoke`. Any failure fails the command. On **push to main**, CI expands [`config.ci-smoke.yaml.example`](../../../config.ci-smoke.yaml.example) with secrets `CF_AI_API_KEY` and `CF_ACCOUNT_ID` and runs the same probes (`-require-cf`).

### 3a. Smoke chat only (L1 transport)

```bash
make smoke-chat
```

Default model: `cf_local/@cf/zai-org/glm-4.7-flash` (override with `POLYPUS_CHAT_SMOKE_MODEL`).

### 3b. Smoke named router (when `routers:` configured)

Run after step 3a when `/v1/models` lists `router/…` ids or `~/.config/polypus/config.yaml` has `routers:` with a composed (`stage_router`) entry:

```bash
make smoke-router
```

Default model: `router/investigator` (override with `POLYPUS_ROUTER_SMOKE_MODEL`).

- **503 / switchyard unavailable** → check `/health/backends` for `"id":"switchyard"`; ensure Switchyard is in the stack (`POLYPUS_SWITCHYARD=1`, default). Restart with `make serve-down && make serve`.
- **Passthrough only** (e.g. `router/scribe`) → `POLYPUS_ROUTER_SMOKE_MODEL=router/scribe make smoke-router`; Switchyard not required (`POLYPUS_SWITCHYARD=0` OK).

`/health/backends` probing Switchyard is **not** a substitute for this smoke; it only checks `:4000/health`.

### 3c. Smoke systemone (TypeSafe / Jev)

Requires `systemone_backend` enabled and `typesafe/jev` on the allow list. The CLI dials the gateway; Cloudflare credentials come from config `secrets:` plus keyring or process env on **serve**, not from the smoke shell:

```bash
make smoke-systemone
```

Clients speak TypeSafe wire format at `POST /v1/systemone` (point `TYPESAFE_BASE_URL` at `:1320`).

### 4. Smoke audio

Default path is **cf_local** (gateway needs `secrets:` for `CF_AI_API_KEY` / `CF_ACCOUNT_ID`, then env or `polypus secret set`). For MLX:

```bash
make smoke
make smoke-stt
make smoke-local
make smoke-stt-local
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

- Probe: `curl -sS 'http://127.0.0.1:1320/v1/models?view=inventory' | jq '.data | length'`
- Needs: `cf_local` in `config.yaml`, `secrets:` listing `CF_AI_API_KEY` and `CF_ACCOUNT_ID`, and those values in the process environment **or** OS keyring (`polypus secret set`). Env and `stack/.env` still win. Compose / GitLab inject env only (no keyring in the container).

### 9. lm_studio down

- LM Studio is external; user starts it on `:1234`.
- Probe: `curl -sf http://127.0.0.1:1234/v1/models | jq .`

## Config edits

- Bootstrap: `make init` or `polypus init` (writes `~/.config/polypus/config.yaml` if missing). **MUST NOT** treat a manual `cp config.yaml.example` as the default setup.
- Cloudflare (local machine): uncomment `secrets:` (`CF_AI_API_KEY`, `CF_ACCOUNT_ID`) and the `cf_local` backend in that file, then:
  ```bash
  polypus secret set CF_AI_API_KEY
  polypus secret set CF_ACCOUNT_ID
  ```
  `secret set` refuses names that are not listed under `secrets:` in the live config. Omit `secrets:` for MLX-only so the keyring is never queried. Docker Compose / Windows GitLab: put `CF_*` in the container env (SOPS); do not use the host keyring inside Linux containers.
- After allow-list or secrets change: restart gateway in process-compose TUI (or `make serve-down && make serve`). With `cf_local` configured, serve fail-closes on startup if Cloudflare Model Search ping fails (fix `secret set` / env; MLX-only configs skip this probe).
- **MUST NOT** add non-loopback backend URLs when `reject_non_loopback_backends` applies.

## Observability

- Phoenix UI: http://127.0.0.1:6006 (LLM / OpenInference)
- Phoenix OTLP gRPC: `:4317` (`openinference.endpoint` for clients)
- HyperDX UI: http://127.0.0.1:8080 (app traces / logs)
- HyperDX OTLP: gRPC `:4319`, HTTP `:4318` (point app `olly` / OTEL exporters here; keep Phoenix on `:4317`)
- HyperDX OTel table TTL: `HYPERDX_OTEL_EXPORTER_TABLES_TTL` (default `1h`); set `HYPERDX_OTEL_EXPORTER_RECONCILE_TABLE_TTL=true` once to rewrite existing `otel_*` table TTLs
- HyperDX ClickHouse system-log TTL: 7 days via `hyperdx.clickhouse.config.xml` (separate from OTel retention)
- Failure dumps: `logs/inference-failures/` via [olly](https://github.com/behaviorengineering/olly)
- Disable tracing: `POLYPUS_OTEL=0`
- Skip containers: `POLYPUS_PHOENIX=0`, `POLYPUS_HYPERDX=0`
- Docker probe timeout (default 3s): `POLYPUS_DOCKER_PROBE_TIMEOUT`
- Skip Docker confirm when daemon is down: `POLYPUS_DOCKER_CONTINUE=1`
- `make serve-down` stops processes only; named Phoenix/HyperDX volumes are kept (pack skill `process-compose-docker`)
- Shared practice: skill `process-compose-docker` (cursor-packs)

## Client contract

Downstream apps MUST use `POLYPUS_BASE_URL` only (`http://127.0.0.1:1320`). Backend tables live in `~/.config/polypus/config.yaml`, not in client repos. Model ids in client job configs MUST match allow-list entries with the correct prefix.

### Resilience ownership (breaker vs retries)

**Moral:** Polypus fail-opens; clients sleep-and-retry. Do not put both jobs in one place.

| Concern | Owner | What it does |
|---------|-------|----------------|
| Circuit breaker | Polypus (`internal/upstream.Board`) | After consecutive dial failures, stops hitting a sick upstream and returns **503** (open / half-open limit). Fixed open window; not exponential backoff. |
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

## Refresh this pack

Re-run [../BOOTSTRAP.md](../BOOTSTRAP.md) or edit shards under `ai-copilots/skills/polypus-operator/` only.
