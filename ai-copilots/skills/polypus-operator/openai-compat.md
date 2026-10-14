# OpenAI-compatible gateway

**Moral:** Polypus is the contract clients see. Every extension is an adapter behind that contract.

## When to load

Load when editing `internal/gateway`, `internal/clients`, Batch retrieve/create, chat thinking translation, or public JSON on `/v1/*`.

## Core constraints

**CONSTRAINT:** Public HTTP paths and JSON MUST match OpenAI client expectations for the surfaces Polypus implements (`/v1/chat/completions`, `/v1/files`, `/v1/batches`, Batch status strings we document).

- Enforcement: Review handler `writeJSON` payloads and OpenAI docs for the endpoint.
- Violation: STOP, add or fix an adapter; MUST NOT expose upstream shapes on the wire.

CORRECT:

```text
GET /v1/batches/{id} → 200 PublicBatch (status in_progress) while Workers AI poll is empty.
```

PROHIBITED:

```text
GET /v1/batches/{id} → 502 because cloudflare.parsePollResult failed.
```

**CONSTRAINT:** Extension-specific fields MUST stay in adapters (`internal/clients/cloudflare`, `internal/clients/gemini`, gateway helpers such as `applyChatThinking`, Batch poll/finalize).

- Enforcement: Grep public structs for `cf_`, `queueRequest`, `generateContent`.
- Violation: STOP, move fields to internal `BatchMeta` or client-only types; expose `Public()` or equivalent at the handler.

**CONSTRAINT:** Create MUST persist the OpenAI resource before HTTP 200. Retrieve MUST return last-known metadata when upstream refresh fails with unavailable, timeout, rate limit, or breaker open.

- Enforcement: Batch retrieve tests (`TestBatchRetrievePollUnavailableKeepsInProgress`); slog warn on refresh miss.
- Violation: STOP, use `isBatchRefreshMiss` pattern; MUST NOT call `writeHandlerError` for transient poll misses when the job exists on disk.

**CONSTRAINT:** MUST NOT fake upstream capabilities. Workers AI Batch cancel stays HTTP 501 (`CodeUnimplemented`).

- Enforcement: `serveBatchCancel` and operator docs.
- Violation: STOP, return 501; MUST NOT set `status=cancelled` without upstream support.

**CONSTRAINT:** MUST NOT tell operators or clients to call Cloudflare, Gemini, or OpenRouter URLs directly for product traffic.

- Enforcement: Skills and examples use `http://127.0.0.1:1320` or configured gateway base URL.
- Violation: STOP, rewrite examples to gateway-only.

## Chat streaming (`stream: true`)

Polypus normalizes every provider stream (Bifrost Gemini leaf and HTTP proxy backends) to the OpenAI chat completion chunk shape clients expect.

**CONSTRAINT:** Mid-stream `choices[]` entries MAY include `"finish_reason": null` when the upstream omitted the key (openai-go and similar parsers require the field).

**CONSTRAINT:** After a choice reports a string `finish_reason` (for example `"stop"`), Polypus MUST NOT forward another chunk with non-empty `choices` for that choice index unless the chunk is the legal usage trailer rewritten to `"choices": []` with the same `usage` object.

**CONSTRAINT:** Illegal post-stop content chunks are dropped. When `POLYPUS_FAILURE_DUMP_DIR` (olly dump dir) is set, dropped chunks are also written under that directory for post-mortem.

**CONSTRAINT:** `extra_fields` (Bifrost provider metadata such as `RawResponse`) MUST NOT appear on the public SSE wire. That JSON is recorded on the `polypus.chat` span as `polypus.bifrost.stream_chunk` events instead.

Proxy backends that already emit legal OpenAI SSE pass through unchanged except for the shared sanitizer rules above.

## Related shards

- [thinking-policy.md](thinking-policy.md) (OpenAI `reasoning.effort` inbound, per-extension outbound).
- [config-reference.md](config-reference.md) (`batch_backend`, capabilities).
