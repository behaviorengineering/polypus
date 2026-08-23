# BOOTSTRAP — Polypus ai-copilots operator pack

**Audience:** Any AI agent (Cursor, GitHub Copilot, Claude Code, Codex) executing this file in the Polypus git repo or nested under `providers/polypus/` in a parent monorepo.

**Goal:** Operate the Polypus operator pack under `ai-copilots/`. **Wire discovery** for the user's IDE and OS (agent-driven; no Makefile shortcut). Optionally refresh canonical content.

**MUST NOT** skip verification after wiring. **MUST NOT** copy skill bodies into a parent repo's `.cursor/skills/` unless symlinks/junctions fail and the user approves copy fallback.

---

## When to run

Run this bootstrap when the user asks to:

- **wire** Polypus ai-copilots into Cursor, Copilot, Claude Code, or Codex
- build or refresh the Polypus operator agent pack (content only)
- troubleshoot Polypus via the operator skill after wiring

**Two modes:**

| Mode | Phases |
|------|--------|
| **Wire only** (content already in `ai-copilots/`) | 0 → 5 → 6 (if nested) → 7 → 8 |
| **Refresh content + wire** | 0 → 1–4 → 5 → 6 → 7 → 8 |

If `ai-copilots/agents/polypus-operator.md` and `skills/polypus-operator/SKILL.md` exist, skip Phases 2–4 unless the user asked to refresh content.

**Stop** if the workspace is a case-instance repo (sensitive case data at repo root). Polypus ops belong in the Polypus repo or product tooling repo only.

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

Optional code references for thinking-policy shard:

- `internal/gateway/chat.go` — thinking disable/merge behavior

---

## Phase 1 — Target tree

Create or refresh this layout. **Edit only files under `ai-copilots/`** for canonical content.

```text
ai-copilots/
  README.md
  BOOTSTRAP.md
  agents/
    polypus-operator.md
  skills/
    polypus-operator/
      SKILL.md
      config-reference.md
      troubleshooting.md
      harness.md
      thinking-policy.md
```

---

## Phase 2 — Author `agents/polypus-operator.md`

Use the existing file under `ai-copilots/agents/` as the template. The agent operates Polypus only; it does not run case workflows.

---

## Phase 3 — Author `skills/polypus-operator/SKILL.md`

Front matter and body: imperative steps, short bullets. Decision flows use **Polypus repo commands only** (`make serve`, `make smoke-chat`, etc.). Link to shards one level deep.

---

## Phase 4 — Author skill shards

See existing shards under `ai-copilots/skills/polypus-operator/`. Keep host-specific harness tooling out of this repo; document L1 smoke here only.

---

## Phase 5 — Wire for IDE and OS (agent-driven)

**No Makefile target.** The copilot reading this file performs wiring. **MUST NOT** assume Cursor or macOS without checking.

### 5a. Ask before wiring (if unknown)

1. **IDE / copilot** (Cursor, Copilot, Claude Code, Codex, or Agent Skills standard)
2. **OS** (macOS/Linux symlinks vs Windows junction/copy)
3. **Workspace layout** (Polypus repo alone vs nested under `providers/polypus/`)

### 5b. Canonical source (never duplicate bodies)

| Artifact | Canonical path |
|----------|----------------|
| Agent manifest | `ai-copilots/agents/polypus-operator.md` |
| Skill tree | `ai-copilots/skills/polypus-operator/` |

### 5c. Discovery paths by IDE

| IDE | Agents | Skills |
|-----|--------|--------|
| **Cursor** | `<workspace-root>/.cursor/agents/*.md` | `.cursor/skills/**/SKILL.md` |
| **GitHub Copilot** | `.github/agents/*.agent.md` | `.github/skills/**/SKILL.md` |
| **Claude Code** | `.claude/agents/*.md` | `.claude/skills/**/SKILL.md` |
| **Codex** | `.codex/agents/*.md` | `.codex/skills/**/SKILL.md` |

### 5d. Wire strategy (Polypus repo root)

**macOS / Linux:**

```bash
mkdir -p .cursor/agents .cursor/skills
ln -snf ../ai-copilots/agents/polypus-operator.md .cursor/agents/polypus-operator.md
ln -snf ../ai-copilots/skills/polypus-operator .cursor/skills/polypus-operator
```

**Windows:** symlink or junction per Phase 5 in prior revisions; copy fallback only with user approval.

### 5e. Idempotency

Before creating a link: skip if target already resolves to canonical path; ask before overwriting stale copies.

### 5f. Do not commit wiring blindly

Ask user whether to commit `.cursor/`, `.github/`, etc.

---

## Phase 6 — Parent monorepo (if applicable)

Detect nesting: workspace root contains `providers/polypus/ai-copilots/` and Polypus is a submodule or subtree.

**Cursor subagent at monorepo root:** some IDEs discover subagents only at workspace-root `.cursor/agents/`. From the **parent repo root**, link:

```bash
ln -snf ../providers/polypus/ai-copilots/agents/polypus-operator.md \
  .cursor/agents/polypus-operator.md
```

Skills under `providers/polypus/.cursor/skills/` cover edits when the workspace file tree is under the nested path.

**MUST NOT** edit parent-repo skill indexes unless the user explicitly asks for discoverability there.

---

## Phase 7 — Verify

```bash
ls -la .cursor/agents/polypus-operator.md
ls -la .cursor/skills/polypus-operator
curl -sf http://127.0.0.1:1320/health | jq .   # when stack is up
make smoke-chat                                 # Polypus root
```

---

## Phase 8 — Commit order

Ask user what to commit.

1. **Polypus repo:** commit `ai-copilots/` content changes
2. **Polypus repo:** commit `.cursor/` / `.github/` wiring only if user wants it in git
3. **Parent repo:** commit root agent link only if user asked; bump submodule SHA separately

---

## Constraints (always)

| MUST | MUST NOT |
|------|----------|
| Ask IDE and OS before wiring if unknown | Assume Cursor + Unix blindly |
| Edit canonical content only under `ai-copilots/` | Copy skill bodies into parent repos without approval |
| Prefer symlink/junction to canonical tree | Host public operator UI |
| Supervise via process-compose (`make serve`) | Start `bin/polypus` in ad-hoc shells |
| Loopback backends in sensitive deployments | Put remote inference URLs in client app config |
| English only; no em dashes (U+2014) | Write sensitive case data from this agent |

---

## Discovery reference

| Tool | Project paths |
|------|---------------|
| Cursor skills | `.cursor/skills/**/SKILL.md` |
| Cursor subagents | Workspace-root `.cursor/agents/*.md` |
| Copilot skills | `.github/skills/**/SKILL.md` |
| Copilot agents | `.github/agents/*.agent.md` |

When nested at `providers/polypus/`, wire skills under that tree; add a root agent link only if the workspace root is the parent monorepo.
