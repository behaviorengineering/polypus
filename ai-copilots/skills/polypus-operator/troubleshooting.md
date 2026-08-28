# Polypus troubleshooting

Symptom-first table. Run health before chasing downstream errors.

## Quick probes

```bash
curl -sf http://127.0.0.1:1320/health | jq .
curl -sf http://127.0.0.1:1320/health/backends | jq .   # upstream probe; may be slow
curl -sS http://127.0.0.1:1320/v1/models | jq '.data | length'
curl -sS 'http://127.0.0.1:1320/v1/models?view=inventory' | jq '.data | length'   # cf_local catalog
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
| Chat hits MLX `:1322` | Wrong `default_chat_backend` or missing prefix | Set `default_chat_backend`; use `cf_local/` or `lm_studio/` prefix |
| cf_local missing from `/v1/models` | `INFERENCE_CLOUD_CASE` unset or catalog sync failed | Set in process env; probe inventory view; check CF credentials |
| cf_local 401/403 | Missing CF credentials | `CF_AI_API_KEY`, `CF_ACCOUNT_ID` |
| LM Studio errors | Server not started | User starts LM Studio on `:1234` |
| OCR/embed fails, chat OK | `lm_studio` down or model not allowed | Probe `:1234`; check embed allow-list |
| Timeout mid-request | Hop shorter than thinking | Raise `timeouts.chat_thinking` or send `X-Polypus-Timeout` within max |
| Smoke passes, host job fails | L3 fixture or different model | Run host L3 harness for that model when available |
| TTS works, STT fails | STT model not allowed or wrong backend | Check `default_stt_backend` and allow list |
| `router/…` returns 503 | Switchyard down or not ready | `/health/backends` → `switchyard`; `make serve-down && make serve`; `make smoke-router` |
| `router/…` returns 502 | Switchyard up but chat hop failed | Check Switchyard logs; upstream leaf error (distinct from 503 unavailable) |
| `router/…` unknown / 400 | Router not in `routers:` or typo | Check `config.yaml` `routers:`; probe `/v1/models` for `router/<name>` |
| Passthrough router fails, composed OK | Leaf allow-list or backend | Validate `route.target` leaf in `models.allow` |

## Logs and traces

| Resource | Location |
|----------|----------|
| Phoenix UI | http://127.0.0.1:6006 |
| Inference failure JSON | `logs/inference-failures/<trace_id>.json` |
| Gateway trace noise | Set `POLYPUS_OTEL_SKIP_PATHS=/health,/health/backends,/v1/models` |

## Restart after config change

1. Edit `~/.config/polypus/config.yaml` (allow-list, defaults, timeouts).
2. In process-compose TUI: restart `gateway`.
3. Or: `make serve-down` then `make serve`.

## Client reminders

- Downstream apps must not store remote cloud inference URLs.
- No semantic cache of sensitive narration on the gateway.
- Callers use `POLYPUS_BASE_URL` only.
