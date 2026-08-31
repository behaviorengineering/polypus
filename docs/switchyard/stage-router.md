# Stage router

`type = "stage_router"` sends each turn to a **capable** target or a cheaper **efficient** one. Switchyard scores recent **tool-result** history. The picker names the default tier when that score is below `confidence_threshold` (or there are no tools yet).

This is not a three-role pipeline (plan, then review, then write). One public route id; two leaves; pick per turn.

Upstream: `providers/switchyard/docs/routing_algorithms/stage_router_routing.md`.

## How a turn is routed

1. Score the recent tool window (`recent_turn_window`, default 3).
2. If confidence is at or above `confidence_threshold`, follow the signal (capable vs efficient).
3. If not, and no classifier is configured, use the picker default.
4. Critical errors override to capable. A recent test pass plus a write can land on efficient (`tests_passed`).
5. If the chosen target blows the context window, Switchyard tries the other leaf.

No tool results yet: picker default. Plain chat with no tools will not escalate because "the answer felt wrong."

Signals (no extra config):

| Axis | Direction | Looks at |
|------|-----------|----------|
| `severity` | capable | Windowed error severity |
| `spinning` | capable | Churn with no reads or writes |
| `exploring` | capable | Reading or planning without producing |
| `recent_production_intensity` | efficient | Writes and edits landing |

The scorer is corroborative. One full "wrong" signal is about `0.46`. A second agreeing signal, or a critical-error override, is what usually clears `0.5`.

## Fields (out of the box)

| Key | Required | Values / notes |
|-----|----------|----------------|
| `type` | yes | `stage_router` |
| `capable_target` | yes | Target table name (stronger model) |
| `efficient_target` | yes | Target table name (cheaper model) |
| `picker` | yes | `efficient_first` or `capable_first` |
| `confidence_threshold` | yes | `[0, 1]`. Start at `0.5`. |
| `recent_turn_window` | no | Default `3`. Must be >= 1 if set. |

Picker: same signals, different default. `efficient_first` stays cheap until the score says capable. `capable_first` stays strong until the score says efficient.

Optional Switchyard extras (not required): `[routes.*.classifier]`, `[routes.*.handoff_notes]`, `capable_system_prompt`, `efficient_system_prompt`. See the upstream page.

## Use cases

Same two-leaf type; different picker, threshold, and window. Copy a block and change target ids.

### Investigation and triage

Cheap leaf gathers facts and proposes the next probe. Capable leaf takes over when search is looping or evidence conflicts.

Good fits: log triage, "where does X live," support threads, reading tickets then listing checks.

Poor fits: a test-and-patch loop ([debugger](#debug-and-fix-loops) below); always-strong drafting ([passthrough](passthrough.md)); first-turn-always-hard ([capable-first intake](#intake-start-strong)).

```toml
[routes.investigator]
id = "investigator"
type = "stage_router"
capable_target = "strong"
efficient_target = "weak"
picker = "efficient_first"
confidence_threshold = 0.5
recent_turn_window = 3
```

### Debug and fix loops

Reproduce, patch, re-run. This is the shape that matches tool-progress signals best.

Good fits: failing tests in-session, stack trace plus checkout, agent spinning on the same error, CI bisect.

Poor fits: open-ended research with no failing test; one-shot "explain this panic" with no tools (you stay on efficient).

Lower threshold and a longer window escalate sooner than investigation.

```toml
[routes.debugger]
id = "debugger"
type = "stage_router"
capable_target = "strong"
efficient_target = "weak"
picker = "efficient_first"
confidence_threshold = 0.4
recent_turn_window = 4
```

Typical thread: first turns have no tools so efficient; writes and a green test stay efficient; repeated failure or spin goes capable; after a real fix, back to efficient. Both leaves see the same `messages` history.

### Coding while edits land

Produce code cheaply as long as files keep changing. Escalate when the agent explores without producing, or a compile loop starts.

Good fits: scaffolding, mechanical refactors, "fill in this module" after a plan exists in `messages`.

Poor fits: architecture as the main job (cheap leaf will do it until tools look stuck); a required review leaf (there isn't one; use [passthrough](passthrough.md) for review).

Stays cheaper longer than debugger (`0.5`, window 3).

```toml
[routes.coder]
id = "coder"
type = "stage_router"
capable_target = "strong"
efficient_target = "weak"
picker = "efficient_first"
confidence_threshold = 0.5
recent_turn_window = 3
```

### Intake (start strong)

First turn is usually the hard one: new incident, unknown repo, long dump. Later turns drop to efficient when production looks settled.

Good fits: frame-then-execute, orientation then checklist.

Poor fits: cost-first research (`efficient_first` investigation); every turn must stay strong ([passthrough](passthrough.md)); no tools (you pay capable for the whole session; passthrough is simpler).

```toml
[routes.intake]
id = "intake"
type = "stage_router"
capable_target = "strong"
efficient_target = "weak"
picker = "capable_first"
confidence_threshold = 0.5
recent_turn_window = 3
```

## When not to use this type

- Fixed traffic split: [random](random.md).
- Judge the **prompt** before any tool work: [llm-classifier](llm-classifier.md).
- Judge the **run** after the cheap model tried: [escalation](escalation.md).
- Never switch models: [passthrough](passthrough.md).
