---
name: release-polypus
description: >-
  Cut a semver GitHub Release for polypus: annotated v* tag from green main,
  push tag, verify GoReleaser workflow and Release assets. Use when shipping
  a new polypus binary release.
---

# Release polypus

Tag-triggered releases via GoReleaser (`.goreleaser.yaml`, `.github/workflows/release.yml`).

## Must

- Release only from green `main` (CI passing).
- Use annotated semver tags: `vMAJOR.MINOR.PATCH`.
- Push the tag: `git push origin vX.Y.Z` — this triggers the Release workflow.
- Confirm the GitHub Release has multi-platform binaries, `checksums.txt`, and changelog groups.

## Must not

- Force-move or delete published tags.
- Tag dirty trees or feature branches.
- Add Homebrew taps or Docker release artifacts unless explicitly requested.

## Procedure

```bash
git checkout main && git pull --ff-only && git status
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
gh run watch
gh release view v0.1.0
goreleaser check   # local config validation only
```

## Install note for users

Download the `polypus` archive for your OS/arch from the GitHub Release matching the tag. Contributors build locally with `make build` or `go run ./cmd/polypus`.
