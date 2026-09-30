# Polypus

OpenAI-compatible gateway and switchyard sidecar.

## Releases (for agents)

1. **Full product release:** merge releasable changes to `main`. **Auto patch release** cuts git tag `vX.Y.Z`, runs GoReleaser, calls **Docker release** (reusable workflow), and **verify** inspects GHCR `X.Y.Z` (and `:latest` when applicable). Binaries and images share app version `X.Y.Z`.

2. **Packaging-only / base image rebuild:** when the diff since the last `v*` tag touches only Docker/packaging paths, **Auto patch** skips Go tagging. **Packaging rebuild** (after green **CI** on `main`, or manual dispatch) publishes immutable GHCR tags `X.Y.Z-rN` (`N >= 1`) using binaries from the existing `vX.Y.Z` GitHub Release. Do not cut a new Go tag for Dockerfile-only work.

3. **OCI labels:** `org.opencontainers.image.version` is always app semver `X.Y.Z`. Rebuild counter appears only in the image tag and `com.behaviorengineering.image.rebuild-revision` (`rN` or empty).

4. **Consumers:** pin `ghcr.io/ORG/polypus:X.Y.Z` or `X.Y.Z-rN` in `images.env` after publish-complete (full Auto patch verify for plain tags; Docker release success for `-rN`).

Manual packaging rebuild:

```bash
gh workflow run packaging-rebuild.yml -f tag=v0.2.40 -f image_revision=1
```
