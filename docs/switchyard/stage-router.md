# Stage router picks a capable or efficient model from tool progress

`type = "stage_router"` sends each turn to a **capable** target or a cheaper **efficient** one. Switchyard scores the recent tool-result history, and the picker names the default tier when that score is below `confidence_threshold` (or there are no tools yet).

The route has one public route id and two leaves, and it picks between them on each turn. It is not a three-role pipeline that plans, then reviews, then writes.

The upstream page is `providers/switchyard/docs/routing_algorithms/stage_router_routing.md`.

## How a turn is routed

1. Switchyard scores the recent tool window (`recent_turn_window`, default 3).
2. If confidence is at or above `confidence_threshold`, it follows the signal (capable vs efficient).
3. If not, and no classifier is configured, it uses the picker default.
4. Critical errors override the choice and send the turn to capable. A recent test pass plus a write can land on efficient (`tests_passed`).
5. If the chosen target blows the context window, Switchyard tries the other leaf.

With no tool results yet, the turn goes to the picker default. Plain chat with no tools will not escalate because "the answer felt wrong."

Switchyard reads these signals with no extra config:

| Axis | Direction | Looks at |
|------|-----------|----------|
| `severity` | capable | Windowed error severity |
| `spinning` | capable | Churn with no reads or writes |
| `exploring` | capable | Reading or planning without producing |
| `recent_production_intensity` | efficient | Writes and edits landing |

The scorer is corroborative, so one full "wrong" signal scores only about `0.46`. A second agreeing signal, or a critical-error override, is what usually clears `0.5`.

## Fields (out of the box)

| Key | Required | Values / notes |
|-----|----------|----------------|
| `type` | yes | `stage_router` |
| `capable_target` | yes | Target table name (stronger model) |
| `efficient_target` | yes | Target table name (cheaper model) |
| `picker` | yes | `efficient_first` or `capable_first` |
| `confidence_threshold` | yes | `[0, 1]`. Start at `0.5`. |
| `recent_turn_window` | no | Default `3`. Must be >= 1 if set. |

Both pickers read the same signals and differ only in their default. `efficient_first` stays cheap until the score says capable, and `capable_first` stays strong until the score says efficient.

Switchyard also has optional extras, described on the upstream page: `[routes.*.classifier]`, `[routes.*.handoff_notes]`, `capable_system_prompt` and `efficient_system_prompt`.

## Where the stage router fits

Each use case below keeps the same two-leaf type and changes the picker, threshold and window. Copy a block and change the target ids.

### Investigation and triage

The cheap leaf gathers facts and proposes the next probe. The capable leaf takes over when the search is looping or the evidence conflicts.

It suits log triage, "where does X live" questions, support threads, and reading tickets then listing checks.

It fits poorly for a test-and-patch loop ([debugger](#debug-and-fix-loops) below), always-strong drafting ([passthrough](passthrough.md)), or work where the first turn is always hard ([capable-first intake](#intake-start-strong)).

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

A loop that reproduces, patches and re-runs is the shape that matches tool-progress signals best.

It suits failing tests in-session, a stack trace plus a checkout, an agent spinning on the same error, and CI bisect.

It fits poorly for open-ended research with no failing test, or a one-shot "explain this panic" with no tools, where you stay on efficient.

A lower threshold and a longer window make this route escalate sooner than the investigation route.

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

In a typical thread, the first turns have no tools, so they run on efficient. Writes and a green test stay on efficient, repeated failure or spinning moves to capable, and after a real fix the thread goes back to efficient. Both leaves see the same `messages` history.

### Coding while edits land

This route produces code cheaply as long as files keep changing. It escalates when the agent explores without producing, or when a compile loop starts.

It suits scaffolding, mechanical refactors, and "fill in this module" after a plan exists in `messages`.

It fits poorly when architecture is the main job, because the cheap leaf will do that work until the tools look stuck. It also has no review leaf, so use [passthrough](passthrough.md) for review.

With `0.5` and a window of 3, this route stays cheaper for longer than the debugger route.

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

Here the first turn is usually the hard one, such as a new incident, an unknown repo or a long dump. Later turns drop to efficient when production looks settled.

It suits frame-then-execute work, or orientation followed by a checklist.

It fits poorly for cost-first research, which suits the `efficient_first` investigation route. When every turn must stay strong, use [passthrough](passthrough.md). Without tools you pay for capable for the whole session, so passthrough is simpler there too.

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
- Judge the prompt before any tool work: [llm-classifier](llm-classifier.md).
- Judge the run after the cheap model tried: [escalation](escalation.md).
- Never switch models: [passthrough](passthrough.md).
