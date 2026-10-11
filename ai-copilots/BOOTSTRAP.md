# BOOTSTRAP — Polypus ai-copilots

**Audience:** Any AI agent in a host repo that depends on Polypus.

## Resolve module root

```bash
MODULE_ROOT="$(go list -m -f '{{.Dir}}' github.com/behaviorengineering/polypus)"
test -f "$MODULE_ROOT/ai-copilots/BOOTSTRAP.md"
```

## Wire mode (Cursor example)

```bash
mkdir -p .cursor/skills .cursor/rules
ln -snf "$MODULE_ROOT/ai-copilots/skills/polypus-operator" .cursor/skills/polypus-operator
ln -snf "$MODULE_ROOT/ai-copilots/rules/openai-compat-gateway.mdc" .cursor/rules/openai-compat-gateway.mdc
```

**MUST NOT** copy skill bodies into the host unless links fail and the human approves.

## Secret resolution for deploy

Homelab Compose uses **operatorconfig**, not ad-hoc Keychain scripts in this repo:

```bash
OC="$(go list -m -f '{{.Dir}}' github.com/behaviorengineering/operatorconfig)"
# Wire operatorconfig skills from $OC/ai-copilots/BOOTSTRAP.md
```

Load [operatorconfig skill]($OC/ai-copilots/skills/operatorconfig/SKILL.md) via module path after wire.

## Load order

1. [skills/polypus-operator/SKILL.md](skills/polypus-operator/SKILL.md)
2. Deploy shard when on Windows/Mac Compose: [windows-gitlab-deploy.md](skills/polypus-operator/windows-gitlab-deploy.md)
