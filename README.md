<p align="center">
  <img src="media/banner.webp" alt="Polypus. One face. Many arms. Stay local." width="100%" />
</p>

# Polypus: one OpenAI-compatible face for every model your apps call

Polypus is an **inference gateway**. Your apps, agents and n8n flows speak the OpenAI API to one address, and Polypus routes each call to the right arm. That arm can be local MLX speech, LM Studio, OpenRouter, Claude, Gemini or Cloudflare Workers AI. The gateway holds every provider URL and key, so **your apps never hold one**.

Today a Go binary, `polypus`, serves the gateway, and an NVIDIA Switchyard sidecar handles the routers that pick a model per turn. Phoenix and HyperDX show every call.

---

- **Website:** [behaviorengineering.github.io/polypus](https://behaviorengineering.github.io/polypus/)
- **Run your own** on your machine and tailnet: [docs/setup.md](docs/setup.md)
- **Documentation:** [docs/](docs/README.md), covering install, local development, routers and releases
- **Agents** start at [AGENTS.md](AGENTS.md)

---

## 💡 One gateway learns the providers, so each app doesn't have to

Each provider has its own API shape, keys, limits and failure modes. When every app learns every provider, teams end up copying keys into each app, rewriting clients when a model moves, and guessing why a call failed.

Polypus gathers that work into one place, in four layers:

| Layer | Role |
|-------|------|
| Gateway | One OpenAI-compatible surface; picks a backend by model prefix or capability default |
| Backends | OpenAI-compatible servers, plus extensions for Claude, Gemini and Cloudflare |
| Switchyard | Named routers (`router/<name>`) that choose a cheap or strong model per turn |
| Observability | OTLP traces to Phoenix and HyperDX, and a JSON dump for each failed call |

Swapping a model, a provider or a key then becomes a **gateway config change**, and the apps stay the same.

## Licence

Polypus is licensed under Apache 2.0; see [LICENSE](LICENSE). The Switchyard image also carries NVIDIA's Switchyard licence and notice under `/usr/share/doc/polypus-switchyard/`.
