# How releases reach binaries and images

This page is for agents and maintainers who cut or pin Polypus releases.

1. To cut a full product release, merge releasable changes to `main`. The Auto patch release workflow cuts git tag `vX.Y.Z`, runs GoReleaser and calls the reusable Docker release workflow. Its verify job then inspects GHCR `X.Y.Z` (and `:latest` when applicable). Binaries and images share app version `X.Y.Z`.

2. A packaging-only or base image rebuild covers a diff since the last `v*` tag that touches only Docker or packaging paths. In that case Auto patch skips Go tagging. The Packaging rebuild workflow runs after a green CI run on `main`, or on manual dispatch. It publishes immutable GHCR tags `X.Y.Z-rN` (`N >= 1`) using binaries from the existing `vX.Y.Z` GitHub Release. Do not cut a new Go tag for Dockerfile-only work.

3. The OCI label `org.opencontainers.image.version` is always the app semver `X.Y.Z`. The rebuild counter appears only in the image tag and in `com.behaviorengineering.image.rebuild-revision`, which holds `rN` or is empty.

4. Consumers pin `ghcr.io/ORG/polypus:X.Y.Z` or `X.Y.Z-rN` in `images.env` after publish-complete. For a plain tag that means the full Auto patch verify; for `-rN` it means Docker release success.

5. Both images are built for `linux/amd64` and `linux/arm64` from `docker-bake.hcl` in one bake, and Switchyard builds natively on an arm64 runner. A packaging rebuild of a tag released before arm64 builds stays `linux/amd64`.

To run a packaging rebuild by hand:

```bash
gh workflow run packaging-rebuild.yml -f tag=v0.2.40 -f image_revision=1
```
