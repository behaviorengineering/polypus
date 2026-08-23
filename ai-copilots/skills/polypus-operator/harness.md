# Polypus model harness

Polypus ships **L1 transport smoke** in this repo. Deeper L2/L3 XML and job-fixture matrices may live in a **host application** that calls `POLYPUS_BASE_URL`; this pack documents Polypus-native probes only.

## Prerequisites

- Polypus up (`make serve` from this repo)
- Live LLM backends reachable (cf_local, lm_studio, or mlx_local per `config.yaml`)

## L1 smoke (this repo)

```bash
make smoke-chat
POLYPUS_CHAT_SMOKE_MODEL='cf_local/@cf/zai-org/glm-4.7-flash' make smoke-chat
make smoke
make smoke-stt
```

Binary: `bin/polypus-chat-smoke` (built with `make build`).

Default chat model: `cf_local/@cf/google/gemma-4-26b-a4b-it` (override with `POLYPUS_CHAT_SMOKE_MODEL`).

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
| Empty content / parse errors | L1 + host L2 if available |
| Specific downstream job fails | Host L3 for that job's model |
| New model on allow-list | L1 on that model, then L2 if used for XML |

## Manifest

Example tier config may ship with your host as `models.harness.yaml`. Auto-discovery can read Polypus `config.yaml` `models.allow`.
