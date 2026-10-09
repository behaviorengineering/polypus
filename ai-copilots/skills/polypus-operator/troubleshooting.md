# Polypus troubleshooting

Symptom-first table. Run health before chasing downstream errors.

## Quick probes

```bash
curl -sf http://127.0.0.1:1320/health | jq .
curl -sf http://127.0.0.1:1320/health/backends | jq .   # upstream probe; may be slow
curl -sf http://127.0.0.1:1320/health/upstreams | jq .  # circuit breaker board (no dials)
curl -sS http://127.0.0.1:1320/v1/apis | jq '.apis[].id'
curl -sS http://127.0.0.1:1320/v1/apis/openai/models | jq '.data | length'
curl -sS 'http://127.0.0.1:1320/v1/apis/openai/models?view=inventory' | jq '.data | length'   # cf_local catalog
curl -sS http://127.0.0.1:1320/v1/apis/systemone/models | jq '.data | length'
curl -sf http://127.0.0.1:1234/v1/models | jq '.data | length'   # LM Studio
```

## Symptom → cause → fix

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| Connection refused on `:1320` | Gateway not running | `make serve` |
| process-compose shows gateway crash | Config error, port in use | Read TUI logs; check `POLYPUS_PORT`; `make serve-down` then retry |
| `model_not_allowed` on POST | Id not in `models.allow` | Add to `~/.config/polypus/config.yaml` or use correct `backend_id/` prefix |
| Model in host job config but smoke fails | Allow-list drift | Align Polypus `models.allow` with host job model ids |
| Empty `message.content`, job XML fail | Thinking on; text in `reasoning_content` | [thinking-policy.md](thinking-policy.md); host L2 harness if available |
| Chat hits MLX `:1322` | Wrong `chat_backend.default` or missing prefix | Set `chat_backend`; use `cf_local/` or `lm_studio/` prefix |
| cf_local missing from `GET /v1/apis/openai/models` | Catalog sync failed or credentials missing | Probe `?view=inventory`; check `CF_AI_API_KEY` / `CF_ACCOUNT_ID` |
| cf_local 401/403 | Missing CF credentials | `CF_AI_API_KEY`, `CF_ACCOUNT_ID` |
| LM Studio errors | Server not started | User starts LM Studio on `:1234` |
| OCR/embed fails, chat OK | `lm_studio` down or model not allowed | Probe `:1234`; check embed allow-list |
| Timeout mid-request | Hop shorter than thinking | Raise `timeouts.chat_thinking` or send `X-Polypus-Timeout` within max |
| Smoke passes, host job fails | L3 fixture or different model | Run host L3 harness for that model when available |
| TTS works, STT fails | STT model not allowed or wrong backend | Check `stt_backend.default` and allow list |
| `router/…` returns 503 | Switchyard down or not ready | `/health/backends` → `switchyard`; `make serve-down && make serve`; `make smoke-router` |
| Leaf or Switchyard dial returns 503 after recent 5xx | Upstream circuit breaker open (`internal/gateway/upstream.Board`) | Wait for the open window (~30s) or fix the upstream; clients MAY budget-retry 503, MUST NOT expect Polypus to sleep-retry chat (see SKILL.md resilience ownership) |
| Smoke chat/TTS/STT fail in ~1–3 ms with `circuit breaker is open` on `cf_local` | Breaker still open in a long-lived gateway after earlier CF/auth failures | Fix credentials (`polypus secret set` / env), then `make serve-down && make serve` (breaker state is in-process only). `/health/backends` no longer trips the production breaker. Compare `/health/upstreams` (`state=open`) vs Cloudflare throttle in dump (`polypus.failure.layer=cloudflare`). |
| `POST /v1/chat/completions` returns **429** JSON (`error.type=rate_limit_error`) | Cloudflare Workers AI throttled the account or model (quota 3036, capacity 3040, edge 1015) | Use `curl -i` on the failing request: honor `Retry-After`, `Cf-Ray`, and any `x-ratelimit-*` on the **HTTP response** (not only success `extra_fields`). `error.code` may be `3036`/`3040`/`1015` when Workers JSON survived classification; otherwise `rate_limited`. Do not treat as Polypus breaker. |
| Same path returns **503** JSON (`error.code=polypus_breaker`, `polypus.failure.layer=polypus_breaker`) | Polypus `gobreaker` refused the dial (open or half-open limit) | Polypus will not dial until the open window ends; honor `Retry-After: 30`. `/health/upstreams` is a live snapshot only, not the response body. |
| `router/…` returns 502 | Switchyard up but chat hop failed | Check Switchyard logs; upstream leaf error (distinct from 503 unavailable) |
| `router/…` unknown / 400 | Router not in `routers:` or typo | Check `config.yaml` `routers:`; probe `GET /v1/apis/openai/models` for `router/<name>` |
| Passthrough router fails, composed OK | Leaf allow-list or backend | Validate `route.target` leaf in `models.allow` |

## Logs and traces

Agent decision order when a job fails:

1. `GET http://127.0.0.1:1320/debug/failures/<trace_id>` (or read `logs/inference-failures/<trace_id>.json` on the gateway host)
2. On child spans, read `polypus.failure.layer` (`cloudflare`, `polypus_breaker`, `switchyard`, `leaf`)
3. For `router/…` models, also `GET http://127.0.0.1:4000/debug/failures/<trace_id>` for Switchyard retry attempts
4. `GET /health/upstreams` when layer is `polypus_breaker` but `/health/backends` is green
5. Phoenix http://127.0.0.1:6006 when OTLP was up (same TraceID should include `switchyard.request` and `libsy.upstream_attempt`)

| Resource | Location |
|----------|----------|
| Phoenix UI | http://127.0.0.1:6006 |
| Polypus failure dump API | `GET :1320/debug/failures/<trace_id>` |
| Switchyard failure dump API | `GET :4000/debug/failures/<trace_id>` |
| Inference failure JSON (disk) | `POLYPUS_FAILURE_DUMP_DIR` / `SWITCHYARD_FAILURE_DUMP_DIR` (default `logs/inference-failures/`) |
| Gateway trace noise | Set `POLYPUS_OTEL_SKIP_PATHS=/health,/health/backends,/health/upstreams,/v1/models,/v1/apis,/v1/apis/openai/models,/v1/apis/systemone/models` |

## Restart after config change

1. Edit `~/.config/polypus/config.yaml` (allow-list, defaults, timeouts).
2. In process-compose TUI: restart `gateway`.
3. Or: `make serve-down` then `make serve`.

## Client reminders

- Downstream apps must not store remote cloud inference URLs.
- No semantic cache of sensitive narration on the gateway.
- Callers use `POLYPUS_BASE_URL` only.
- **HTTP 429** with OpenAI `rate_limit_error`: Cloudflare throttled the Workers AI hop; retry with backoff using HTTP response headers (`curl -i` for `Retry-After`, `Cf-Ray`, `x-ratelimit-*`). Check `error.code` for Workers codes when present.
- **HTTP 503** with `error.code=polypus_breaker` (and matching `polypus.failure.layer` in JSON): Polypus refused to dial because the upstream circuit breaker is open; honor `Retry-After` (open-window seconds). Clients MAY retry after that delay; Polypus does not sleep-retry on behalf of callers.
