# Polypus documentation

Polypus is an OpenAI-compatible gateway: your apps call one address, and Polypus routes each call to the right backend. These pages cover running it, deploying it and choosing its routers. The [README](../README.md) explains why it exists.

## What ships today

| Capability | How |
|------------|-----|
| Chat and vision | `POST /v1/chat/completions`, streamed or not, with tools |
| Embeddings | `POST /v1/embeddings` |
| Speech | `POST /v1/audio/speech`, `/v1/audio/transcriptions`, `GET /v1/audio/voices` |
| Batches | `POST /v1/files` and `/v1/batches` over the provider's own batch API |
| SystemOne | `POST /v1/systemone` for TypeSafe and Decider structured evaluation |
| Model catalogue | `GET /v1/models`, with an optional allow list per backend |
| Routers | `model: router/<name>`: stage router, LLM classifier, random, passthrough, fallback |
| Access keys | Optional lock with gateway keys (`polypus admin-key`) |
| Failure dumps | `GET /debug/failures/<trace id>` returns what went wrong, readable by an agent |

## Deploy it for a team

`polypus deploy` runs the whole stack with Docker Compose, which includes the gateway, Switchyard, an OTLP collector, Phoenix, HyperDX and Tailscale. Nothing in the stack publishes a host port. People reach the gateway at `https://polypus-<identifier>.<tailnet>`, so **only machines on your tailnet** can call it.

```bash
polypus deploy init                       # a starting environment under ~/.config/polypus/deploy/
polypus deploy secret set TS_AUTHKEY      # each secret goes to the OS keyring
polypus deploy run --dry-run              # prints the plan; writes nothing
polypus deploy run                        # deploys, then checks health and smoke tests every router
polypus deploy client-key                 # optional: locks the gateway with a client key
```

The binary carries its own stack, so you don't need a Polypus checkout. Each environment is its own compose project, Tailscale host and keyring service, so several can run side by side on one machine.

For the full walk-through, from an empty machine to a working gateway, follow [Run your own Polypus on your tailnet](setup.md).

## Where to go next

- [Install a release binary](install.md) to get the `polypus` command.
- [Run Polypus on your machine](run-locally.md) to work on the gateway itself.
- [Choosing a Switchyard routing type](switchyard/README.md) to pick a router per route.
- [How releases reach binaries and images](releases.md) for release and image tags.
