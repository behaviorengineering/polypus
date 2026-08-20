# Polypus model harness (Consilium parent)

The full L1/L2/L3 matrix lives in Consilium `tool/internal/stackdoctor/harness/`. Polypus standalone repo runs L1 via `make smoke-chat` only.

## Prerequisites

- Polypus up (`make polypus-serve` from Consilium root)
- `stack/ai.yaml` present (gitignored local copy)
- Live LLM backends reachable

## Commands (Consilium repo root)

```bash
make model-harness
bin/stack-doctor model-harness --discover
bin/stack-doctor model-harness --tier L1
bin/stack-doctor model-harness --tier L2
bin/stack-doctor model-harness --tier L3
bin/stack-doctor model-harness --tier L1 --model 'cf_local/@cf/google/gemma-4-26b-a4b-it'
bin/stack-doctor model-harness --json
```

## Polypus-only L1 smoke

From Polypus repo (or Consilium via Make):

```bash
make smoke-chat                    # Polypus Makefile
make polypus-smoke-chat            # Consilium Makefile
POLYPUS_CHAT_SMOKE_MODEL='cf_local/@cf/zai-org/glm-4.7-flash' make smoke-chat
```

Binary: `bin/polypus-chat-smoke` (built with Polypus `make build`).

## Tier summary

| Tier | Probes | Purpose |
|------|--------|---------|
| **L1** | ping, content_nonempty, thinking_policy, optional thinking_on | Transport and thinking defaults |
| **L2** | minimal_xml, directives_ack, list_field | DSPy/XML structured output |
| **L3** | prose_clarity, concept_discover, grounded_ask | Job slices with frozen fixtures |

## Manifest

Example config: `stack/models.harness.yaml.example`. Auto-discovery reads Polypus `config.yaml` allow-list.

## When to run which tier

| User report | Start with |
|-------------|------------|
| Gateway down / connection errors | Health + L1 smoke only |
| Empty content / parse errors | L1 thinking_policy + L2 |
| Specific Consilium job fails | L3 for that job's model |
| New model added to allow-list | L1 on that model, then L2 if used for XML jobs |

## Integration test gate

`CONSILIUM_MODEL_HARNESS=1 go test ./tool/internal/stackdoctor/...` runs harness integration when env set.
