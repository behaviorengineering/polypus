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
- Use **minor** or **major** only via workflow_dispatch or an explicit user request. Dispatch it from `main` only: a run from another branch skips every job. A manual run passes `go tool task ci` and builds `switchyard-server` before it tags.
- Skip auto release for docs/chore/ci-only commits, agent-harness-only diffs (`.cursor/`, `lefthook.yml`, `AGENTS.md`, `ai-copilots/`, `docs/`, `site/`), or `[skip release]` in the push subject (`auto-patch-decide.sh`).
- A diff of packaging paths plus agent-harness paths ships as an image revision `X.Y.Z-rN` through `packaging-rebuild.yml`, not as a new `v*` tag (see [docs/releases.md](../../../docs/releases.md)).
- MUST NOT push a `v*` tag by hand: no workflow runs on a tag push, so it would publish no binaries and no images. Every manual release goes through `auto-patch-release.yml` with `workflow_dispatch` (`bump=patch|minor|major`).
- Confirm the GitHub Release has multi-platform binaries, `checksums.txt`, and changelog groups.
- Confirm GHCR tags `ghcr.io/behaviorengineering/polypus:<semver>` and `polypus-switchyard:<semver>` after auto-patch (same workflow via `workflow_call`). `:latest` moves only after the image smoke and the manifest checks pass, and never for an `-rN` revision.

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

## Check the GoReleaser config

```bash
goreleaser check   # local config validation only; releases come from auto-patch-release.yml
```

## Install note for users

Download the `polypus` archive for your OS/arch from the GitHub Release matching the tag. Contributors build locally with `go tool task build` or `go run ./cmd/polypus`.
