---
name: polypus-operator
description: >-
  Operates Polypus inference gateway: health, config, allow-list, smoke tests,
  model harness, thinking policy, Phoenix traces. Use when Polypus is down,
  models are rejected, chat returns empty content, or Consilium jobs fail on
  inference. Not for case intake or timeline.
---

# Polypus operator

**Moral:** Diagnose with probes, then fix config or supervision. Consilium calls only `:1320`.

## Start every task

1. Confirm workspace: Polypus repo root or Consilium `providers/polypus/`.
2. Load shard when needed: [config-reference.md](config-reference.md), [troubleshooting.md](troubleshooting.md), [harness.md](harness.md), [thinking-policy.md](thinking-policy.md).
3. Run health before deep edits: `curl -sf http://127.0.0.1:1320/health | jq .`

## Supervision (MUST)

| Context | Start | Stop |
|---------|-------|------|
| Polypus repo | `make serve` | `make serve-down` |
| Consilium repo | `make polypus-serve` | `make polypus-down` |

Detached (scripts only): `./scripts/pc-up.sh -D` (Polypus) or `./providers/polypus/scripts/pc-up.sh -D` (Consilium).

**MUST NOT** start `bin/polypus`, MLX, or cf-adapter in ad-hoc Cursor shells.

## Decision flows

Offer numbered options. One probe per turn when possible.

### 1. Is Polypus up?

```bash
curl -sf http://127.0.0.1:1320/health | jq .
```

- Fail → offer start (`make serve` / `make polypus-serve`).
- OK but backend red → open [troubleshooting.md](troubleshooting.md) for that backend.

### 2. Which models are enabled?

```bash
curl -sS http://127.0.0.1:1320/v1/models | jq '.data[].id'
curl -sS 'http://127.0.0.1:1320/v1/models?view=inventory' | jq '.data[].id'
```

Compare to `config.yaml` `backends.*.models.allow`. Enabled list = first call; full upstream = second.

### 3. Smoke chat (L1 transport)

| Context | Command |
|---------|---------|
| Polypus root | `make smoke-chat` |
| Consilium root | `make polypus-smoke-chat` |

Default model: `cf_local/@cf/google/gemma-4-26b-a4b-it` (override with `POLYPUS_CHAT_SMOKE_MODEL`).

### 4. Smoke audio

| Test | Polypus | Consilium |
|------|---------|-----------|
| TTS | `make smoke` | `make polypus-smoke` |
| TTS + STT | `make smoke-stt` | `make polypus-smoke-stt` |

### 5. Full model matrix (Consilium only)

See [harness.md](harness.md). Requires Polypus up + `stack/ai.yaml`.

### 6. model_not_allowed

- POST returns 400 `model_not_allowed`.
- Fix: add model to `models.allow` or use prefixed id (`cf_local/...`, `lm_studio/...`).
- If Consilium cites model in `stack/ai.yaml` but allow-list blocks it: `bin/stack-doctor diagnose`.

### 7. Empty content / XML parse fail

See [thinking-policy.md](thinking-policy.md). Run L2 harness. Check Phoenix http://127.0.0.1:6006 and `logs/inference-failures/<trace_id>.json`.

### 8. cf_local down

- Probe: `curl -sf http://127.0.0.1:1323/v1/models | jq .`
- Needs: `INFERENCE_CLOUD_CASE=1`, `CF_AI_API_KEY`, `CF_ACCOUNT_ID` in Consilium `stack/.env`.

### 9. lm_studio down

- LM Studio is external; user starts it on `:1234`.
- Probe: `curl -sf http://127.0.0.1:1234/v1/models | jq .`

## Config edits

- Copy `config.yaml.example` → `config.yaml` when bootstrapping backends.
- After allow-list change: restart gateway in process-compose TUI (or serve-down + serve).
- **MUST NOT** add non-loopback backend URLs when `reject_non_loopback_backends` applies.

## Observability

- Phoenix UI: http://127.0.0.1:6006
- OTLP gRPC: `:4317` (`openinference.endpoint` for clients)
- Failure dumps: `logs/inference-failures/`
- Disable tracing: `POLYPUS_OTEL=0`

## Consilium alignment

Consilium uses `stack/ai.yaml` → `POLYPUS_BASE_URL` only. Backend table lives in Polypus `config.yaml`, not Consilium. Model ids in job configs must match allow-list entries with correct prefix.

## Refresh this pack

Re-run [../BOOTSTRAP.md](../BOOTSTRAP.md) or edit shards under `ai-copilots/skills/polypus-operator/` only.
