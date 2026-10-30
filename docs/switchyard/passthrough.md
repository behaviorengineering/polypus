# Passthrough sends every turn to one model

`type = "passthrough"` registers one target under one route `id`, with no pick, no judge and no weights.

The upstream overview, `providers/switchyard/docs/routing_algorithms/overview.md`, covers this type under direct model routes.

## How a turn is routed

Every request with that route `id` goes to `target`, so every turn lands on the same leaf.

## Fields (out of the box)

| Key | Required | Values / notes |
|-----|----------|----------------|
| `type` | yes | `passthrough` |
| `target` | yes | One target table name |

Do not set picker, weights or classifier keys on this route.

## Where passthrough fits

### Stable public name for one model

Clients keep sending `model: "scribe"` (or any `id`), and you change the target table when you swap the underlying model. That suits demos and evals that must stay comparable turn to turn.

```toml
[routes.scribe]
id = "scribe"
type = "passthrough"
target = "strong"
```

### Quality-critical drafting

Use it for tone, legal or architecture write-ups, where a weaker leaf must never "continue" the thread. It also suits code review that should always run on the strong model.

It fits long mechanical agent loops poorly, because you overpay. Use [stage-router](stage-router.md) or [escalation](escalation.md) for those.

### Local or self-hosted only

Point one route at one vLLM (or similar) server. Switchyard does not start that server; it only posts to the client `base_url`.

### No routing at all

If you do not need Switchyard, call the leaf model id directly. Use passthrough when you still want a Switchyard route id (shared client, headers, stats) without a strategy.

## When not to use this type

- Two leaves and tool-progress: [stage-router](stage-router.md).
- Traffic mix: [random](random.md).
- Judge the prompt or the run: [llm-classifier](llm-classifier.md) / [escalation](escalation.md).
