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

```bash
make smoke
make smoke-stt
```

### 5. Full model matrix

See [harness.md](harness.md). L1 runs in this repo; L2/L3 may require host tooling.

### 6. model_not_allowed

- POST returns 400 `model_not_allowed`.
- Fix: add model to `models.allow` or use prefixed id (`cf_local/...`, `lm_studio/...`).
- If a host job cites a model id that the allow-list blocks, align host config with Polypus `config.yaml`.

### 7. Empty content / XML parse fail

See [thinking-policy.md](thinking-policy.md). Run L2 harness when host provides it. Check Phoenix http://127.0.0.1:6006 and `logs/inference-failures/<trace_id>.json`.

### 8. cf_local down

- Probe: `curl -sS 'http://127.0.0.1:1320/v1/models?view=inventory' | jq '.data | length'`
- Needs: `INFERENCE_CLOUD_CASE=1`, `CF_AI_API_KEY`, `CF_ACCOUNT_ID` in the process environment.

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
- Failure dumps: `logs/inference-failures/`
- Disable tracing: `POLYPUS_OTEL=0`

## Client contract

Downstream apps should use `POLYPUS_BASE_URL` only (`http://127.0.0.1:1320`). Backend tables live in `~/.config/polypus/config.yaml`, not in client repos. Model ids in client job configs must match allow-list entries with the correct prefix.

## Refresh this pack

Re-run [../BOOTSTRAP.md](../BOOTSTRAP.md) or edit shards under `ai-copilots/skills/polypus-operator/` only.
