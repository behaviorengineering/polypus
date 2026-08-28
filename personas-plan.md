# Plan: Named routers (composed model endpoints)

Status: **draft — iterating**  
Related: NVIDIA NeMo Switchyard — **git submodule under Polypus**, invoked as a process  
Last updated: 2026-08-28

## Goal

Expose **named routers** as OpenAI `model` ids of the form `router/<name>`. Each router is a policy over one or more leaf models. Clients bind to the router on Polypus; Polypus **invokes Switchyard** (internal) for composed policies; leaf traffic still hits Polypus backends so security / thinking / Phoenix stay local.

Not an alias to one model. Not a Go reimplementation of libsy.

```text
Client  →  model: "router/investigator"   (:1320 Polypus)
              │
              ▼
       routers.investigator → Switchyard route id "router/investigator"
              │  libsy heuristics / escalate / random
              ▼
       leaf call via Polypus backends
       (lm_studio/…, cf_local/…, …)
```

## Problem today

- Clients must know concrete arms: `cf_local/@cf/…`, `lm_studio/…`.
- Routing is static prefix + capability defaults (`internal/router/registry.go`).
- No place to encode “this role uses weak/strong/local/cloud with policy X.”
- Apps and agents re-implement that policy outside Polypus.

## Design principles

1. **Router = public endpoint** — listed on `/v1/models`, sent as `model: router/<name>` on `:1320`.
2. **Router = composed multi-model policy** — declared in Polypus `routers:`; executed by Switchyard after runtime transform when not passthrough.
3. **Invoke as a process** — run `switchyard-server` as its own long-lived process (process-compose / make serve); Polypus calls it over loopback HTTP. Do not port or embed libsy.
4. **Switchyard lives in-repo as a submodule** — under Polypus `providers/switchyard/` (not a sibling monorepo provider, not `tmp/`).
5. **Config transform at runtime** — Polypus YAML is authoritative; generate Switchyard TOML on start (no dual hand-edited configs).
6. **Backends stay Polypus arms** — generated targets point at Polypus (`http://127.0.0.1:1320`) with leaf model ids.
7. **Escape hatch** — raw `backend_id/downstream` still works; bypasses Switchyard.
8. **Speech stays Polypus** — TTS/STT/voices do not go through Switchyard.
9. **Always-on Switchyard process** — process-compose starts it with the stack (same as Phoenix default). Clients never call it; loopback-only. Empty composed-router set still runs the process (generated TOML may have no `routes.*`).
10. **Wire grammar** — first path segment is kind: `cf_local/…`, `lm_studio/…`, `router/…`. Forbid a backend id named `router`.
11. **Unknown YAML keys fail load** — Polypus config decode uses known-fields (strict) so typos in `routers:` / `switchyard:` never silently drop.

## Repo layout

```text
polypus/                          # this repo (also nested as providers/polypus in monorepos)
  providers/
    switchyard/                   # git submodule → NVIDIA-NeMo/Switchyard (pinned tag)
  backends/
    mlx/                          # existing speech arm
  process-compose.yaml            # gateway + mlx + phoenix + switchyard
  scripts/pc-switchyard.sh        # render YAML→TOML, then exec switchyard-server
  personas-plan.md                # this file (filename historical)
  # generated at runtime, not in git:
  #   ~/.cache/polypus/switchyard/routes.toml
```

`.gitmodules` entry (illustrative):

```gitmodules
[submodule "providers/switchyard"]
	path = providers/switchyard
	url = https://github.com/NVIDIA-NeMo/Switchyard.git
	# pin to tag, e.g. v0.2.0 — do not track main floating
```

`tmp/Switchyard` was only for exploration; the real checkout is the submodule.

## Invoke architecture

Same ops model as MLX: **another process in the stack**, binary/source from `providers/switchyard`.

```text
process-compose (polypus)
  gateway           :1320     namespace: core
  mlx               :1322     namespace: mlx
  phoenix           :6006     namespace: obs
  switchyard-server :4000     namespace: switchyard
       cwd/command → providers/switchyard (cargo run / installed bin + generated TOML)

Apps ──► Polypus :1320
            │
            ├─ speech / embed / raw models ──► backends (as today)
            │
            └─ composed router chat ──loopback HTTP──► switchyard-server :4000
                                                 │  model = router/<name>
                                                 │  libsy decides leaf
                                                 ▼
                                            targets = Polypus URLs
                                            model = lm_studio/… or cf_local/…
                                                 │
                                                 ▼
                                            Polypus :1320 (leaf path, not router/*)
```

**Re-entry rule:** Switchyard targets always use leaf ids (`backend_id/…`). Those never start with `router/`, so they cannot match a named router.

### Why a process + HTTP (not embed)

| Option | Fit for Polypus (Go) |
|--------|----------------------|
| `switchyard-server` as a process from submodule | **Yes** — invoke like mlx; OpenAI Chat on loopback |
| Spawn Switchyard per request | Possible but worse (startup cost, no session latch) |
| Rust libsy in-process | No clean Go FFI |
| Sibling `providers/switchyard` in the monorepo only | No — keep it inside the Polypus tree so standalone `make serve` works |

### Conceptual layers

| Layer | Authoritative config | Runtime form |
|-------|----------------------|--------------|
| Backend | Polypus `config.yaml` | unchanged |
| Named router + leaves | Polypus `routers:` | composed → Switchyard `routes.toml`; passthrough stays in Polypus |
| Switchyard process | generated TOML + env | `switchyard-server --config <generated>` |

**Single source of truth:** operators edit Polypus YAML only. Polypus (or the process-compose launcher) **emits Switchyard TOML at runtime** before/while starting the Switchyard process. No hand-maintained parallel `routes.toml`.

## Config transform (Polypus → Switchyard)

### When

On gateway/serve start (and on **hot reload**):

1. Load Polypus `config.yaml` (backends + routers).
2. Validate router leaves against backends / allow-lists. Reject a backend id `router`.
3. **Render** a Switchyard deployment TOML into a runtime path (e.g. `~/.cache/polypus/switchyard/routes.toml` or `$POLYPUS_STATE/switchyard-routes.toml`).
4. Start or **restart** `switchyard-server --config <that file>` (rewrite file, then restart that process only — gateway stays up).
5. Composed router chat on `:1320` proxies to Switchyard with `model = router/<name>` (same string as Switchyard route `id`).

### Source shape (Polypus YAML)

```yaml
# ~/.config/polypus/config.yaml
backends:
  lm_studio: { … }   # existing
  cf_local:  { … }

switchyard:
  listen: 127.0.0.1:4000
  # transform output path optional; default under cache/state

routers:
  investigator:
    capability: chat
    route:
      type: stage_router
      picker: efficient_first
      confidence_threshold: 0.5
      capable:   cf_local/@cf/zai-org/glm-4.7-flash
      efficient: lm_studio/qwen2.5-7b

  intake:
    capability: chat
    route:
      type: escalate
      weak:   lm_studio/phi-3
      strong: cf_local/@cf/google/gemma-4-26b-a4b-it
      judge:  cf_local/@cf/zai-org/glm-4.7-flash
      confirmations: 2

  scribe:
    capability: chat
    route:
      type: passthrough
      target: cf_local/@cf/google/gemma-4-26b-a4b-it
```

Public ids: `router/investigator`, `router/intake`, `router/scribe`. YAML keys are the name after `router/`.

### Generated shape (Switchyard TOML)

Emitted by transform — not edited by hand. Route `id` matches the public model string so Polypus does not rewrite `model` on the Switchyard hop:

```toml
schema_version = 1

[llm_clients.polypus]
format = "openai_chat"
base_url = "http://127.0.0.1:1320/v1"

[targets.investigator_capable]
id = "cf_local/@cf/zai-org/glm-4.7-flash"
llm_client = "polypus"

[targets.investigator_efficient]
id = "lm_studio/qwen2.5-7b"
llm_client = "polypus"

[routes.investigator]
id = "router/investigator"
type = "stage_router"
capable_target = "investigator_capable"
efficient_target = "investigator_efficient"
picker = "efficient_first"
confidence_threshold = 0.5

# …intake → escalate route + weak/strong/judge targets…
# scribe (passthrough) is NOT emitted
```

### Mapping rules (sketch)

| Polypus | Switchyard |
|---------|------------|
| `routers.<name>` | `routes.<name>` with `id = "router/<name>"` |
| `route.type` | composed → Switchyard `type`; `passthrough` is **not** emitted |
| leaf model strings | `targets.<name>_<role>.id` + shared `llm_clients.polypus` |
| `backends` | not copied; only used to validate leaves before emit |
| Polypus listen URL | `llm_clients.polypus.base_url` |

Target table names are **internal** (router-scoped) so pools never collide across routers.

Always-on empty composed set: Switchyard still requires `schema_version`, `[targets]` (may be empty), and `[routes]` (may be empty). Emit those plus `[llm_clients.polypus]`.

### v1 field map: `stage_router`

Polypus YAML (under `routers.<name>.route`) → Switchyard TOML. **v1 required + one optional.** Extra keys fail Polypus load (`KnownFields`). Classifier, handoff notes, subagents, and per-tier system prompts are **not** in v1 YAML (Phase 3+).

```yaml
routers:
  investigator:
    capability: chat          # required; v1 only `chat`
    route:
      type: stage_router      # required
      picker: efficient_first # required: efficient_first | capable_first
      confidence_threshold: 0.5  # required: [0, 1]
      capable: cf_local/@cf/zai-org/glm-4.7-flash     # required leaf
      efficient: lm_studio/qwen2.5-7b                 # required leaf
      recent_turn_window: 3   # optional; omit → Switchyard default 3
```

| Polypus YAML | Switchyard TOML |
|--------------|-----------------|
| `routers.investigator` | `[routes.investigator]` |
| (implicit) | `id = "router/investigator"` |
| `route.type: stage_router` | `type = "stage_router"` |
| `route.capable` | `[targets.investigator_capable]` `id` = leaf; route `capable_target = "investigator_capable"` |
| `route.efficient` | `[targets.investigator_efficient]` `id` = leaf; route `efficient_target = "investigator_efficient"` |
| `route.picker` | `picker` |
| `route.confidence_threshold` | `confidence_threshold` |
| `route.recent_turn_window` | `recent_turn_window` (omit if unset) |
| — | `[llm_clients.polypus]` `format = "openai_chat"`, `base_url` = Polypus `:1320/v1`, no `api_key_env` |

**Passthrough** (not emitted):

```yaml
routers:
  scribe:
    capability: chat
    route:
      type: passthrough
      target: cf_local/@cf/google/gemma-4-26b-a4b-it   # required leaf
```

Polypus resolves `target` on `:1320` only.

**Load validation (Polypus):** `capability` is `chat`; leaf strings parse as `backend_id/…` with a known backend; backend id is not `router`; leaf allowed by that backend’s allow-list; `picker` enum; `confidence_threshold` in `[0, 1]`; `recent_turn_window` ≥ 1 if set; no unknown keys on `routers.*` / `route`.

After render, `switchyard-server --config <generated> --dry-run` in the launcher (fail start if invalid).

### Who runs the transform

**One Go render function**, two entry points:

| Entry | When |
|-------|------|
| Gateway startup + hot reload | `polypus serve` / process-compose gateway: render (and on reload, rewrite TOML + restart Switchyard) |
| CLI verb | `polypus switchyard-render` (same function). `scripts/pc-switchyard.sh` calls this **before** exec so the process has a file even if it starts before the gateway. |

No second implementation. Launcher and gateway stay consistent.

## How routing works

Routing decision for **composed** routers runs **in Switchyard (libsy)**. Polypus invokes it over HTTP, then leaf calls return through Polypus backends. **Passthrough** routers never leave Polypus.

### Request path (composed chat)

```text
POST :1320/v1/chat/completions
  body.model = "router/investigator"
        │
        ▼
1. Polypus: prefix router/ → lookup routers.investigator
   composed → forward to Switchyard (model unchanged)
   POST :4000/v1/chat/completions
        │
        ▼
2. Switchyard: run route algorithm (heuristics / random / escalate…)
   may issue judge CallModel steps against Polypus leaf ids
        │
        ▼
3. Switchyard: terminal call to chosen target
   → Polypus :1320 with model = lm_studio/… or cf_local/…
        │
        ▼
4. Polypus leaf path (not router/*): allow-list, **thinking**, **timeouts**, proxy, Phoenix
        │
        ▼
5. Response bubbles Switchyard → Polypus → client
```

Client still only talks to `:1320` and only knows `router/investigator`.

### What “route algorithm” means

| `route.type` | Who runs it | Decision |
|--------------|-------------|----------|
| `passthrough` | **Polypus** | Always the single `target`. No Switchyard hop. |
| `random` | Switchyard (internal) | Weighted pick among that router’s targets. |
| `escalate` | Switchyard (internal) | Weak first; judge may latch session to strong. |
| `stage_router` | Switchyard (internal) | Built-in tool/progress heuristics → capable/efficient. |

Important: **the public id stays constant** across turns (`model: router/investigator`). Only the **leaf** changes. Switchyard is never a public URL.

### Example: router/investigator via Switchyard stage_router

Same behavior as Switchyard’s built-in heuristics; Polypus only forwards.

| Turn | Client `model` | Switchyard signal | Leaf model id on Polypus |
|------|----------------|-------------------|--------------------------|
| 1 | `router/investigator` | ambiguous | `lm_studio/qwen2.5-7b` |
| 2 | `router/investigator` | WRONG (errors/spinning) | `cf_local/@cf/…/glm-4.7-flash` |
| 3 | `router/investigator` | PROGRESS (edits landing) | `lm_studio/qwen2.5-7b` |

### What does *not* go through Switchyard

- **Passthrough routers** — Polypus resolves `target` and uses the existing leaf proxy.
- **Speech / embed** — Polypus only.
- **Raw `cf_local/…`** — escape hatch, direct leaf.
- **Backends** — dumb arms; they never see `router/<name>`.

### Resolution order (Polypus)

```text
1. model has prefix "router/" → lookup routers.<rest>
     passthrough → leaf path
     composed → proxy to Switchyard (fail hard 503 if down)
2. else if model has backend_id/ prefix → existing leaf resolve
     (including Switchyard’s callbacks; never router/)
3. else → default_*_backend + bare model
```

Reserved prefix: **`router` is not a backend id.**

## Implementation designs

These were gaps; they are now part of the plan. Grounded in today’s gateway (`serveChatCompletions`, `proxyChatCompletions`, `/health`, `/health/backends`, `WrapTransport` + `StartLLMSpan`).

### Gateway router proxy (chat only)

Fork at the start of `serveChatCompletions`, after `extractChatModel`:

```text
if strings.HasPrefix(model, "router/"):
  name = rest after "router/"
  if routers[name] missing → 400
  if chatBodyHasVision → 400 (v1 routers are chat-only)
  if route.type == passthrough:
    resolve router.target → existing leaf path (rewrite + proxy)
    return
  # composed: Switchyard is required
  if Switchyard not configured or probe fails → 503 fail hard
    (no fallback to a default leaf)
  StartLLMSpan("polypus.router", model, backendID="switchyard", …)
  proxy to Switchyard (OpenAI chat, stream-capable)
    — do NOT rewrite body.model (id is already router/<name>)
    — do NOT apply thinking-disable (leaf hop does that)
    — outer HTTP timeout = timeouts.max (transport ceiling, not router policy)
  return
existing leaf path (ResolveChat / ResolveVision / rewrite / thinking / timeout buckets / proxy)
```

Reuse streaming/header copy for the Switchyard hop; skip `disableChatThinkingInRequest` on that hop. Thinking merge and `timeouts.ResolveChat` (including `chat_thinking` / per-backend buckets) run only on the **leaf** path, keyed by the chosen backend — not by the router id.

Transform **omits** passthrough routers from generated TOML. The Switchyard process **still starts** (always-on).

Embeddings, speech, transcription stay on the leaf path. A `router/…` id on those endpoints is `400 model not valid for this capability`.

### Config load validation

Today `yaml.Unmarshal` ignores unknown keys. For `routers:` / `switchyard:` (and ideally the whole router config file), use `yaml.NewDecoder` with **`KnownFields(true)`** so unknown fields fail at Polypus load — not at Switchyard TOML parse later. Invalid `route.type`, missing leaves, `router` as a backend id, and leaves not on the allow-list also fail load.

### `/v1/models`

Default list: **routers + raw enabled models** (`router/<name>` with `owned_by: polypus`, then today’s allow-gated backend models). Not routers-only. `?view=inventory` unchanged (upstream arms only; named routers are not inventory). `GET /v1/models/{id}` retrieves a router object when `id` is `router/…`, else existing retrieve.

### Health

`GET /health` stays liveness (no probes). When Switchyard is enabled, include it next to backends — do **not** put it in `config.Backends`:

```json
{
  "status": "ok",
  "router": "bifrost",
  "backends": [{ "id": "cf_local", "url": "…" }],
  "switchyard": { "url": "http://127.0.0.1:4000" }
}
```

(`health.router` remains the Bifrost engine name; do not confuse with named routers.)

`GET /health/backends` probes Switchyard `GET {url}/health` with the same 5s timeout as other pings. Failure → `degraded` / 503 like any backend. Always include `switchyard` when the stack is running (always-on process).

### Traces (Polypus → Switchyard → leaf)

Looked at Switchyard source (server + llm-client), not assumed.

| Piece | What the code does |
|-------|-------------------|
| Inbound | `request_span` extracts W3C `traceparent` / `tracestate` (`TraceContextPropagator`) and parents `switchyard.request`. |
| Outbound | Incoming HTTP headers are stored on metadata and **forwarded** to upstream minus a reserved set (`host`, `authorization`, cookies, …). **`traceparent` is not reserved**, so Polypus’s injected header is forwarded to the leaf. |
| OTLP | Switchyard can export if `OTEL_EXPORTER_OTLP_ENDPOINT` is set (`switchyard-server` observability). |
| Inject child span | No separate inject of the Switchyard span id; leaf sees the same `traceparent` Polypus sent into Switchyard. |

Polypus already: `otelhttp` server handler (inbound extract) + `WrapTransport` (outbound inject).

**v1:** one trace id across hops is achievable without extra glue. Point Switchyard at the same Phoenix collector as Polypus (`OTEL_EXPORTER_OTLP_ENDPOINT` / `POLYPUS_OTLP_ENDPOINT`) so Switchyard spans show in the same UI. Leaf HTTP span is a sibling of `switchyard.request` under the Polypus named-router span, not a child of Switchyard — same trace, good enough.

| Hop | Span |
|-----|------|
| Client → Polypus | `polypus` HTTP + `polypus.router` |
| Polypus → Switchyard | injected `traceparent` |
| Switchyard | `switchyard.request` (parent = that context) |
| Switchyard → Polypus leaf | forwarded `traceparent` |
| Polypus leaf | `otelhttp` + `polypus.chat` |

### Operator pack + smoke

| Surface | Change |
|---------|--------|
| `config-reference.md` | Ports (`:4000`), `POLYPUS_SWITCHYARD`, `routers:` YAML, `router/<name>` ids, generated TOML path |
| `troubleshooting.md` | Switchyard down → `/health/backends` `switchyard.ok=false`; loop suspicion → leaf ids must be `backend_id/…` not `router/…` |
| `harness.md` | L1 router smoke |
| `SKILL.md` | Named routers are public `model` ids (`router/<name>`); clients still only call `:1320` |
| `Makefile` | `make smoke-router` → `polypus-chat-smoke -model $POLYPUS_ROUTER_SMOKE_MODEL` (default `router/investigator`) |

`config.yaml.example` gets a commented `routers:` + `switchyard:` stub. No checked-in Switchyard TOML.

Smoke: `make smoke-router` hits a composed router (default `router/investigator`) and **fails hard** if Switchyard is down. Passthrough smoke can run with Switchyard off (`POLYPUS_CHAT_SMOKE_MODEL=router/scribe` or similar).

Switchyard’s own `/v1/stats` (if present) is operator-optional; Polypus does not scrape it in v1.

## Observability

Named-router hop records `polypus.router` via the span name + `llm.model_name`. Leaf hop records existing `backend` + `downstream_model`. Switchyard decision labels (capable/efficient) live in Switchyard logs/metrics until we confirm a response header we can copy; do not block v1 on parsing those.

## Non-goals (for this plan)

- Porting libsy heuristics into Go.
- Embedding Rust Switchyard crates / cgo FFI.
- Replacing Polypus with Switchyard as the only public URL.
- Sending Switchyard targets straight to LM Studio/CF (skip Polypus leaf path).
- Auto-discovering routers from upstream catalogs.
- Speech through Switchyard.

## Phased delivery

### Phase 0 — Spec lock

- YAML→TOML transform map + validation rules (`KnownFields` on Polypus load).
- Pin Switchyard submodule to **`v0.2.0`** (tags exist: `v0.0.1`, `v0.1.0`, `v0.2.0`; they do not float `main` as a release). Bump the pin deliberately when we want newer.

### Phase 1 — Submodule process + transform + one composed router

- Add `providers/switchyard` git submodule (pinned).
- Implement render: Polypus `routers:` → generated Switchyard TOML (`id = router/<name>`).
- process-compose: render then start `switchyard-server --config <generated>`.
- Polypus: `router/<name>` composed → proxy to Switchyard; passthrough → leaf.
- Smoke: `model=router/investigator` round-trip.

### Phase 2 — Catalog + ops

- `/v1/models` lists `router/…` **and** raw enabled models.
- `/health` + `/health/backends` include Switchyard (see implementation designs).
- W3C `traceparent` on the named-router hop; leaf hop as today.
- Operator pack + `make smoke-router`.

### Phase 3 — More route types in YAML

- Extend the **Polypus YAML → generated TOML** map (`escalate`, `random`). No hand-edited Switchyard config.
- Operator docs: how to edit `routers:` in `config.yaml`.

## Mapping when invoking Switchyard

| Switchyard | Polypus |
|------------|---------|
| Route `id` | Public `model` = `router/<name>` |
| `routes.toml` algorithms | Owned by Switchyard (no Go port) |
| Target `id` | Polypus leaf model (`lm_studio/…`, `cf_local/…`) |
| LLM client `base_url` | `http://127.0.0.1:1320/v1` (Polypus) |
| `:4000` server | process-compose process from `providers/switchyard` submodule |
| Protocol translation | Optional bonus if we expose Anthropic later; not required for routers v1 |

## Open questions (iterate here)

1. **Config key:** **decided** — `routers:` (not `personas:`).
2. **Public id form:** **decided** — `router/<name>` (not bare). Reserved prefix; not a backend id.
3. **Transform owner:** **decided** — shared Go render; gateway startup + hot reload; CLI `polypus switchyard-render` for `pc-switchyard.sh`.
4. **Reload:** **decided** — hot rewrite generated TOML + restart Switchyard process; gateway stays up.
5. **Re-entry:** **decided** — Switchyard always calls `backend_id/…`; never `router/…`.
6. **Lifecycle:** **decided** — Switchyard process always on with the stack (simple).
7. **Failure mode:** **decided** — fail hard (`503`). No fallback leaf.
8. **Pin:** **decided** — submodule tag **`v0.2.0`** (tags exist; local clones may not have fetched them). Pre-alpha server still a local-stack risk.
9. **Traces:** **decided** — Switchyard continues W3C inbound and **forwards `traceparent`** (not in reserved header strip). Same trace id; point Switchyard OTLP at Phoenix.
10. **Unknown YAML fields:** **decided** — fail at **Polypus config load** (`KnownFields(true)`), not at Switchyard.
11. **Passthrough routers:** **decided** — Polypus serves them; Switchyard is internal (not emitted, not hopped).
12. **`/v1/models`:** **decided** — `router/…` + raw enabled models (not routers-only).
13. **Thinking / timeouts:** **decided** — bound on the **leaf** backend only; named-router hop is a transport envelope (`timeouts.max`, no thinking rewrite).

## Decision log

| Date | Decision | Notes |
|------|----------|-------|
| 2026-08-28 | Named endpoint ≠ single-model alias | Each router owns a policy over leaves |
| 2026-08-28 | ~~Port heuristics into Go~~ → **Invoke Switchyard** | HTTP to long-lived process; leave libsy in Rust |
| 2026-08-28 | Leaf targets via Polypus `:1320` | Keep allow-lists, thinking, Phoenix on the arm path |
| 2026-08-28 | Switchyard = submodule **inside Polypus** | `providers/switchyard/`; not monorepo sibling; replace `tmp/Switchyard` |
| 2026-08-28 | **Polypus YAML → Switchyard TOML at runtime** | Single source of truth; generated file, not hand-maintained |
| 2026-08-28 | Implementation gaps designed | Router chat proxy; health probe; traces; models list; operator/smoke; no checked-in TOML |
| 2026-08-28 | Re-entry = leaf model ids | Switchyard always calls `backend_id/…` |
| 2026-08-28 | Switchyard down → **fail hard** | `503`; no silent leaf fallback for composed routers |
| 2026-08-28 | Passthrough = **Polypus** | Clients always hit `:1320`; Switchyard is internal; no passthrough in generated TOML |
| 2026-08-28 | Reload = **hot rewrite + restart** | Regen TOML, restart Switchyard only |
| 2026-08-28 | `/v1/models` = routers **+ raw** | Inventory view stays arms-only |
| 2026-08-28 | Thinking/timeouts on **leaf** | Named-router hop does not apply thinking policy |
| 2026-08-28 | Rename **persona → router** | YAML `routers:`; public id `router/<name>` |
| 2026-08-28 | Transform = gateway **and** CLI | Same function; `pc-switchyard.sh` uses CLI before exec |
| 2026-08-28 | Switchyard **always on** | Simple process-compose; not gated on composed routers |
| 2026-08-28 | Pin **v0.2.0** | Remote tags exist (`v0.0.1` … `v0.2.0`) |
| 2026-08-28 | Traces = W3C through Switchyard | Extract inbound; forward `traceparent` to leaf |
| 2026-08-28 | Unknown YAML = **fail Polypus load** | `KnownFields(true)` |
| 2026-08-28 | v1 map = **stage_router + passthrough** | Capable/efficient leaves; no classifier/handoff in v1 |

## Next iteration

- Field map for **`stage_router` + passthrough** is spec’d above.
- Implement Phase 1: submodule `@ v0.2.0`, render, always-on process, `router/<name>` proxy, `KnownFields`, smoke `router/investigator`.
