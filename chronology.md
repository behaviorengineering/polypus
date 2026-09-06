# Chronology

Newest first.

### 2026-09-06 - majordomo - fb6dc974fc2e

- **Did:** Capability backend blocks and remove cloud opt-in gate (#12)
- **Because:** * Replace scalar capability defaults with enabled/default backend blocks.
- **In order to:** advance context cursor on default first-parent tape
- **Evidence:** commit fb6dc974fc2e91d3fd97d664ca812b6a927cc0e4; files: .cursor/packs/shared, .cursor/rules/repository-boundaries.mdc, README.md, ai-copilots/agents/polypus-operator.md, ai-copilots/skills/polypus-operator/SKILL.md, ai-copilots/skills/polypus-operator/config-reference.md, ai-copilots/skills/polypus-operator/troubleshooting.md, config.cloud.yaml.example, config.yaml.example, internal/cli/serve.go, internal/config/env.go, internal/config/env_test.go, internal/config/policy.go, internal/config/policy_test.go, internal/config/processes_test.go, internal/config/router_config.go, internal/config/router_config_test.go, internal/config/routers_test.go, internal/extension/cloudflare/bifrost_speech_plugin_test.go, internal/gateway/circuit_test.go, internal/gateway/models.go, internal/gateway/models_helpers_test.go, internal/gateway/named_router_test.go, internal/gateway/router_test.go, internal/gateway/server.go, internal/gateway/server_test.go, internal/gateway/switchyard_startup_test.go, internal/gateway/tracing_test.go, internal/router/account_test.go, internal/router/cf_speech_plugin_integration_test.go, internal/router/policy.go, internal/router/policy_test.go, internal/router/registry.go, internal/router/registry_test.go, process-compose.yaml, scripts/pc-up.sh, scripts/smoke-env.sh

### 2026-09-06 - majordomo - 31cb98a5c72f

- **Did:** Remove obsolete named-routers personas plan draft.
- **Because:** Switchyard routers shipped; the planning doc was unused historical noise.
- **In order to:** advance context cursor on default first-parent tape
- **Evidence:** commit 31cb98a5c72f6948edda6eb6397f232b79ce6247; files: personas-plan.md
