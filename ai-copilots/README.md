# Polypus ai-copilots

Operator pack for the Polypus inference gateway module.

**Minimal prompt:**

> Wire Polypus ai-copilots using BOOTSTRAP.md

Docker Compose deploy ships here as `polypus deploy`: `polypus deploy init` writes an environment under `~/.config/polypus/deploy/`, then `polypus deploy run` deploys it (`--config` picks another environment; `polypus deploy --help` for the rest). The homelab still deploys from gitlab.com/xynova/polypus-local until it switches to `polypus deploy`; load [skills/polypus-operator/windows-gitlab-deploy.md](skills/polypus-operator/windows-gitlab-deploy.md) for that path.
