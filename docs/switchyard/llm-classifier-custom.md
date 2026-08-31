# LLM classifier (custom)

`type = "llm_classifier"` with `mode = "custom"` lets the judge **name** a leaf. You define two or more target table names, a JSON Schema for the verdict, and a policy that reads a string out of that JSON.

This is not capability mode. There is no `weak_target` / `strong_target` and no `p_solve` threshold. Sibling pages: [capability](llm-classifier.md), [escalation](escalation.md).

Upstream: `providers/switchyard/docs/routing_algorithms/llm_classifier_routing.md` (Custom multi-target routing).

## How a turn is routed

1. Switchyard calls `classifier_target` with your `prompt` and `response_schema` (schema goes through structured output; do not paste it into the prompt).
2. The judge must return schema-valid JSON in assistant `content`. Reasoning fields are ignored.
3. Policy `target_selector` resolves `selector` (a JSON Pointer) to a string, for example `/decision/target`.
4. If that string is one of `targets`, that leaf serves the user turn.
5. Missing, non-string, unknown name, empty `content`, or judge failure uses `default_target`.

`{{RESPONSE_SCHEMA}}` in `prompt` is rejected at config load.

`session_affinity = true` reuses the first named target for the session (`x-switchyard-session-id`). `message_hash_fallback` keys off the first user message when that header is missing.

The `enum` (or allowed values) in the schema should match `targets`. Those names are **target table names** (`[targets.fast]`), not provider model ids.

## Fields (out of the box)

| Key | Required | Values / notes |
|-----|----------|----------------|
| `type` | yes | `llm_classifier` |
| `mode` | yes | `custom` |
| `classifier_target` | yes | Judge (not a client-facing model) |
| `targets` | yes | List of target table names (two or more, unique) |
| `default_target` | yes | Fallback; must be one of `targets` |
| `prompt` | yes (in practice) | How to choose. Do not embed the schema. |
| `response_schema` | yes | JSON Schema string (TOML literal) |
| `policy.type` | yes | `target_selector` |
| `policy.selector` | yes | JSON Pointer to a string field |
| `session_affinity` | no | Stick to the first named target for the session |
| `recent_turn_window` | no | Conversation span the judge sees |
| `max_output_tokens` | no | Default `4096`. Room for the JSON object. |

## Use cases

### More than two cost or quality tiers

Capability mode is only weak vs strong. Custom can be a ladder: `fast`, `balanced`, `reasoning`, `premium`. The judge picks a name. A bad verdict hits `default_target` (usually the safest or most expensive).

Good fits: a public "auto" route in front of several sizes; "chat vs code vs long-context" when those are different targets.

Poor fits: two models and a probability of success. Use [capability](llm-classifier.md). Poor fits: tool-progress (errors, spinning, writes). The judge still sees **text**, not stage-router axes. Use [stage-router](stage-router.md).

```toml
[routes.smart]
id = "smart"
type = "llm_classifier"
mode = "custom"
classifier_target = "classifier"
targets = ["fast", "balanced", "reasoning", "premium"]
default_target = "premium"
prompt = """
Choose the best configured target for this request.
Return JSON matching the response schema supplied with the request.
"""
response_schema = '''
{
  "type": "object",
  "properties": {
    "decision": {
      "type": "object",
      "properties": {
        "target": {
          "type": "string",
          "enum": ["fast", "balanced", "reasoning", "premium"]
        }
      },
      "required": ["target"],
      "additionalProperties": false
    }
  },
  "required": ["decision"],
  "additionalProperties": false
}
'''

[routes.smart.policy]
type = "target_selector"
selector = "/decision/target"
```

Those four names must exist as `[targets.fast]`, `[targets.balanced]`, and so on. Clients send `model: "smart"` (the route `id`).

### Domain or skill buckets

Put buckets in the schema enum (`code`, `sql`, `general`) that map 1:1 to target tables. Point `selector` at that string. Do not ask the judge to return `cf_local/@cf/...`.

### Sticky N-way session

Set `session_affinity = true` so a thread that started on `reasoning` does not jump to `fast` on the next turn. Skip affinity if every turn should be re-classified.

### Fail safe, not fail cheap

Point `default_target` at the strongest or most correct target. A truncated or empty judge reply will use it.

## Polypus YAML

Polypus can emit this route from `~/.config/polypus/config.yaml`. Leaf ids are `backend_id/downstream-model` and must be allow-listed. Target short names (`fast`, `premium`) must be unique across the whole config against every emitted Switchyard target table, including other classifiers' `{name}_classifier` tables and stage_router `{name}_capable` / `{name}_efficient` tables. The judge leaf is `classifier`; Switchyard receives it as `{router}_classifier`. `response_schema` must be a non-empty JSON object. `message_hash_fallback: true` requires `session_affinity: true`.

```yaml
routers:
  smart:
    capability: chat
    route:
      type: llm_classifier
      mode: custom
      classifier: cf_local/@cf/zai-org/glm-4.7-flash
      targets:
        fast: lm_studio/ornith-1.0-9b
        premium: cf_local/@cf/google/gemma-4-26b-a4b-it
      default_target: premium
      prompt: |
        Choose the best configured target for this request.
        Return JSON matching the response schema supplied with the request.
      response_schema: |
        {"type":"object","properties":{"decision":{"type":"object","properties":{"target":{"type":"string","enum":["fast","premium"]}},"required":["target"],"additionalProperties":false}},"required":["decision"],"additionalProperties":false}
      policy:
        type: target_selector
        selector: /decision/target
      session_affinity: true
```

Public id: `router/smart`. Restart the gateway after edits so Polypus regenerates `~/.cache/polypus/switchyard/routes.toml`.

## When not to use custom

- Exactly two leaves and a p(success) forecast: [capability](llm-classifier.md).
- Judge **after** the weak model already replied: [escalation](escalation.md).
- Tool WRONG vs PROGRESS, no judge: [stage-router](stage-router.md).
- Fixed percentages, no judge: [random](random.md).
- Always one model: [passthrough](passthrough.md).
