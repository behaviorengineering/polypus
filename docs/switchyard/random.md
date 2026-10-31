# Random routing splits traffic by weight

`type = "random"` picks one of two or more configured targets for each request, and the weights set the mix. It does not read the prompt or the tool history.

The upstream page is `providers/switchyard/docs/routing_algorithms/random_routing.md`.

## How a turn is routed

Each request draws a target from `targets` using `weights`, so turns in the same conversation can land on different models. Short runs will not match the weights exactly.

Omit `weights` for an even split, and set a weight to `0` to disable that target. The optional `seed` makes the sequence repeatable for tests, and you omit it for live traffic.

## Fields (out of the box)

| Key | Required | Values / notes |
|-----|----------|----------------|
| `type` | yes | `random` |
| `targets` | yes | List of target table names (unique, two or more) |
| `weights` | no | One non-negative number per target, same order. Need not sum to 1. |
| `seed` | no | Integer. Same seed plus same request order repeats the picks. |

## Where random routing fits

### A/B a new model

Send most traffic to the current model and a slice to a candidate, then compare quality and cost on real prompts. Do not use this when the same thread must stay on one model, because the chat UX will flip mid-conversation. For sticky sessions, use [llm-classifier](llm-classifier.md) with `session_affinity`, or [passthrough](passthrough.md).

```toml
[routes.ab_test]
id = "ab-test"
type = "random"
targets = ["strong", "weak"]
weights = [1, 9]
```

That route sends about 10% of requests to strong and 90% to weak.

### Eval and benchmark baselines

A fixed mix lets a harness hit both models without writing two client configs. Set `seed` so a replay is comparable.

It suits offline eval, load tests and questions such as "what is p50 latency on this mix".

It fits debug loops poorly, because there you want progress signals rather than a coin flip. Use [stage-router](stage-router.md) for those.

```toml
[routes.bench]
id = "bench"
type = "random"
targets = ["strong", "weak"]
weights = [1, 1]
seed = 42
```

### Cost experiments

Raise the cheap weight until quality drops too far. Random routing only sets the traffic mix and makes no per-turn judgement, so if quality must follow "stuck vs producing", use [stage-router](stage-router.md) instead.

### Canary a self-hosted target

Keep most traffic on a known cloud model and send a small weight to a local server you are trying. Random routing does not fail over, so if the local target is down, that slice fails.

## When not to use this type

- Tool-aware capable vs efficient: [stage-router](stage-router.md).
- Prompt looks hard, send to strong: [llm-classifier](llm-classifier.md).
- Cheap until the run is actually failing: [escalation](escalation.md).
- Always one model: [passthrough](passthrough.md).
