# Polypus ai-copilots

Operator agent pack for Polypus: configure, smoke-test, and troubleshoot the gateway without reading long runbooks.

## Contents

```text
ai-copilots/
  agents/polypus-operator.md       # Cursor subagent manifest
  skills/polypus-operator/
    SKILL.md                       # Main workflow
    config-reference.md
    troubleshooting.md
    harness.md
    thinking-policy.md
  BOOTSTRAP.md                     # Agent entry: wire IDE, refresh content, verify
```

Canonical source lives here. **No Makefile link step.** Wiring is done by the AI copilot you use (Cursor, VS Code Copilot, etc.) when you ask it to execute [BOOTSTRAP.md](BOOTSTRAP.md).

## Wire discovery (from your IDE)

Tell your copilot to run [BOOTSTRAP.md](BOOTSTRAP.md) in **wire mode**. It will ask IDE and OS if it cannot infer them, then create symlinks, junctions, or copies as appropriate (see BOOTSTRAP.md Phase 5).

**Minimal prompt:**

> Wire Polypus ai-copilots using BOOTSTRAP.md

**Example (recommended — fewer questions):**

> Execute `ai-copilots/BOOTSTRAP.md` in wire mode. I'm on Cursor on macOS. Workspace root is the Polypus repo. Wire the skill and `/polypus-operator` agent.

Other examples:

| Situation | Example prompt |
|-----------|----------------|
| Nested under `providers/polypus/`, VS Code Copilot, Windows | Execute `ai-copilots/BOOTSTRAP.md` wire mode. GitHub Copilot on Windows 11. Use junction if symlinks need admin. |
| Cursor, Linux, Polypus standalone | Execute `ai-copilots/BOOTSTRAP.md` wire mode. Cursor on Linux. Workspace root is the Polypus repo. |
| Refresh content only | Execute `ai-copilots/BOOTSTRAP.md` refresh content only. Do not wire yet. |

## Refresh content

Same file, different intent:

> Refresh Polypus ai-copilots content from BOOTSTRAP.md

Skips wiring unless you also ask to wire.
