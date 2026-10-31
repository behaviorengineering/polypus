# Escalation routing starts cheap and latches to strong on evidence

Escalation is `type = "llm_classifier"` with `mode = "escalation"`, and every conversation starts on the weak target. A judge reads how the completed weak turn went. After enough consecutive "escalate" verdicts, the session **latches** to the strong target and skips the judge.

[Capability classification](llm-classifier.md) guesses from a fresh prompt, whereas escalation rates **work the weak model actually did**.

The upstream page is `providers/switchyard/docs/routing_algorithms/escalation_router_routing.md`.

## How a turn is routed

On an unlatched session, Switchyard takes these steps:

1. It calls weak and buffers the reply.
2. It asks the judge about that completed turn.
3. An escalate verdict increments a streak, and a decline resets the streak to 0.
4. If the streak is still below `confirmations`, it serves the buffered weak reply (weak + judge billed).
5. If the streak reaches `confirmations`, it discards the weak reply and serves strong (weak + judge + strong billed for that turn).

A latched session goes straight to strong, with no judge.

A judge timeout or bad JSON **fails open**, so Switchyard serves the buffered weak reply and holds the streak. It does not latch on a judge failure.

A `confirmations` value of 2 or more needs a session id (`x-switchyard-session-id`). Without one, the streak never accumulates and the route never latches.

## Fields (out of the box)

| Key | Required | Values / notes |
|-----|----------|----------------|
| `type` | yes | `llm_classifier` |
| `mode` | yes | `escalation` |
| `classifier_target` | yes | Judge |
| `strong_target` | yes | Rescue model |
| `weak_target` | yes | Default model |
| `prompt` | no | Replaces the packaged trajectory-judge prompt |
| `escalation.confirmations` | no | Default `2`. Consecutive escalate verdicts to latch. >= 1. |
| `escalation.recent_turn_window` | no | Default `28`. Trailing messages shown to the judge. |
| `escalation.window_message_chars` | no | Default `500`. Per-message cap in that window. >= 50. |

A bare `escalation = {}` is valid and uses those defaults.

## Where escalation fits

### Long agent runs that are usually easy

A small model can finish most tasks, and only a few go off the rails through loops, drift or repeated errors. Here you want rescue after the evidence appears, rather than a first-token capability guess.

It suits overnight agent jobs, repo-wide mechanical edits, and "keep going until the suite is green" with a cheap default.

It fits poorly when you need the strong model on turn one, as in an unknown outage. Use [stage-router](stage-router.md) `capable_first` or [passthrough](passthrough.md) there. It also fits poorly with no session id and `confirmations > 1`, because the latch never happens.

```toml
[routes.agent]
id = "agent"
type = "llm_classifier"
mode = "escalation"
classifier_target = "judge"
strong_target = "strong"
weak_target = "weak"
escalation = { confirmations = 2, recent_turn_window = 28, window_message_chars = 500 }
```

### Cheaper than scoring every turn with stage-router extras

The stage router uses free tool heuristics, while escalation pays a judge on every unlatched turn. Use escalation when tool signals are weak because the client is not a coding agent, but you still see "the cheap model is rambling" in the transcript.

If the client is a coding agent with tests and edits, prefer [stage-router](stage-router.md), because it does not add a judge call on every turn.

### Latch and stay

Once the streak confirms, later turns skip the judge. That suits a rescued session that should finish on the strong model. It does not suit a session that should drop back to cheap after recovery, because this latch does not de-escalate and the stage router can.

## When not to use this type

- Tool WRONG vs PROGRESS without a judge: [stage-router](stage-router.md).
- Classify the opening prompt only: [llm-classifier](llm-classifier.md).
- A/B split: [random](random.md).
- Never leave the strong model: [passthrough](passthrough.md).
