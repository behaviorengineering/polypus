# Run your own Polypus on your tailnet

This guide takes you from nothing to your own Polypus gateway. The gateway runs in Docker on the computer you work on, and only your own Tailscale network can reach it. It is written for people at Digizoo, so it uses Digizoo's OpenRouter organisation for model access. Everything else applies to anyone.

You end up with one address, `https://polypus-<identifier>.<tailnet>`, that speaks the OpenAI API. Your tools call it with a client key, and the gateway holds the OpenRouter key, so no tool ever sees it.

If you use Claude Code or another coding agent, ask it to set up Polypus with the skill at [ai-copilots/skills/polypus-setup/SKILL.md](../ai-copilots/skills/polypus-setup/SKILL.md). It follows this guide, checks your machine before each step, and leaves every secret for you to type into your own terminal.

## Before you start, set up four things

### Docker

Any Docker engine that runs Compose v2 works: Docker Desktop, OrbStack or Colima on a Mac, Docker Desktop on Windows. Polypus starts its services as containers, so the engine must be running whenever you deploy.

### Tailscale, signed in to your own tailnet

Install the Tailscale app on this computer and sign in. The deploy checks your gateway through its tailnet address, so it fails when this machine is not on the tailnet. Each person uses their own tailnet, which keeps your gateway and its traces visible only to your own devices.

In the Tailscale admin console, open DNS and check two settings there. MagicDNS must be on, and HTTPS certificates must be enabled, so the gateway can answer on `https://` with a real certificate. The same page shows your tailnet name, such as `tail1234.ts.net`, which you need in step 3.

### A Tailscale auth key for the gateway

The gateway joins your tailnet as its own device, named `polypus-<identifier>`, and it uses an auth key to join the first time. In the admin console, open Settings, then Keys, and generate an auth key. Leave Ephemeral off, because an ephemeral device disappears from the tailnet when it goes offline. A one-off key is enough: the gateway keeps its identity in a Docker volume after the first join, so later deploys never use the key again.

### An invite to Digizoo's OpenRouter organisation

Ask in the `#chapter-ai` channel on Digizoo's Slack for an invite. Every model call goes through OpenRouter, and the organisation pays for it from one shared credit pool.

## 1. Install polypus

The `polypus` command is the deployer. It writes the Docker Compose stack for your environment, starts it and checks it, and every release carries the images it deploys. Install it with Go 1.27 or newer:

```bash
go install github.com/behaviorengineering/polypus/cmd/polypus@latest
polypus version
```

Go puts the binary in `$(go env GOPATH)/bin`, so add that folder to your `PATH` if `polypus version` is not found.

## 2. Create your own OpenRouter key

Accept the invite, then create an API key under the Digizoo organisation in OpenRouter's Keys page. Each person creates and keeps their own key, so revoking one never stops anyone else's gateway, and spend shows per person.

Your account carries a **weekly guardrail**: a spending allowance that resets each week. It acts as a circuit breaker for a runaway agent, so normal work should stay well under it. You can also put a smaller limit on your own key if you want an earlier stop.

Keep the key in your clipboard for step 4 and nowhere else. Polypus stores it in your operating system's keyring.

## 3. Write your environment with `polypus deploy init`

```bash
polypus deploy init
```

`init` writes two files into `~/.config/polypus/deploy/`, and it never overwrites them unless you pass `--force`. Open `config.yaml` and set two values:

| Setting | What to put there |
| --- | --- |
| `identifier` | A short name for this environment, such as `laptop`, in lower-case letters, digits and hyphens. It names the containers, the tailnet device (`polypus-laptop`) and the keyring entries, so pick it once and keep it. |
| `tailnet` | Your tailnet name from the Tailscale DNS page, such as `tail1234.ts.net`. |

Leave `observability: false` unless you want traces. Turning it on adds Phoenix, HyperDX and an OpenTelemetry collector as three more containers, which show every call in detail.

The second file, `gateway.yaml`, is the gateway's own configuration. It runs every model through OpenRouter and defines `router/turno`, the route Digizoo tools use for chat, which goes to Claude Sonnet. You can deploy it as it is.

## 4. Store your two secrets

```bash
polypus deploy secret set TS_AUTHKEY
polypus deploy secret set OPENROUTER_API_KEY
```

Each command asks for the value at a hidden prompt and stores it in your keyring under the service `polypus-<identifier>`: the macOS Keychain, or Windows Credential Manager. Always store secrets with this command. A value saved by another tool can carry a stray character that breaks the deploy later.

## 5. Deploy

Check the plan first, then deploy:

```bash
polypus deploy run --dry-run
polypus deploy run
```

The dry run prints what the deploy would do and changes nothing. The real run pulls the images, starts the gateway and the Tailscale container, then checks `https://polypus-<identifier>.<tailnet>/health` and runs short smoke checks, including a chat through `router/turno`. A good run ends with every smoke line marked `pass`.

The first deploy of a new environment can run past the two-minute health wait while Tailscale sets up the new device. We believe this is the HTTPS certificate being issued, though we have not confirmed it. Run `polypus deploy run` again, and the second run passes.

## 6. Lock the gateway with a client key

```bash
polypus deploy client-key
```

This creates a **client key** inside your running gateway, stores it in your keyring as `POLYPUS_API_KEY`, and then checks that the gateway refuses calls without it. From then on every call needs the key, so a lost or shared device on your tailnet cannot spend on your account.

On a Mac, print the key when a tool asks for it:

```bash
security find-generic-password -s polypus-<identifier> -a POLYPUS_API_KEY -w
```

## 7. Point your tools at your gateway

Use these settings in any tool that speaks the OpenAI API:

| Setting | Value |
| --- | --- |
| Base URL | `https://polypus-<identifier>.<tailnet>/v1` |
| API key | Your client key, sent as `Authorization: Bearer <key>` |
| Chat model | `router/turno` |

`/v1/models` lists every other model the gateway allows. Two that are useful beside chat are `openrouter/baai/bge-m3` for embeddings and `openrouter/fish-audio/transcribe-1-pro` for speech to text. Transcripts from the speech model can start with a speaker tag such as `<|speaker:0|>`, so strip it if your tool shows the text as it is.

## Keep it up to date

A new Polypus release reaches you when you install the newer binary and deploy again:

```bash
go install github.com/behaviorengineering/polypus/cmd/polypus@latest
polypus deploy run
```

The binary carries its own image versions, so the deploy moves the whole stack to that release. Your config, secrets, tailnet device and client key all stay as they are.

## When something goes wrong

| What you see | What to do |
| --- | --- |
| Every model call fails partway through a week | Your weekly guardrail has probably tripped, and OpenRouter refuses calls until it resets. If normal work trips it, ask in `#chapter-ai` for the limit to be looked at. |
| `setenv "<NAME>": invalid argument` during a deploy | The stored secret holds a character the deploy cannot pass on. Store it again with `polypus deploy secret set <NAME>`. |
| The health check times out on the first deploy | Run `polypus deploy run` again; see step 5. |
| The health check times out later | Check that Tailscale is connected on this computer, then look at the containers with `docker compose -p polypus-<identifier> ps` and `docker compose -p polypus-<identifier> logs gateway`. |
| You want to re-test a running gateway | `polypus deploy smoke` sends the same chat checks without deploying. |

## Later, a server of your own

A gateway on a home server or a spare machine stays up when your laptop sleeps. Set it up as a **new environment** with its own identifier, such as `server`, rather than moving your laptop's setup across. Each environment has its own tailnet device, keyring entries and client key, so the two never interfere.

On the server, follow the same steps as its login user. Store the secrets as that user, because Polypus reads them from that user's keyring.

You can deploy a server by hand, or from your own environments repository and pipeline. That repository holds each environment's two files under `envs/<name>/` and a CI job that runs a pinned release:

```bash
go run github.com/behaviorengineering/polypus/cmd/polypus@vX.Y.Z deploy run --config envs/<name>/config.yaml
```

The runner that executes this job must run as the server's login user, not as a system service account, so the job can read that user's keyring. When Polypus publishes a release, change the pinned version in a merge request; its merge deploys the new release. Secrets never go in the repository or in CI variables.
