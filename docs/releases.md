# How releases reach binaries and images

This page is for agents and maintainers who cut or pin Polypus releases.

1. To cut a full product release, merge releasable changes to `main`. The Auto patch release workflow cuts git tag `vX.Y.Z`, runs GoReleaser and calls the reusable Docker release workflow. Its verify job then inspects the GHCR `X.Y.Z` manifests for both platforms. Binaries and images share app version `X.Y.Z`.

2. A minor or major bump is manual. Run the Auto patch release workflow by hand, from `main` only; a run dispatched from any other branch does nothing. A manual run first passes `go tool task ci` and builds `switchyard-server`, then releases as an automatic run does. Never push a `v*` tag by hand: no workflow builds binaries or images for it.

3. A packaging-only or base image rebuild covers a diff since the last `v*` tag that touches only packaging paths (the Dockerfiles, `docker-bake.hcl` and the Docker scripts and workflows), or packaging paths plus agent-harness paths (`AGENTS.md`, `ai-copilots/`, `docs/`, `site/` and the like). Harness paths ship no new binary, so such a diff skips Go tagging too. The Packaging rebuild workflow runs after a green CI run on `main`, or on manual dispatch. It publishes immutable GHCR tags `X.Y.Z-rN` (`N >= 1`) using binaries from the existing `vX.Y.Z` GitHub Release. Do not cut a new Go tag for Dockerfile-only work.

4. The Docker release pushes only the version tag (`X.Y.Z` or `X.Y.Z-rN`) at first. It then pulls the image and checks that the binary loads and `/health` answers, verifies the GHCR manifests, platforms and labels, and only then moves `:latest`. A failed check leaves `:latest` on the previous release. Only a plain `X.Y.Z` moves `:latest`; an `X.Y.Z-rN` never does.

5. The OCI label `org.opencontainers.image.version` is always the app semver `X.Y.Z`. The rebuild counter appears only in the image tag and in `com.behaviorengineering.image.rebuild-revision`, which holds `rN` or is empty.

6. Consumers pin `ghcr.io/ORG/polypus:X.Y.Z` or `X.Y.Z-rN` in `images.env` after publish-complete. For a plain tag that means the full Auto patch verify; for `-rN` it means Docker release success.

7. Both images are built for `linux/amd64` and `linux/arm64` from `docker-bake.hcl` in one bake, and Switchyard builds natively on an arm64 runner. A packaging rebuild of a tag released before arm64 builds stays `linux/amd64`. Both images run as the non-root uid and gid 10001.

To run a packaging rebuild by hand:

```bash
gh workflow run packaging-rebuild.yml -f tag=v0.2.40 -f image_revision=1
```
