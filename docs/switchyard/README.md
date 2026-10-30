# Choosing a Switchyard routing type

Switchyard sits between the client and the leaf models. The client always sends one route `id` as the model name, and Switchyard picks a **target** (a concrete model) for each turn.

Each page in this folder covers one routing type and when to use it. The upstream algorithm pages live in `providers/switchyard/docs/routing_algorithms/`.

| Type | Page | Use when |
|------|------|----------|
| `stage_router` | [stage-router.md](stage-router.md) | Tool-progress signals should pick capable vs efficient |
| `random` | [random.md](random.md) | Fixed traffic split; A/B or cost experiments |
| `llm_classifier` (`capability`) | [llm-classifier.md](llm-classifier.md) | A judge predicts whether the cheap model can do this task (weak or strong only) |
| `llm_classifier` (`custom`) | [llm-classifier-custom.md](llm-classifier-custom.md) | A judge names one of two or more targets from your JSON schema |
| `llm_classifier` (`escalation`) | [escalation.md](escalation.md) | Start cheap; latch to strong after a judge sees real trouble |
| `passthrough` | [passthrough.md](passthrough.md) | Always one model; no pick |

To choose a type, find the line that matches your case:

- An agent with tools (edit, test, shell), where you care about "stuck vs producing": `stage_router`.
- You need a known mix of models for eval or canary: `random`.
- The prompt should decide cheap vs strong before the work starts: `llm_classifier` capability mode.
- The prompt should pick among more than two named targets: `llm_classifier` custom mode ([llm-classifier-custom.md](llm-classifier-custom.md)).
- The run should decide, after the cheap model has already tried: escalation mode.
- You never want a second model: `passthrough`.

The context for every turn is the client's `messages` array, unless a type adds session latching (classifier affinity or escalation confirmations). Switchyard does not keep a private notebook per target.

For composed `router/…` chat, Polypus dials Switchyard over Bifrost, using the synthetic provider id `switchyard`. The leaf callbacks still hit Polypus on `:1320`. Cloudflare TTS and STT calls enter Bifrost, and then a plugin short-circuits them onto the extension's `/ai/run`. The plugin is there because the Workers AI OpenAI-compatible API does not expose `/ai/v1/audio/*` (verified live). Model Search stays on the Cloudflare extension.

## Every route file uses the same TOML shape

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

Clients send `model: "my-route"`, which is the route `id`. Table names such as `[routes.my_route]` are local to the file.
