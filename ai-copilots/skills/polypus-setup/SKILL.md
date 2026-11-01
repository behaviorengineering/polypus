---
name: polypus-setup
description: >-
  Walks a person through standing up their own Polypus on their own tailnet
  with polypus deploy, following docs/setup.md: checks Docker, Go and
  Tailscale (signed in, MagicDNS, HTTPS certificates) before the first deploy,
  has the person store secrets in their own terminal, retries the first
  deploy's health timeout once, and checks the gateway answers.
  Use when someone asks to set up, install or run their own Polypus, a laptop
  or single-machine setup, a server as a new environment, or says the first
  polypus deploy run timed out. Not for operating or debugging a running
  gateway (polypus-operator).
---

# Polypus setup

**Moral:** [docs/setup.md](../../../docs/setup.md) owns the steps. This skill checks the machine before a step and the result after it, and never handles a secret.

## Core constraints

**CONSTRAINT 1: The guide owns the steps.** MUST read the setup guide before step 1 and follow its order: install, OpenRouter key, `init`, secrets, deploy, tool settings. The client key is optional, for a shared tailnet only. MUST take flags from `polypus deploy <command> --help`, not from memory.

- Guide location: `docs/setup.md` in a Polypus checkout; else `gh api repos/behaviorengineering/polypus/contents/docs/setup.md -H 'Accept: application/vnd.github.raw'`; else `https://raw.githubusercontent.com/behaviorengineering/polypus/main/docs/setup.md`.
- Enforcement: the guide was read in this session before the first command; every flag used appears in that command's `--help`.
- Violation: STOP, read the guide, redo the step from it.

**CONSTRAINT 2: Secrets never pass through the agent.** The person types every secret into their own terminal.

- MUST: hand the person `polypus deploy secret set TS_AUTHKEY` and `polypus deploy secret set OPENROUTER_API_KEY` to run themselves, at the hidden prompt.
- MUST NOT: ask for a key in chat, run `secret set --stdin` with a value, run `security find-generic-password ... -w`, or print, log or echo a secret.
- MUST: check a secret exists without reading it: macOS `security find-generic-password -s polypus-<identifier> -a <NAME> >/dev/null && echo present`; Windows `cmdkey /list | findstr "polypus-<identifier>:<NAME>"`. On Linux, skip the check and let the deploy report a missing secret.
- Enforcement: no secret value appears in the conversation or in any command the agent ran.
- Violation: STOP, tell the person which key was exposed, and have them revoke and replace it (Tailscale admin console, or OpenRouter's Keys page), then store the new one with `secret set`.

CORRECT:

```text
Run this in your own terminal and paste the key at the hidden prompt:
  polypus deploy secret set OPENROUTER_API_KEY
Then tell me when it says it stored it.
```

PROHIBITED:

```text
Paste your OpenRouter key here and I'll store it for you.
```

**CONSTRAINT 3: Prerequisites pass before the first deploy.** MUST run the [prerequisite gate](#prerequisite-gate) and MUST NOT run `polypus deploy run` until every row passes or the person confirmed it.

- Enforcement: the completed gate is in the conversation before the first `deploy run`.
- Violation: STOP, run the gate, fix the failing row.

**CONSTRAINT 4: One retry, for the first deploy's health timeout only.** The first deploy of a new identifier can fail with `health: timeout waiting for https://polypus-<identifier>.<tailnet>` while Tailscale sets up the device.

- MUST: IF the first `deploy run` of this identifier fails with that message → run `polypus deploy run` once more.
- MUST: IF it fails again, or fails with any other message → STOP retrying; match the message in the guide's "When something goes wrong" table and follow that row.
- MUST NOT: pass `--skip-smoke`, `--skip-pull` or a longer `--health-timeout` to get a green run.
- Enforcement: at most two `deploy run` calls before a passing run or a diagnosis.
- Violation: STOP, report both outputs, diagnose from the table.

**CONSTRAINT 5: Show the plan before a change.** MUST run `polypus deploy run --dry-run` before the first `deploy run`, show its plan, and wait for the person's yes. `init`, `secret set` and `client-key` need no dry run.

- Enforcement: a dry-run output and the person's yes precede the first real run.
- Violation: STOP, show what ran, ask whether to keep it.

## Prerequisite gate

Run each command, fill the row, then show the gate.

```text
Docker engine:   `docker info --format '{{.ServerVersion}}'` → [version | FAIL: <error>]
Docker Compose:  `docker compose version` → [version line | FAIL]
Go:              `go version` → [go1.27 or newer | FAIL: <version>]
Tailscale:       `tailscale status --json` → BackendState [Running | FAIL: <state>]
MagicDNS:        same output, CurrentTailnet.MagicDNSEnabled → [true | FAIL]
HTTPS certs:     same output, CertDomains → [non-empty | FAIL: empty, turn on HTTPS certificates under DNS]
Tailnet name:    same output, CurrentTailnet.MagicDNSSuffix → [name, used for `tailnet:`]
Auth key:        person confirms a non-ephemeral key for this tailnet → [confirmed | WAIT]
OpenRouter key:  person confirms a key created under the Digizoo organisation → [confirmed | WAIT]
Result:          [all pass → continue | FAIL or WAIT → fix that row first]
```

- On macOS without `tailscale` on `PATH`, run `/Applications/Tailscale.app/Contents/MacOS/Tailscale status --json`.
- Any `docker compose version` line passes; the guide's "Compose v2" means the `docker compose` plugin, not a major version.

## Steps

1. **Read the guide** (constraint 1).
2. **Gate:** run the [prerequisite gate](#prerequisite-gate) (constraint 3).
3. **Install:** IF `polypus version` fails → give the guide's `go install` line; IF it is still not found → add `$(go env GOPATH)/bin` to `PATH`.
4. **Environment:** ask the person for an identifier (lower-case letters, digits, hyphens; once chosen, kept). DO `polypus deploy init`. DO set `identifier` and `tailnet` (from the gate) in `~/.config/polypus/deploy/config.yaml`. Keep `observability: true`; traces in Phoenix and HyperDX are part of every setup.
5. **Secrets:** hand over the two `secret set` commands; check both exist (constraint 2).
6. **Deploy:** dry run, the person's yes, then `polypus deploy run` (constraints 4 and 5). Pass: the run ends with every smoke line `pass`.
7. **Lock, only if the tailnet is shared:** ask whether anyone else's device is on the person's tailnet, or whether they share the gateway's device in Tailscale. IF yes → `polypus deploy client-key`; pass: it reports the 401 without the key and the 200 with it. IF no → skip; the tailnet decides who reaches the gateway.
8. **Check:** `polypus deploy smoke` passes, and `curl -s -o /dev/null -w '%{http_code}' https://polypus-<identifier>.<tailnet>/v1/models` prints `200`, or `401` when step 7 locked the gateway.
9. **Hand over:** give the guide's tool settings table with the real identifier and tailnet filled in. IF step 7 locked the gateway → tell the person to print the client key themselves with the command in the guide's "Lock the gateway if others share your tailnet" section; ELSE → any text works as the API key.

A **server setup** is a new environment with its own identifier, run as the server's login user (the guide's "Later, a server of your own"). IF the agent is not running on the server → hand the person the gate and steps as a checklist to run there.

## Pre-completion checklist

- [ ] **No secret seen**
      Method: search the conversation and the commands run for key values, `--stdin` with a value, and `-w`
      Pass: none
      Fail: STOP, have the person revoke and replace the key (constraint 2)
- [ ] **Gate passed before the deploy**
      Method: find the completed gate above the first `deploy run`
      Pass: every row passes or is confirmed
      Fail: STOP, run the gate
- [ ] **At most one retry**
      Method: count `deploy run` calls before the passing one
      Pass: two or fewer, the second only after a first-deploy health timeout
      Fail: STOP, diagnose from the guide's table
- [ ] **Gateway answering**
      Method: `polypus deploy smoke`, then the `curl` from step 8
      Pass: smoke passes; `curl` prints `200`, or `401` when step 7 locked the gateway
      Fail: STOP, diagnose from the guide's table; IF a locked gateway prints `200` → rerun `polypus deploy client-key`, then recheck
