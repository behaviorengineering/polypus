# Install a release binary

Download the `polypus` archive for your OS from the [latest release](https://github.com/behaviorengineering/polypus/releases/latest), unpack it, and put `polypus` on your `PATH`. Releases ship for Linux, macOS and Windows, on `amd64` and `arm64`.

```bash
polypus version
polypus            # prints the operating guide and every command
```

You can also run a release straight from Go:

```bash
go run github.com/behaviorengineering/polypus/cmd/polypus@latest version
```

The container images publish to GHCR as `ghcr.io/behaviorengineering/polypus` and `ghcr.io/behaviorengineering/polypus-switchyard`, for `linux/amd64` and `linux/arm64`.
