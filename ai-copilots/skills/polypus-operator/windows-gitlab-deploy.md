# Homelab GitLab Compose deploy (Windows and Mac)

Operator path for **Docker Compose** on a home runner (not Mac `go tool task serve` / MLX).

## Where

- **Deploy repo:** [gitlab.com/xynova/polypus-local](https://gitlab.com/xynova/polypus-local) (private). The GitLab runner checks out this repo each job; you do not maintain a separate manual clone for updates.
- **Product images:** `ghcr.io/behaviorengineering/polypus` and `polypus-switchyard` on GitHub `v*` tags (semver in GHCR, not `:latest` on homelab).
- **Image pins:** committed `images.env` in polypus-local (gateway, switchyard, Phoenix, HyperDX). Renovate bumps observability images; deploy bumps Polypus/Switchyard from GitHub releases.
- **Host config:** `%USERPROFILE%\.config\polypus\config.yaml` (Windows) or `~/.config/polypus/config.yaml` (macOS), seeded once from `config.deploy.yaml.example`. Omit `secrets:`; inject `CF_*` via compose env from the OS keychain. Mac `polypus serve` still uses `secrets:` in the same user config path.

## One-time on the host

1. Docker Desktop.
2. GitLab Runner (`windows-home` / `macos-home`) as **login user** (not `LocalSystem`).
3. Git for Windows (deploy uses `bash` helper scripts).
4. Keyring service `polypus`: `CF_AI_API_KEY`, `CF_ACCOUNT_ID`.
5. Enable Renovate on polypus-local; allow GitLab job token push for bump commits.

## Trigger

**GitHub Packages** webhook on `behaviorengineering/polypus` when GHCR publishes → GitLab pipeline trigger on `xynova/polypus-local` (`ref=main`) → `deploy:windows` / `deploy:macos` (`pull` + `up` pinned tags).

There is no GitHub Actions deploy job. Do not also subscribe **Pushes** on the same webhook unless you want a second compose run on merge to `main`. Full steps: polypus-local `ai-copilots/skills/polypus-local-operator/SKILL.md` and `docs/PROVE-CD.md`.

## Secrets (operatorconfig)

Cloudflare credentials are **not** in GitHub or GitLab CI variables.

| Store | Value |
|-------|--------|
| Keyring service | `polypus` |
| Accounts | `CF_AI_API_KEY`, `CF_ACCOUNT_ID` |

Deploy scripts call **operatorconfig** `export-env` (`env` → keyring → optional local SOPS file). MUST NOT run `sops`, `security`, or Credential Manager from polypus-local scripts.

Wire operatorconfig skills:

```bash
go list -m -f '{{.Dir}}' github.com/behaviorengineering/operatorconfig
# execute that module's ai-copilots/BOOTSTRAP.md
```

## GitLab CI

| Job | Tag | Script |
|-----|-----|--------|
| `deploy:windows` | `windows-home` | `scripts/deploy.ps1` |
| `deploy:macos` | `macos-home` | `scripts/deploy.sh` |

## Health

```bash
curl -sf http://127.0.0.1:1320/health
curl -sf http://127.0.0.1:1320/health/backends
```

Switchyard must be healthy when `routers:` are configured. `routes.toml` must use `http://gateway:1320/v1`, not `127.0.0.1`.
