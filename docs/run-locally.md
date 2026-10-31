# Run Polypus on your machine

`polypus serve` starts the gateway on `127.0.0.1:1320`. The local stack runs it under process-compose, with the MLX speech backend, Switchyard, Phoenix and HyperDX beside it.

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
