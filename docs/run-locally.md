# Run Polypus on your machine

`polypus serve` starts the gateway on `127.0.0.1:1320`. The local stack runs it under process-compose, with Switchyard, Phoenix and HyperDX beside it.

```bash
go tool task init     # writes ~/.config/polypus/config.yaml from the example
go tool task build    # bin/polypus and bin/switchyard-server
go tool task serve    # gateway :1320, backends, Phoenix :6006, HyperDX :8080
go tool task serve-down
```

The config lives at `~/.config/polypus/config.yaml`, and `POLYPUS_CONFIG` overrides that path. Polypus reads keys from the environment or the OS keyring (`polypus secret set`), never from the YAML.

```bash
curl -sS http://127.0.0.1:1320/v1/models | jq .
```

## MLX speech

The MLX speech backend is opt-in. `go tool task serve` starts it only when your config sets `processes.mlx: true` or the shell sets `POLYPUS_ENABLE_MLX=1`. The example config that `go tool task init` writes sets `processes.mlx: true`. A config without the key leaves MLX off, and so does a config the gateway cannot read, with a warning. MLX runs on Apple silicon only, and needs `go tool task mlx-sync` once before its first start.

## Tracing

`go tool task serve` starts Phoenix, HyperDX and the OpenTelemetry collector in Docker. When both Phoenix and HyperDX run, it points the gateway at the collector through `POLYPUS_OTLP_ENDPOINT`. A gateway started any other way exports no traces until `POLYPUS_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_ENDPOINT` is set. Failure dumps are written either way.

## The example config

`config.yaml.example` at the repository root is the example people read. `polypus init` embeds a copy of it, `internal/config/config.yaml.example`. Edit the root file, then copy it across:

```bash
go tool task sync-config-example    # copies the root example into internal/config/
go tool task check-config-example   # fails when the two copies differ
```

`go tool task build` and `go tool task init` copy it for you. `go tool task ci` and CI run the check, so a forgotten copy fails the build rather than shipping an old example.
