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

## Related shards

- [thinking-policy.md](thinking-policy.md) (OpenAI `reasoning.effort` inbound, per-extension outbound).
- [config-reference.md](config-reference.md) (`batch_backend`, capabilities).
