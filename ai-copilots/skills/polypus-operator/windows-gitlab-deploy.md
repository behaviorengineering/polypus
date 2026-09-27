# Homelab GitLab Compose deploy (Windows and Mac)

Operator path for **Docker Compose** on a home runner (not Mac `make serve` / MLX).

## Where

- **Deploy repo:** [gitlab.com/xynova/polypus-local](https://gitlab.com/xynova/polypus-local) (private). Clone that repo on the runner; do not use nested `deploy/` under the Polypus GitHub tree.
- **Product images:** `ghcr.io/behaviorengineering/polypus` and `polypus-switchyard` on GitHub `v*` tags.
- **Compose file:** `docker-compose.deploy.yml` in polypus-local (synced from product releases).
- **Config template:** `config.deploy.yaml.example` → gitignored `config/config.yaml`. Omit `secrets:` in that file; inject `CF_*` via compose env only (no keyring inside the Linux gateway container). Host `polypus serve` uses `secrets:` in `~/.config/polypus/config.yaml` instead.

## Trigger

Canonical: **GitHub Packages** webhook on `behaviorengineering/polypus` when GHCR publishes an image (`package` / `registry_package`, UI label **Packages**) → GitLab pipeline trigger on `xynova/polypus-local` (`ref=main`) → homelab runners `docker compose`.

There is no GitHub Actions deploy job. Do not also subscribe **Pushes** on the same trigger unless you want a second compose run on merge to `main`. Full steps: polypus-local `ai-copilots/skills/polypus-local-operator/SKILL.md`.

## Secrets (operatorconfig)

Cloudflare credentials are **not** in GitHub or GitLab CI variables.

| Store | Value |
|-------|--------|
| Keyring service | `polypus` |
| Accounts | `CF_AI_API_KEY`, `CF_ACCOUNT_ID` |

Deploy scripts call **operatorconfig** `export-env` (`env` → keyring → optional local SOPS file). MUST NOT run `sops`, `security`, or Credential Manager from polypus-local scripts.

Optional: encrypted `~/.config/polypus/secrets.enc.yaml` via operatorconfig; never commit ciphertext to polypus-local git.

Runner MUST run as the user who created keyring items (login keychain unlocked; not `LocalSystem`).

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

Scripts assert OS before compose.

## Health

```bash
curl -sf http://127.0.0.1:1320/health
curl -sf http://127.0.0.1:1320/health/backends
```

Switchyard must be healthy when `routers:` are configured. `routes.toml` must use `http://gateway:1320/v1`, not `127.0.0.1`.

Full checklist: polypus-local repo README and `ai-copilots/skills/polypus-local-operator/SKILL.md`.
