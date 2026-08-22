# BOOTSTRAP — Polypus ai-copilots operator pack

**Audience:** Any AI agent (Cursor, GitHub Copilot, Claude Code, Codex) executing this file in the Polypus git repo or as submodule `providers/polypus/` inside Consilium.

**Goal:** Operate the Polypus operator pack under `ai-copilots/`. **Wire discovery** for the user's IDE and OS (agent-driven; no Makefile shortcut). Optionally refresh canonical content or Consilium monorepo pointers.

**MUST NOT** skip verification after wiring. **MUST NOT** copy skill bodies into Consilium `.cursor/skills/` unless symlinks/junctions fail and user approves copy fallback.

---

## When to run

Run this bootstrap when the user asks to:

- **wire** Polypus ai-copilots into Cursor, Copilot, Claude Code, or Codex
- build or refresh the Polypus operator agent pack (content only)
- troubleshoot Polypus via the operator skill after wiring

**Two modes:**

| Mode | Phases |
|------|--------|
| **Wire only** (content already in `ai-copilots/`) | 0 → 5 → 6 (if Consilium) → 7 → 8 |
| **Refresh content + wire** | 0 → 1–4 → 5 → 6 → 7 → 8 |

If `ai-copilots/agents/polypus-operator.md` and `skills/polypus-operator/SKILL.md` exist, skip Phases 2–4 unless the user asked to refresh content.

**Stop** if the workspace is a case instance repo (`pii/` at root). Polypus ops belong in the product or Polypus repo only.

---

## Phase 0 — Read prerequisites

Before writing files, read these (paths relative to Polypus repo root):

| File | Why |
|------|-----|
| `README.md` | Ports, process-compose, OpenAI surface, env vars |
| `config.yaml.example` | Backends, `models.allow`, timeouts |
| `config.yaml` | Live allow-list if present (may differ from example) |
| `Makefile` | `serve`, `smoke`, `smoke-chat` targets |
| `process-compose.yaml` | Supervised processes |

If nested in Consilium, also read:

| File | Why |
|------|-----|
| `../../docs/run/providers/polypus/router.md` | Router acceptance, breach model |
| `../../.cursor/skills/consilium-ai-stack/SKILL.md` | `make polypus-serve` supervision rules |
| `../../Makefile` | `polypus-serve`, `polypus-smoke-chat`, `model-harness` |
| `../../stack/ai.yaml` | Consilium model ids (must match allow-list) |

Optional code references for thinking-policy shard:

- `internal/gateway/chat.go` — thinking disable/merge behavior
- Consilium `engine/internal/intelligence/cloudrun/reasoning_transport.go` — Consilium force-off for structured XML

---

## Phase 1 — Target tree

Create or refresh this layout. **Edit only files under `ai-copilots/`** for canonical content.

```text
ai-copilots/
  README.md                    # already exists; update if invoke path changes
  BOOTSTRAP.md                 # this file
  agents/
    polypus-operator.md        # Cursor subagent manifest
  skills/
    polypus-operator/
      SKILL.md                 # main workflow (under ~500 lines)
      config-reference.md      # backends, env, ports, allow-list
      troubleshooting.md       # symptom → cause → fix
      harness.md               # L1/L2/L3 matrix (Consilium parent)
      thinking-policy.md       # Gemma/GLM thinking, empty content, XML
```

---

## Phase 2 — Author `agents/polypus-operator.md`

Follow the pattern of Consilium `.cursor/agents/consilium-architect.md`:

```yaml
---
name: polypus-operator
description: >-
  Polypus gateway operator: start/stop stack, config.yaml and models.allow,
  smoke tests, model harness, thinking policy, Phoenix traces, cf_local and
  lm_studio backends. Use when Polypus, inference routing, empty content,
  model_not_allowed, or stack/ai.yaml model mismatch comes up. Not for case
  timeline, evidence, or pii/ work.
model: inherit
---
```

**Body MUST include:**

- Role: operate Polypus gateway; do not log case facts
- **Write here:** `config.yaml`, Polypus `Makefile`, `process-compose.yaml`, `ai-copilots/`
- **Read/run:** Consilium `stack/ai.yaml` when diagnosing model mismatches (nested repo only)
- **MUST:** supervise via `make serve` (Polypus root) or `make polypus-serve` (Consilium root); loopback only; cloud via `INFERENCE_CLOUD_CASE=1`
- **MUST NOT:** ad-hoc `bin/polypus` or MLX in background shells; remote URLs in Consilium config; case `pii/` writes
- **MUST** load skill `polypus-operator` at start of every task

---

## Phase 3 — Author `skills/polypus-operator/SKILL.md`

Front matter:

```yaml
---
name: polypus-operator
description: >-
  Operates Polypus inference gateway: health, config, allow-list, smoke tests,
  model harness, thinking policy, Phoenix traces. Use when Polypus is down,
  models are rejected, chat returns empty content, or Consilium jobs fail on
  inference. Not for case intake or timeline.
---
```

**SKILL.md body:** imperative steps, short bullets. Include these **decision flows** (offer numbered options in chat; tutor voice: situation → why → what you know → choice):

| Flow | Steps |
|------|-------|
| **Is Polypus up?** | `curl -sf http://127.0.0.1:1320/health \| jq .`; if fail → offer `make serve` (Polypus) or `make polypus-serve` (Consilium) |
| **Which models are enabled?** | `GET /v1/models` vs `GET /v1/models?view=inventory`; compare to `config.yaml` `models.allow` |
| **Smoke chat (L1)** | Polypus: `make smoke-chat`; Consilium: `make polypus-smoke-chat` |
| **Smoke audio** | `make smoke` / `make polypus-smoke`; STT round-trip: `make smoke-stt` |
| **Full matrix** | Consilium only: `make model-harness`, `bin/stack-doctor model-harness --discover`, `--tier L1\|L2\|L3 --model '...'` |
| **model_not_allowed** | Model id missing from allow-list or wrong prefix (`cf_local/`, `lm_studio/`) |
| **Empty content / XML fail** | Load `thinking-policy.md`; run L2 harness; check Phoenix `:6006` and `logs/inference-failures/` |
| **cf_local down** | In-process extension, `INFERENCE_CLOUD_CASE=1`, `CF_AI_API_KEY`, `CF_ACCOUNT_ID` |
| **lm_studio down** | External `:1234`; user starts LM Studio; probe `curl -sf http://127.0.0.1:1234/v1/models` |
| **stack/ai.yaml drift** | Consilium: `bin/stack-doctor diagnose`; model id must appear in Polypus allow-list |

Link to shards (one level deep only). **MUST NOT** paste full router.md into SKILL.md.

---

## Phase 4 — Author skill shards

### `config-reference.md`

- Ports table: `:1320` gateway, `:1322` MLX, `:1234` LM Studio, `:6006` Phoenix, `:4317` OTLP (cf_local in-process when cloud opt-in)
- Key env: `POLYPUS_BASE_URL`, `INFERENCE_CLOUD_CASE`, `POLYPUS_PHOENIX`, `POLYPUS_OTEL`
- `config.yaml` fields: `default_*_backend`, `backends.*.models.allow`, `timeouts`
- Inventory vs enabled: `/v1/models` vs `?view=inventory`

### `troubleshooting.md`

Symptom → cause → fix table. Include at minimum:

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| Connection refused `:1320` | Gateway not running | `make serve` / `make polypus-serve` |
| `model_not_allowed` | Not in allow-list | Add to `config.yaml` or use prefixed id |
| Empty `message.content` | Thinking on; answer in `reasoning_content` | See thinking-policy; Polypus merges on success |
| Chat routed to MLX | Wrong default backend or missing prefix | Fix `default_chat_backend` |
| cf_local auth error | Missing opt-in or keys | `INFERENCE_CLOUD_CASE=1`, CF env |
| Timeout | Hop too short | `timeouts.chat_thinking`, `X-Polypus-Timeout` |
| Consilium job fails, smoke passes | L3 fixture or stack/ai.yaml model | L3 harness; diagnose |

### `harness.md`

Document Consilium parent commands (not available in standalone Polypus):

```bash
make model-harness
bin/stack-doctor model-harness --discover
bin/stack-doctor model-harness --tier L1 --model 'cf_local/@cf/google/gemma-4-26b-a4b-it'
bin/stack-doctor model-harness --tier L2
bin/stack-doctor model-harness --tier L3
```

Tier summary:

- **L1:** ping, content_nonempty, thinking_policy
- **L2:** minimal_xml, directives_ack, list_field (DSPy/XML)
- **L3:** prose_clarity, concept_discover, grounded_ask slices

Requires: Polypus up, `stack/ai.yaml`, live LLM.

### `thinking-policy.md`

- Cloudflare Gemma 4: `chat_template_kwargs.enable_thinking` (nested); CF defaults thinking on
- Polypus gateway disables thinking unless client enables; merges `reasoning_content` → `content`
- Consilium structured XML jobs force `enable_thinking: false` for Gemma/GLM
- When to probe thinking on vs off: smoke/debug only; production structured output → off

---

## Phase 5 — Wire for IDE and OS (agent-driven)

**No Makefile target.** The copilot reading this file performs wiring. **MUST NOT** assume Cursor or macOS without checking.

### 5a. Ask before wiring (if unknown)

Use tutor voice. Ask only what you cannot infer from the environment:

1. **IDE / copilot** (pick one or more):
   - **Cursor** (`.cursor/agents/`, `.cursor/skills/`)
   - **GitHub Copilot in VS Code** (`.github/agents/*.agent.md`, `.github/skills/`)
   - **Claude Code** (`.claude/agents/`, `.claude/skills/`)
   - **Codex** (`.codex/agents/`, `.codex/skills/`)
   - **Agent Skills standard** (`.agents/skills/` only; no subagents path)

2. **OS** (pick one):
   - **macOS** or **Linux** → prefer relative symlinks
   - **Windows** → ask whether symlinks work (Developer Mode or admin). If not, use junction or copy fallback.

3. **Workspace layout** (infer when possible):
   - Polypus repo opened alone (workspace root = Polypus)
   - Consilium monorepo (workspace root = parent; Polypus at `providers/polypus/`)
   - Need `/polypus-operator` at Consilium root? (yes only for Cursor subagent at monorepo root)

Detect OS from `uname -s` (Darwin/Linux) or `OS=Windows_NT` in cmd/powershell. Detect IDE from user message or open product (Cursor vs VS Code).

### 5b. Canonical source (never duplicate bodies)

All links point **into** `ai-copilots/`:

| Artifact | Canonical path |
|----------|----------------|
| Agent manifest | `ai-copilots/agents/polypus-operator.md` |
| Skill tree | `ai-copilots/skills/polypus-operator/` |

Paths below are **link targets** relative to the repo where wiring runs (usually Polypus root; Consilium root for root agent only).

### 5c. Discovery paths by IDE

| IDE | Agents | Skills |
|-----|--------|--------|
| **Cursor** | `<workspace-root>/.cursor/agents/*.md` only | `.cursor/skills/**/SKILL.md` (nested under repo OK) |
| **GitHub Copilot** | `.github/agents/*.agent.md` | `.github/skills/**/SKILL.md` |
| **Claude Code** | `.claude/agents/*.md` | `.claude/skills/**/SKILL.md` |
| **Codex** | `.codex/agents/*.md` | `.codex/skills/**/SKILL.md` |
| **Agent Skills** | (none) | `.agents/skills/**/SKILL.md` |

Copilot agent file may symlink/copy to the same content as Cursor agent but use extension `.agent.md`.

### 5d. Wire strategy by OS

**macOS / Linux (preferred): relative symlinks**

From Polypus repo root (`POLYPUS_ROOT`):

```bash
mkdir -p .cursor/agents .cursor/skills
ln -snf ../ai-copilots/agents/polypus-operator.md .cursor/agents/polypus-operator.md
ln -snf ../ai-copilots/skills/polypus-operator .cursor/skills/polypus-operator
```

For Copilot (same repo):

```bash
mkdir -p .github/agents .github/skills
ln -snf ../../ai-copilots/agents/polypus-operator.md .github/agents/polypus-operator.agent.md
ln -snf ../../ai-copilots/skills/polypus-operator .github/skills/polypus-operator
```

Adjust prefix depth if linking from Consilium root (see Phase 6).

**Windows — symlinks (Developer Mode or elevated shell):**

```powershell
# File symlink (agent)
New-Item -ItemType SymbolicLink -Path .cursor\agents\polypus-operator.md `
  -Target ..\ai-copilots\agents\polypus-operator.md -Force
# Directory symlink (skill) — or use Junction (below)
New-Item -ItemType SymbolicLink -Path .cursor\skills\polypus-operator `
  -Target ..\ai-copilots\skills\polypus-operator -Force
```

Cmd alternative:

```cmd
mklink .cursor\agents\polypus-operator.md ..\ai-copilots\agents\polypus-operator.md
mklink /J .cursor\skills\polypus-operator ..\ai-copilots\skills\polypus-operator
```

**Windows — junction (no admin symlink; directories only):**

```cmd
mklink /J .cursor\skills\polypus-operator ..\ai-copilots\skills\polypus-operator
```

For the agent **file**, use a small wrapper `.md` that says `See ../ai-copilots/agents/polypus-operator.md` only if symlink fails; prefer symlink or hardlink.

**Windows / any OS — copy fallback (last resort):**

If user confirms symlinks/junctions fail in their IDE:

```bash
cp -a ai-copilots/agents/polypus-operator.md .cursor/agents/
cp -a ai-copilots/skills/polypus-operator .cursor/skills/
```

**MUST** warn: copies drift from canonical `ai-copilots/`; re-run bootstrap to refresh.

### 5e. Idempotency

Before creating a link:

- If target exists and resolves to canonical path → skip
- If target is a stale copy → ask user before overwrite
- If target is wrong symlink → replace with user approval

### 5f. Do not commit wiring blindly

Ask user whether to commit `.cursor/`, `.github/`, etc. Some teams gitignore IDE wiring and re-run bootstrap per machine.

---

## Phase 6 — Consilium monorepo (if applicable)

Detect Consilium: `../../stack/.env.example` exists from Polypus submodule path, or user opened Consilium as workspace root.

### 6a. Cursor subagent at monorepo root

Cursor discovers subagents only at **workspace root** `.cursor/agents/`. Nested `providers/polypus/.cursor/agents/` is not enough for `/polypus-operator` when workspace is Consilium.

From Consilium repo root, wire using OS strategy from Phase 5d:

**macOS / Linux:**

```bash
ln -snf ../providers/polypus/ai-copilots/agents/polypus-operator.md \
  .cursor/agents/polypus-operator.md
```

**Windows (cmd, symlink):**

```cmd
mklink .cursor\agents\polypus-operator.md providers\polypus\ai-copilots\agents\polypus-operator.md
```

Skills at nested path: wiring under `providers/polypus/.cursor/skills/` is enough for Polypus-path edits without root skill copy.

### 6b. Index pointers (only if user asked)

Edit Consilium files only when user wants full monorepo discoverability:

| File | Change |
|------|--------|
| `.cursor/skills/consilium-index/SKILL.md` | Agents line: add `/polypus-operator`; quick-pick row |
| `.cursor/skills/consilium-ai-stack/SKILL.md` | One line under Polypus → `/polypus-operator` |
| `docs/run/providers/polypus/router.md` | References line |

**MUST NOT** duplicate skill tree under Consilium `.cursor/skills/`.

## Phase 7 — Verify

After wiring, run checks that match the IDE the user chose:

```bash
# Links resolve (macOS/Linux)
ls -la .cursor/agents/polypus-operator.md
ls -la .cursor/skills/polypus-operator
readlink .cursor/agents/polypus-operator.md   # optional
```

Windows: `dir .cursor\agents` and confirm `<SYMLINK>` or junction target.

**Cursor:**

1. Reload window or Customize → Skills
2. `polypus-operator` listed
3. `/polypus-operator` in agent picker (requires root link if Consilium monorepo)

**Copilot:**

1. `.github/agents/polypus-operator.agent.md` resolves
2. `.github/skills/polypus-operator/SKILL.md` resolves

**Optional runtime (if Polypus stack running):**

```bash
curl -sf http://127.0.0.1:1320/health | jq .
make smoke-chat          # Polypus root
make polypus-smoke-chat  # Consilium root
```

## Phase 8 — Commit order

Ask user what to commit.

1. **Polypus repo:** always commit `ai-copilots/` content changes
2. **Polypus repo:** commit `.cursor/` / `.github/` wiring only if user wants it in git
3. **Consilium repo:** commit root agent link and index lines only if user asked; bump submodule SHA separately

---

## Constraints (always)

| MUST | MUST NOT |
|------|----------|
| Ask IDE and OS before wiring if unknown | Run a blind Makefile or shell script that assumes Cursor + Unix |
| Edit canonical content only under `ai-copilots/` | Copy skill bodies into Consilium `.cursor/skills/` without user approval |
| Prefer symlink/junction to canonical tree | Host public operator UI |
| Supervise via process-compose (`make serve` / `make polypus-serve`) | Start `bin/polypus` in ad-hoc shells |
| Loopback backends only in case mode | Put remote inference URLs in Consilium config |
| English only; no em dashes (U+2014) | Case `pii/` writes from this agent |

---

## Discovery reference

| Tool | Project paths |
|------|---------------|
| Cursor skills | `.cursor/skills/**/SKILL.md` (nested anywhere in repo) |
| Cursor subagents | Workspace-root `.cursor/agents/*.md` only |
| Copilot skills | `.github/skills/**/SKILL.md` |
| Copilot agents | `.github/agents/*.agent.md` |

When Polypus is submodule `providers/polypus/`, nested `.cursor/skills/` covers Polypus-path edits; root Consilium symlink covers `/polypus-operator` invoke.
