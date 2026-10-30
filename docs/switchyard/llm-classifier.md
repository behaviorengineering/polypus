# LLM classifier (capability) predicts whether the weak model can finish

`type = "llm_classifier"` with `mode = "capability"` calls a **judge** on the request. The judge estimates whether the weak target can finish the task, as a `p_solve` plus a capability boundary. Switchyard then sends the user turn to `weak_target` or `strong_target` only.

The judge does not name a model. In the other classifier modes, the judge names one of many targets ([custom](llm-classifier-custom.md)), or the route starts on weak and latches to strong ([escalation](escalation.md)).

This mode makes its prediction before trusting tool-progress heuristics, or without them. It costs an extra model call per classified turn, unless session affinity reuses the first pick.

The upstream page is `providers/switchyard/docs/routing_algorithms/llm_classifier_routing.md`.

## How a turn is routed

The classifier target must return schema-valid JSON in assistant `content`, with `p_solve`, `capability_boundary` and related fields. Empty or unparseable `content` fails open to `strong_target`. Switchyard does not read provider reasoning fields.

Do not put `{{RESPONSE_SCHEMA}}` in `prompt`, because Switchyard sends the schema through structured output. Config load rejects that placeholder.

Weak wins when `p_solve` is at least the threshold for that boundary:

- `supported`: `base_threshold`
- `uncertain` / `unmatched`: `base_threshold + threshold_step`
- `unsupported`: `base_threshold + 2 * threshold_step`

`session_affinity = true` keeps the first pick for later turns in the session, and `x-switchyard-session-id` is the preferred session key. When that header is missing, `message_hash_fallback` keys off the first user message, which collides if many sessions share the same opener.

## Fields (out of the box)

| Key | Required | Values / notes |
|-----|----------|----------------|
| `type` | yes | `llm_classifier` |
| `mode` | yes | `capability` |
| `classifier_target` | yes | Judge target (not a client-facing model) |
| `strong_target` | yes | Stronger leaf |
| `weak_target` | yes | Cheaper leaf |
| `base_threshold` | yes | `[0, 1]`. Floor `p_solve` for a supported task to go weak. |
| `threshold_step` | no | Default `0`. Added per boundary step. |
| `session_affinity` | no | Default `false`. Stick to the first pick. |
| `message_hash_fallback` | no | Default `false`. Needs affinity. |
| `recent_turn_window` | no | How much conversation the judge sees. |
| `prompt` | no | Replaces the packaged capability prompt. Packaged JSON fields stay required. |

## Where the capability classifier fits

### Cheap model if the task looks easy

Take an inbox of mixed questions, where trivia and summaries go to weak, and architecture and multi-file reasoning go to strong. The judge reads the user text, not whether tools are succeeding.

It suits helpdesks, "ask anything" gateways, and routing between a small local model and a large cloud model when you have no tool trace yet.

It fits poorly for a coding agent already emitting test failures, because capability mode does not score those traces. Use [stage-router](stage-router.md) or [escalation](escalation.md) for that. For more than two leaves, use [custom](llm-classifier-custom.md).

```toml
[routes.smart]
id = "smart"
type = "llm_classifier"
mode = "capability"
classifier_target = "classifier"
strong_target = "strong"
weak_target = "weak"
base_threshold = 0.5
threshold_step = 0.1
session_affinity = true
```

### Sticky session after the first verdict

A chat product should not flip models mid-thread, and affinity holds the first weak or strong choice. Random routing cannot do this.

Affinity fights you when you want later turns to escalate as tools fail. Use [stage-router](stage-router.md) or [escalation](escalation.md) in that case.

### Custom rubric for your weak model

The packaged prompt may not match what your small model can do, so set `prompt` to describe that model. Keep structured output, and do not paste the JSON schema into the prompt. The verdict schema stays `p_solve` / `capability_boundary`.

## When not to use capability

- More than two named targets: [custom](llm-classifier-custom.md).
- Judge after the weak model already replied: [escalation](escalation.md).
- Tool WRONG vs PROGRESS, no extra judge call: [stage-router](stage-router.md).
- Fixed percentages, no judge: [random](random.md).
- Always one model: [passthrough](passthrough.md).
