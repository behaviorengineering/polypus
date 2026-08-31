# Escalation routing

Escalation is `type = "llm_classifier"` with `mode = "escalation"`. Every conversation starts on the **weak** target. A judge reads how the **completed weak turn** went. After enough consecutive "escalate" verdicts, the session **latches** to the strong target and skips the judge.

Unlike [capability classification](llm-classifier.md), this rates work the weak model actually did, not a guess about a fresh prompt.

Upstream: `providers/switchyard/docs/routing_algorithms/escalation_router_routing.md`.

## How a turn is routed

On an unlatched session:

1. Call weak, buffer the reply.
2. Ask the judge about that completed turn.
3. Escalate verdict: increment a streak. Decline: reset streak to 0.
4. If streak is still below `confirmations`, serve the buffered weak reply (weak + judge billed).
5. If streak reaches `confirmations`, discard the weak reply and serve strong (weak + judge + strong billed for that turn).

Latched sessions go straight to strong, no judge.

Judge timeout or bad JSON fails **open**: serve the buffered weak reply and **hold** the streak (do not latch on a judge failure).

`confirmations` of 2 or more needs a session id (`x-switchyard-session-id`). Without one, the streak never accumulates and the route never latches.

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

## Use cases

### Long agent runs that are usually easy

Most tasks a small model can finish. A few go off the rails (loops, drift, repeated errors). You want rescue **after** evidence, not a first-token capability guess.

Good fits: overnight agent jobs, repo-wide mechanical edits, "keep going until the suite is green" with a cheap default.

Poor fits: you need the strong model on turn one (unknown outage). Use [stage-router](stage-router.md) `capable_first` or [passthrough](passthrough.md). Poor fits: no session id and `confirmations > 1` (latch never happens).

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

Stage-router uses free tool heuristics. Escalation pays a judge on every unlatched turn. Use escalation when tool signals are weak (not a coding agent) but you still see "the cheap model is rambling" in the transcript.

If the client **is** a coding agent with tests and edits, prefer [stage-router](stage-router.md); it does not add a judge call on every turn.

### Latch and stay

Once confirmed, later turns skip the judge. Good when a rescued session should finish on the strong model. Bad when you want to drop back to cheap after recovery (stage-router can de-escalate; this latch does not).

## When not to use this type

- Tool WRONG vs PROGRESS without a judge: [stage-router](stage-router.md).
- Classify the opening prompt only: [llm-classifier](llm-classifier.md).
- A/B split: [random](random.md).
- Never leave the strong model: [passthrough](passthrough.md).
