# Passthrough

`type = "passthrough"` registers one target under one route `id`. There is no pick, no judge, and no weights.

Upstream overview: `providers/switchyard/docs/routing_algorithms/overview.md` (direct model routes).

## How a turn is routed

Every request with that route `id` goes to `target`. Same leaf every time.

## Fields (out of the box)

| Key | Required | Values / notes |
|-----|----------|----------------|
| `type` | yes | `passthrough` |
| `target` | yes | One target table name |

Do not set picker, weights, or classifier keys on this route.

## Use cases

### Stable public name for one model

Clients keep `model: "scribe"` (or any `id`). You change the target table when you swap the underlying model. Useful for demos and evals that must stay comparable turn to turn.

```toml
[routes.scribe]
id = "scribe"
type = "passthrough"
target = "strong"
```

### Quality-critical drafting

Tone, legal, or architecture write-ups where a weaker leaf must never "continue" the thread. Also code review that should always be the strong model.

Poor fits: long mechanical agent loops (you overpay). Use [stage-router](stage-router.md) or [escalation](escalation.md).

### Local or self-hosted only

One vLLM (or similar) server, one route. Switchyard does not start that server; it only posts to the client `base_url`.

### No routing at all

If you do not need Switchyard, call the leaf model id directly. Passthrough is for when you still want a Switchyard route id (shared client, headers, stats) without a strategy.

## When not to use this type

- Two leaves and tool-progress: [stage-router](stage-router.md).
- Traffic mix: [random](random.md).
- Judge the prompt or the run: [llm-classifier](llm-classifier.md) / [escalation](escalation.md).
