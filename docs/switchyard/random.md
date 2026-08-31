# Random routing

`type = "random"` picks one configured target per request. It does **not** read the prompt or tool history. Two or more targets. Weights set the mix.

Upstream: `providers/switchyard/docs/routing_algorithms/random_routing.md`.

## How a turn is routed

Each request draws a target from `targets` using `weights`. Turns in the same conversation can land on different models. Short runs will not match the weights exactly.

Omit `weights` for an even split. A weight of `0` disables that target. Optional `seed` makes the sequence repeatable for tests; omit it for live traffic.

## Fields (out of the box)

| Key | Required | Values / notes |
|-----|----------|----------------|
| `type` | yes | `random` |
| `targets` | yes | List of target table names (unique, two or more) |
| `weights` | no | One non-negative number per target, same order. Need not sum to 1. |
| `seed` | no | Integer. Same seed plus same request order repeats the picks. |

## Use cases

### A/B a new model

Send most traffic to the current model, a slice to a candidate. Compare quality and cost on real prompts. Do not use this when the **same** thread must stay on one model (chat UX will flip mid-conversation). For sticky sessions, use [llm-classifier](llm-classifier.md) with `session_affinity` or [passthrough](passthrough.md).

```toml
[routes.ab_test]
id = "ab-test"
type = "random"
targets = ["strong", "weak"]
weights = [1, 9]
```

About 10% strong, 90% weak.

### Eval and benchmark baselines

Fixed mix so a harness hits both models without writing two client configs. Set `seed` so a replay is comparable.

Good fits: offline eval, load tests, "what is p50 latency on this mix."

Poor fits: debug loops (you want progress signals, not a coin flip). Use [stage-router](stage-router.md).

```toml
[routes.bench]
id = "bench"
type = "random"
targets = ["strong", "weak"]
weights = [1, 1]
seed = 42
```

### Cost experiments

Raise the cheap weight until quality drops too far. This is a traffic knob, not a per-turn brain. If quality must follow "stuck vs producing," use [stage-router](stage-router.md) instead.

### Canary a self-hosted target

Keep most traffic on a known cloud model; send a small weight to a local server you are trying. If the local target is down, that slice fails; random does not fail over.

## When not to use this type

- Tool-aware capable vs efficient: [stage-router](stage-router.md).
- Prompt looks hard, send to strong: [llm-classifier](llm-classifier.md).
- Cheap until the run is actually failing: [escalation](escalation.md).
- Always one model: [passthrough](passthrough.md).
