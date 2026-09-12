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
2. Load shard when needed: [config-reference.md](config-reference.md), [troubleshooting.md](troubleshooting.md), [harness.md](harness.md), [thinking-policy.md](thinking-policy.md).
3. Run health before deep edits: `curl -sf http://127.0.0.1:1320/health | jq .` (upstream probe: `/health/backends`)

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

### 3. Smoke chat (L1 transport)

```bash
make smoke-chat
```

Default model: `cf_local/@cf/google/gemma-4-26b-a4b-it` (override with `POLYPUS_CHAT_SMOKE_MODEL`).

### 3b. Smoke named router (when `routers:` configured)

Run after step 3 when `/v1/models` lists `router/…` ids or `~/.config/polypus/config.yaml` has `routers:` with a composed (`stage_router`) entry:

```bash
make smoke-router
```

Default model: `router/investigator` (override with `POLYPUS_ROUTER_SMOKE_MODEL`).

- **503 / switchyard unavailable** → check `/health/backends` for `"id":"switchyard"`; ensure Switchyard is in the stack (`POLYPUS_SWITCHYARD=1`, default). Restart with `make serve-down && make serve`.
- **Passthrough only** (e.g. `router/scribe`) → `POLYPUS_ROUTER_SMOKE_MODEL=router/scribe make smoke-router`; Switchyard not required (`POLYPUS_SWITCHYARD=0` OK).

`/health/backends` probing Switchyard is **not** a substitute for this smoke; it only checks `:4000/health`.

### 4. Smoke audio

Default path is **cf_local** (gateway needs `CF_AI_API_KEY` / `CF_ACCOUNT_ID`). For MLX:

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
- Needs: `CF_AI_API_KEY`, `CF_ACCOUNT_ID` in the process environment.

### 9. lm_studio down

- LM Studio is external; user starts it on `:1234`.
- Probe: `curl -sf http://127.0.0.1:1234/v1/models | jq .`

## Config edits

- Copy `config.yaml.example` → `~/.config/polypus/config.yaml` when bootstrapping backends.
- After allow-list change: restart gateway in process-compose TUI (or serve-down + serve).
- **MUST NOT** add non-loopback backend URLs when `reject_non_loopback_backends` applies.

## Observability

- Phoenix UI: http://127.0.0.1:6006
- OTLP gRPC: `:4317` (`openinference.endpoint` for clients)
- Failure dumps: `logs/inference-failures/` via [olly](https://github.com/behaviorengineering/olly) via [olly](https://github.com/behaviorengineering/olly)
- Disable tracing: `POLYPUS_OTEL=0`

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
