# Polypus thinking policy

## Client standard (inbound)

- Opt in with OpenAI **`reasoning.effort`** or **`reasoning_effort`**. Nested **`reasoning.effort` wins** when both are set.
- Off values: empty, `none`, `off`, `false`, `0`, **`minimal`**.
- Strop **`Thinking: true`** injects **`reasoning.effort=high`**.
- **`chat_template_kwargs`** and top-level **`enable_thinking`** are **not** client opt-in; they are Workers AI **outbound** fields only.

## Gateway

- **`applyChatThinking`** (`internal/gateway/chat.go`) detects OpenAI effort, emits per `BackendDef.extension`.
- **Gemini:** strip CF kwargs; Gemma 4 off → **`minimal`**, on → **`high`**; Gemini 2.5 uses **`reasoning.max_tokens`** (-1 / 0).
- **Cloudflare:** Gemma/GLM kwargs; DeepSeek off → **`reasoning_effort=none`**.
