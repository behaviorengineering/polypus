# Switchyard routing

Switchyard sits between the client and the leaf models. The client always sends one route `id` as the model name. Switchyard picks a **target** (a concrete model) for that turn.

This folder is about Switchyard's routing types and when to use each one. Upstream algorithm pages live in `providers/switchyard/docs/routing_algorithms/`.

| Type | Page | Use when |
|------|------|----------|
| `stage_router` | [stage-router.md](stage-router.md) | Tool-progress signals should pick capable vs efficient |
| `random` | [random.md](random.md) | Fixed traffic split; A/B or cost experiments |
| `llm_classifier` (`capability`) | [llm-classifier.md](llm-classifier.md) | A judge predicts whether the cheap model can do this task (weak or strong only) |
| `llm_classifier` (`custom`) | [llm-classifier-custom.md](llm-classifier-custom.md) | A judge names one of two or more targets from your JSON schema |
| `llm_classifier` (`escalation`) | [escalation.md](escalation.md) | Start cheap; latch to strong after a judge sees real trouble |
| `passthrough` | [passthrough.md](passthrough.md) | Always one model; no pick |

How to choose:

- Agent with tools (edit, test, shell) and you care about "stuck vs producing": `stage_router`.
- You need a known mix of models for eval or canary: `random`.
- The **prompt** should decide cheap vs strong before the work starts: `llm_classifier` capability mode.
- The **prompt** should pick among more than two named targets: `llm_classifier` custom mode ([llm-classifier-custom.md](llm-classifier-custom.md)).
- The **run** should decide, after the cheap model has already tried: escalation mode.
- You never want a second model: `passthrough`.

Context is the client `messages` array unless a type adds session latching (classifier affinity, escalation confirmations). Switchyard does not keep a private notebook per target.

Polypus dials Switchyard over Bifrost (synthetic provider id `switchyard`) for composed `router/…` chat. Leaf callbacks still hit Polypus `:1320`. CF `/ai/run` speech and Model Search stay on the Cloudflare extension (Workers AI OpenAI-compat does not expose `/ai/v1/audio/*`; verified live).

## Common TOML shape

```toml
schema_version = 1

[llm_clients.example]
format = "openai_chat"
base_url = "http://127.0.0.1:1320/v1"

[targets.strong]
id = "cf_local/@cf/zai-org/glm-4.7-flash"
llm_client = "example"

[targets.weak]
id = "lm_studio/ornith-1.0-9b"
llm_client = "example"

[routes.my_route]
id = "my-route"
type = "stage_router"   # or random, llm_classifier, passthrough
```

Clients send `model: "my-route"` (the route `id`). Table names like `[routes.my_route]` are local to the file.
