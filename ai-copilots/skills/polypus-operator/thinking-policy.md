# Polypus thinking policy

Use when chat returns empty `message.content`, DSPy XML parse fails, or Gemma/GLM behavior differs from docs.

## Cloudflare Gemma 4

- Workers AI exposes thinking via `chat_template_kwargs.enable_thinking` (nested in request body).
- CF schema often defaults thinking **on** for Gemma 4.
- When thinking is on, the model may put the answer in `reasoning_content` and leave `content` empty.

## Polypus gateway behavior

- Disables thinking **unless** the client explicitly enables it.
- On successful responses, merges `reasoning_content` into `content` when `content` is empty.
- Thinking requests use the `chat_thinking` timeout bucket (default 600s in config).

## Structured XML clients

- Some downstream jobs expect **parseable tagged XML in `message.content`**.
- Host transports may force `enable_thinking: false` for Gemma and GLM before requests reach Polypus.
- **Production structured jobs:** thinking off. Do not enable thinking globally to "fix" XML jobs.

## When to turn thinking on

| Scenario | Thinking |
|----------|----------|
| XML / DSPy structured modules | Off (typical) |
| Interactive debug / smoke | Optional probe with thinking on |
| Native JSON schema / grammar (future) | Case-by-case; some backends break grammar when thinking off |

## Debug steps

1. Run L1 smoke: `make smoke-chat`
2. Run host L2 harness if XML jobs fail.
3. Inspect Phoenix trace for request body and response fields.
4. Read failure dump: `logs/inference-failures/<trace_id>.json`

## Raw curl probe (thinking off)

```bash
curl -sS http://127.0.0.1:1320/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "cf_local/@cf/google/gemma-4-26b-a4b-it",
    "messages": [{"role": "user", "content": "Reply with exactly: ok"}],
    "max_tokens": 32
  }' | jq '.choices[0].message'
```

Expect non-empty `content`. If empty, check gateway merge logic and backend response shape.

## Code references

- Polypus: `internal/gateway/chat.go` (thinking disable/merge)
- Host apps may add transport layers that force thinking off for structured paths
