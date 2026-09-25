package cli

import "fmt"

func agentOperatingGuide() string {
	return fmt.Sprintf(`polypus %s — OpenAI-compatible inference gateway (loopback)

ROLE & BOUNDARIES
  Routes speech and chat traffic to configured MLX or remote backends.
  Host Consilium stack-doctor and jobs depend on polypus /health when enabled.

AGENT OPERATING GUIDE
  Read AGENTS.md and ai-copilots/skills/polypus-operator/SKILL.md before changing config.
  Listening requires explicit polypus serve (never bare polypus).

COMMANDS BY RISK & LIFECYCLE
  Inspect & Validate
    version              Build identity
    processes            Print process-compose toggles from config

  Execute & Mutate
    serve                Start gateway (see --host, --port, --backend)
    switchyard-render    Render routes.toml from config

AUTOMATION RULES FOR AGENTS
  - Confirm /health before running stack-doctor model-harness.
  - Unknown commands exit non-zero; bare invoke exits 0 with this guide.
  - Full flag reference: polypus help
`, version)
}
