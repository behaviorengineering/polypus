---
name: release-polypus
description: >-
  Cut or verify semver GitHub Releases for polypus: auto-patch on main,
  manual minor/major via workflow_dispatch, or annotated v* tag push.
  Use when shipping a new polypus binary release.
---

# Release polypus

Tag-triggered and auto-patch releases via GoReleaser (`.goreleaser.yaml`).

## Must

- Release only from green `main` (CI passing).
- Default bump on merge is **patch** (`auto-patch-release.yml` on push to `main`).
- Use **minor** or **major** only via workflow_dispatch or an explicit user request.
- Skip auto release for docs/chore/ci-only commits, agent-harness-only diffs (`.cursor/`, `lefthook.yml`), or `[skip release]` in the push subject (`auto-patch-decide.sh`).
- Manual tag path: annotated `vMAJOR.MINOR.PATCH`, `git push origin vX.Y.Z` (triggers `release.yml` and tag-push `docker-release.yml`).
- Confirm the GitHub Release has multi-platform binaries, `checksums.txt`, and changelog groups.
- Confirm GHCR tags `ghcr.io/behaviorengineering/polypus:<semver>` and `polypus-switchyard:<semver>` after auto-patch (same workflow via `workflow_call`).

## Must not

- Force-move or delete published tags.
- Tag dirty trees or feature branches.
- Rely on auto-patch tag push alone to publish Docker images (sibling `on: push: tags` does not run for `GITHUB_TOKEN` tags).

## Auto patch (usual path)

After a releasable merge to `main`, wait for **Auto patch release** to finish:

```bash
gh run list --workflow=auto-patch-release.yml --limit 3
gh release list --limit 3
docker pull ghcr.io/behaviorengineering/polypus:<semver>
polypus version   # after downloading the new asset
```

Backfill GHCR for an existing release tag (no new semver): **Actions → Docker release → Run workflow** with `tag=vX.Y.Z` and `git_ref=main` (or the tag ref).

## Manual bump

```bash
gh workflow run auto-patch-release.yml -f bump=minor
gh run watch
gh release view vX.Y.Z
```

## Manual tag (fallback)

```bash
git checkout main && git pull --ff-only && git status
git tag -a v0.2.1 -m "v0.2.1"
git push origin v0.2.1
gh run watch --workflow=release.yml
gh release view v0.2.1
goreleaser check   # local config validation only
```

## Install note for users

Download the `polypus` archive for your OS/arch from the GitHub Release matching the tag. Contributors build locally with `make build` or `go run ./cmd/polypus`.
