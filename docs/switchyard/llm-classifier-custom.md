# LLM classifier (custom) lets the judge name the target

`type = "llm_classifier"` with `mode = "custom"` lets the judge name a leaf. You define two or more target table names, a JSON Schema for the verdict, and a policy that reads a string out of that JSON.

Custom mode has no `weak_target` / `strong_target` and no `p_solve` threshold, which sets it apart from [capability](llm-classifier.md) mode. The other sibling page is [escalation](escalation.md).

The upstream page is `providers/switchyard/docs/routing_algorithms/llm_classifier_routing.md`, under Custom multi-target routing.

## How a turn is routed

1. Switchyard calls `classifier_target` with your `prompt` and `response_schema`. The schema goes through structured output, so do not paste it into the prompt.
2. The judge must return schema-valid JSON in assistant `content`, and Switchyard ignores reasoning fields.
3. The `target_selector` policy resolves `selector` (a JSON Pointer such as `/decision/target`) to a string.
4. If that string is one of `targets`, that leaf serves the user turn.
5. A missing or non-string value, an unknown name, empty `content` or a judge failure sends the turn to `default_target`.

Config load rejects `{{RESPONSE_SCHEMA}}` in `prompt`.

`session_affinity = true` reuses the first named target for the session (`x-switchyard-session-id`). When that header is missing, `message_hash_fallback` keys off the first user message.

The `enum` (or allowed values) in the schema should match `targets`. Those names are **target table names** such as `[targets.fast]`, not provider model ids.

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

## Where the custom classifier fits

### More than two cost or quality tiers

Capability mode only chooses weak or strong, while custom mode can run a ladder such as `fast`, `balanced`, `reasoning` and `premium`. The judge picks a name, and a bad verdict falls to `default_target`, which is usually the safest or most expensive target.

It suits a public "auto" route in front of several sizes, or "chat vs code vs long-context" when those are different targets.

For two models and a probability of success, use [capability](llm-classifier.md) instead. Custom mode also fits tool progress (errors, spinning, writes) poorly, because the judge still sees text, not stage-router axes. Use [stage-router](stage-router.md) for that.

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

Those four names must exist as `[targets.fast]`, `[targets.balanced]` and so on. Clients send `model: "smart"`, which is the route `id`.

### Domain or skill buckets

Put buckets such as `code`, `sql` and `general` in the schema enum, each mapping 1:1 to a target table, and point `selector` at that string. Do not ask the judge to return `cf_local/@cf/...`.

### Sticky N-way session

Set `session_affinity = true` so a thread that started on `reasoning` does not jump to `fast` on the next turn. Skip affinity if every turn should be re-classified.

### Make the fallback the strongest target

Point `default_target` at the strongest or most correct target, because a truncated or empty judge reply will use it.

## Defining the route in Polypus YAML

Polypus can emit this route from `~/.config/polypus/config.yaml`. Leaf ids take the form `backend_id/downstream-model`, and each one must be allow-listed.

Target short names such as `fast` and `premium` must be unique across the whole config. That check runs against every emitted Switchyard target table, including other classifiers' `{name}_classifier` tables and stage_router `{name}_capable` / `{name}_efficient` tables.

The judge leaf is `classifier`, and Switchyard receives it as `{router}_classifier`. `response_schema` must be a non-empty JSON object, and `message_hash_fallback: true` requires `session_affinity: true`.

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

The public id is `router/smart`. Restart the gateway after edits, so Polypus regenerates `~/.cache/polypus/switchyard/routes.toml`.

## When not to use custom

- Exactly two leaves and a p(success) forecast: [capability](llm-classifier.md).
- Judge after the weak model already replied: [escalation](escalation.md).
- Tool WRONG vs PROGRESS, no judge: [stage-router](stage-router.md).
- Fixed percentages, no judge: [random](random.md).
- Always one model: [passthrough](passthrough.md).
