# Polypus troubleshooting

Symptom-first table. Run health before chasing downstream errors.

## Quick probes

```bash
curl -sf http://127.0.0.1:1320/health | jq .
curl -sS http://127.0.0.1:1320/v1/models | jq '.data | length'
curl -sf http://127.0.0.1:1323/v1/models | jq '.data | length'   # cf-adapter
curl -sf http://127.0.0.1:1234/v1/models | jq '.data | length'   # LM Studio
```

## Symptom → cause → fix

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| Connection refused on `:1320` | Gateway not running | `make serve` or `make polypus-serve` |
| process-compose shows gateway crash | Config error, port in use | Read TUI logs; check `POLYPUS_PORT`; `make serve-down` then retry |
| `model_not_allowed` on POST | Id not in `models.allow` | Add to `config.yaml` or use correct `backend_id/` prefix |
| Model in `stack/ai.yaml` but smoke fails | Allow-list drift | Align Polypus allow with Consilium job model ids; `bin/stack-doctor diagnose` |
| Empty `message.content`, job XML fail | Thinking on; text in `reasoning_content` | [thinking-policy.md](thinking-policy.md); L2 harness |
| Chat hits MLX `:1322` | Wrong `default_chat_backend` or missing prefix | Set `default_chat_backend`; use `cf_local/` or `lm_studio/` prefix |
| cf-adapter not in health | `INFERENCE_CLOUD_CASE` unset | Set in `stack/.env`; restart Polypus compose |
| cf-adapter 401/403 | Missing CF credentials | `CF_AI_API_KEY`, `CF_ACCOUNT_ID` |
| LM Studio errors | Server not started | User starts LM Studio on `:1234` |
| OCR/embed fails, chat OK | `lm_studio` down or model not allowed | Probe `:1234`; check embed allow-list |
| Timeout mid-request | Hop shorter than thinking | Raise `timeouts.chat_thinking` or send `X-Polypus-Timeout` within max |
| Smoke passes, Consilium job fails | L3 fixture or different model | `bin/stack-doctor model-harness --tier L3 --model '...'` |
| TTS works, STT fails | STT model not allowed or wrong backend | Check `default_stt_backend` and allow list |

## Logs and traces

| Resource | Location |
|----------|----------|
| Phoenix UI | http://127.0.0.1:6006 |
| Inference failure JSON | `logs/inference-failures/<trace_id>.json` |
| Gateway trace noise | Set `POLYPUS_OTEL_SKIP_PATHS=/health,/v1/models` |

## Restart after config change

1. Edit `config.yaml` (allow-list, defaults, timeouts).
2. In process-compose TUI: restart `gateway` (and `cf-adapter` if cloud backends changed).
3. Or: `make serve-down` then `make serve` (Polypus) / `make polypus-down` then `make polypus-serve` (Consilium).

## Breach reminders

- Case apps must not store remote cloud inference URLs.
- No semantic cache of case narration on the gateway.
- Consilium callers use `POLYPUS_BASE_URL` only.
