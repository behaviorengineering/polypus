---
name: polypus-backend-inventory
description: >-
  Polypus gateway backend model inventory: upstream list fetch on serve warmup,
  Allow model (inventory check), and refresh interval. Use when adding backends,
  extension gemini, models.allow, models.sync, inventoryForBackend, or
  POST /v1/admin/models/allow.
---

# Polypus backend inventory

**Moral:** `models.allow` gates inference. Inventory for Allow model and `?view=inventory` MUST come from the provider's upstream catalog when `models.sync` is true (default).

## When to load

- Adding or changing a `backends.*` entry, `extension`, or `models.sync` / `models.allow`
- Editing `inventoryForBackend`, `warmModelsInventory`, or `POST /v1/admin/models/allow`
- Adding `internal/clients/<extension>` for a new remote backend
- Debugging `model_not_in_inventory` or empty Gemini/OpenRouter catalogs

## CONSTRAINT: Upstream catalog when sync is on

When `models.sync` is true (default via `BackendModels.ShouldSync()`), `inventoryForBackend` MUST fetch the model list from that backend's upstream list API. Serve warmup (`warmModelsInventory`), `POST /v1/admin/models/allow`, and the periodic refresh (`POLYPUS_MODELS_REFRESH_INTERVAL`, default 30m) MUST use that fetched list (via `invCache` after live GET).

- Enforcement: `rg 'IsGeminiExtension|IsCloudflareExtension|fetchBackendModels' internal/gateway/models.go`; each extension with sync on has a live path; grep MUST NOT show allow-only return before upstream fetch when `ShouldSync()` is true.
- Violation: STOP, add `internal/clients/<ext>` `ListModelsStrict` (or OpenAI-compat GET), wire gateway inventory branch, add httptest and admin-allow test, re-verify.

CORRECT:

```text
Cloudflare: Model Search via internal/clients/cloudflare ListModelsStrict
OpenRouter: GET {base}/v1/models?output_modalities=all via fetchBackendModelsProtected
Gemini: GET https://generativelanguage.googleapis.com/v1beta/openai/models with Bearer (chat base_url stays empty)
```

PROHIBITED:

```go
if b.IsGeminiExtension() {
    if modelsCfg != nil && modelsCfg.HasAllowGate() {
        return syntheticModels(b.ID, modelsCfg.Allow) // only source while sync true
    }
}
```

## CONSTRAINT: Allow list is not the catalog

MUST NOT return `syntheticModels(allow)` as the only inventory while `models.sync` is true. YAML `models.allow` is the inference gate. Synthetic allow is a fallback after failed upstream fetch (then disk cache), not a substitute for ListModels.

- Enforcement: Admin allow test adds a slug present in httptest upstream but absent from YAML allow; expect HTTP 200.
- Violation: STOP, implement live list branch, re-run test.

## CONSTRAINT: List URL separate from chat base_url

Gemini chat MUST keep empty `base_url` (Bifrost `generateContent`). Inventory MUST use the OpenAI-compat models URL in `internal/clients/gemini`, not `BackendDef.BaseURL` for chat.

- Enforcement: `validateGeminiBackendURL` and operator docs say omit `base_url` for chat; catalog client has its own default list URL.
- Violation: STOP, move list URL into gemini client; do not set generativelanguage host on backend for chat.

## CONSTRAINT: New extension checklist

Adding `extension: <name>` MUST include:

1. `internal/clients/<name>/` with `ListModelsStrict(ctx)` (or documented OpenAI-compat list)
2. httptest proving Bearer/auth and response parse
3. Gateway branch in `inventoryForBackend` mirroring Cloudflare (live, merge allow synthetics, `invCache.put`, cache, synthetic fallback)
4. `TestAdminAllow*` or equivalent: live slug not in YAML allow can be added via POST allow

- Enforcement: PR touches `internal/gateway/models.go` and new client package together.
- Violation: STOP, complete checklist before merge.

## Operator probes

```bash
curl -sS 'http://127.0.0.1:1320/v1/apis/openai/models?view=inventory' | jq '.data[].id'
```

Landing `GET /` form posts `POST /v1/admin/models/allow` with `{"backend":"<id>","model":"<downstream>"}`. Model MUST appear in inventory for that backend.

## Related

- [polypus-operator/SKILL.md](../polypus-operator/SKILL.md) (health, allow flows)
- [polypus-operator/config-reference.md](../polypus-operator/config-reference.md) (inventory vs enabled, env vars)
