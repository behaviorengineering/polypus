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
- Skip auto release for docs/chore/ci-only commits or `[skip release]` in the push subject.
- Manual tag path: annotated `vMAJOR.MINOR.PATCH`, `git push origin vX.Y.Z` (triggers `release.yml`).
- Confirm the GitHub Release has multi-platform binaries, `checksums.txt`, and changelog groups.

## Must not

- Force-move or delete published tags.
- Tag dirty trees or feature branches.
- Add Homebrew taps or Docker release artifacts unless explicitly requested.

## Auto patch (usual path)

After a releasable merge to `main`, wait for **Auto patch release** to finish:

```bash
gh run list --workflow=auto-patch-release.yml --limit 3
gh release list --limit 3
polypus version   # after downloading the new asset
```

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
